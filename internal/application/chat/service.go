package chat

import (
	"context"
	"fmt"
	"io"
	"strings"

	"kirocli-go/internal/application/session"
	appstats "kirocli-go/internal/application/stats"
	"kirocli-go/internal/domain/account"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/truncation"
	"kirocli-go/internal/ports"
)

type Dependencies struct {
	Tokens             ports.TokenProvider
	Upstream           ports.UpstreamClient
	Catalog            ports.ModelCatalog
	Sessions           *session.Manager
	OpenAIFormatter    ports.Formatter
	AnthropicFormatter ports.Formatter
	Cache              *FakeCache
	Stats              *appstats.Collector
	RequestLogs        *appstats.RequestLogRing
}

type Service struct {
	tokens             ports.TokenProvider
	upstream           ports.UpstreamClient
	catalog            ports.ModelCatalog
	sessions           *session.Manager
	openAIFormatter    ports.Formatter
	anthropicFormatter ports.Formatter
	cache              *FakeCache
	stats              *appstats.Collector
	requestLogs        *appstats.RequestLogRing
}

const maxSendAttempts = 3

const (
	autoCompactMinHistoryMessages = 12
	autoCompactMessageDropRatio   = 0.5
	autoCompactMinInputTokens     = 6000
	autoCompactTokenDropRatio     = 0.55

	payloadStrategyReplay        = "replay"
	payloadStrategyReplaySeed    = "replay_seed"
	payloadStrategyStickyFull    = "sticky_full"
	payloadStrategyStickyCompact = "sticky_compact"
)

func NewService(deps Dependencies) (*Service, error) {
	switch {
	case deps.Tokens == nil:
		return nil, fmt.Errorf("chat service: missing token provider")
	case deps.Upstream == nil:
		return nil, fmt.Errorf("chat service: missing upstream client")
	case deps.Catalog == nil:
		return nil, fmt.Errorf("chat service: missing model catalog")
	case deps.OpenAIFormatter == nil:
		return nil, fmt.Errorf("chat service: missing openai formatter")
	case deps.AnthropicFormatter == nil:
		return nil, fmt.Errorf("chat service: missing anthropic formatter")
	default:
		cache := deps.Cache
		if cache == nil {
			cache = NewFakeCache()
		}
		return &Service{
			tokens:             deps.Tokens,
			upstream:           deps.Upstream,
			catalog:            deps.Catalog,
			sessions:           deps.Sessions,
			openAIFormatter:    deps.OpenAIFormatter,
			anthropicFormatter: deps.AnthropicFormatter,
			cache:              cache,
			stats:              deps.Stats,
			requestLogs:        deps.RequestLogs,
		}, nil
	}
}

func (s *Service) Handle(ctx context.Context, req message.UnifiedRequest, format ports.ResponseFormat, w io.Writer) error {
	if s.stats != nil {
		s.stats.RecordRequest()
	}

	if req.Model == "" {
		err := domainerrors.New(domainerrors.CategoryValidation, "model is required")
		s.recordFailure(req, failureMetaFromError(req, err))
		return err
	}

	resolvedModel, err := s.catalog.Resolve(ctx, req.Model)
	if err != nil {
		s.recordFailure(req, failureMetaFromError(req, err))
		return err
	}

	s.prepareRequestMetadata(&req)

	var binding session.Binding
	stickyActive := s.sessions != nil && req.Metadata.StickyEnabled && strings.TrimSpace(req.Metadata.SessionKey) != ""
	if stickyActive {
		binding = s.prepareSessionBinding(&req)
		s.recordCompactReason(req.Metadata.CompactReason)
	}
	req.Metadata.PayloadStrategy = s.determinePayloadStrategy(req)

	formatter := s.formatterFor(format)
	if formatter == nil {
		return domainerrors.New(domainerrors.CategoryValidation, "formatter not configured")
	}

	acquireHint := account.AcquireHint{
		Profile:            account.ProfileCLI,
		Model:              resolvedModel.InternalName,
		Protocol:           string(req.Protocol),
		Stream:             req.Stream,
		PreferredAccountID: req.Metadata.PreferredAccountID,
	}

	var lastErr error
	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		lease, err := s.tokens.Acquire(ctx, acquireHint)
		if err != nil {
			if failure := failureMetaFromError(req, err); failure.Attempts == 0 {
				failure.Attempts = attempt
				s.recordFailure(req, failure)
			}
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		if stickyActive {
			if binding.AccountID != "" && lease.AccountID != binding.AccountID {
				req.Metadata.CompactReason = "account_changed"
				s.recordCompactReason(req.Metadata.CompactReason)
				binding = s.sessions.RotateWithReason(req.Metadata.SessionKey, req.Metadata.WorkingDirectory, req.Metadata.CompactReason)
				req.Metadata.ConversationID = binding.ConversationID
				req.Metadata.ConversationEpoch = binding.Epoch
				req.Metadata.PayloadStrategy = payloadStrategyStickyCompact
			}
			if updated, ok := s.sessions.Update(req.Metadata.SessionKey, session.Update{
				ConversationID:   req.Metadata.ConversationID,
				AccountID:        lease.AccountID,
				WorkingDirectory: req.Metadata.WorkingDirectory,
				Model:            req.Model,
				InputTokens:      req.Metadata.EstimatedInputTokens,
				MessageCount:     len(req.Messages),
				Touch:            true,
			}); ok {
				binding = updated
			}
		}

		upstream, err := s.upstream.Send(ctx, ports.UpstreamRequest{
			Lease:   lease,
			Model:   resolvedModel,
			Request: req,
		})
		if err != nil {
			failure := failureMetaFromError(req, err)
			failure.Attempts = attempt
			_ = s.tokens.ReportFailure(ctx, lease, failure)
			lastErr = err
			s.recordRequestLog(s.buildRequestLogEntry(req, lease.AccountID, false, attempt, appstats.RequestLogEntry{
				StatusCode:    failure.StatusCode,
				Error:         failure.Message,
				FailureReason: string(failure.Reason),
				BodySignal:    failure.BodySignal,
			}))
			if shouldRetry(err, attempt) {
				continue
			}
			s.recordFailure(req, failure)
			return err
		}
		observed := newObservedStream(upstream)

		if req.Stream {
			err = formatter.FormatStream(ctx, req, observed, w)
		} else {
			err = formatter.FormatJSON(ctx, req, observed, w)
		}
		closeErr := observed.Close()
		if err != nil {
			failure := failureMetaFromError(req, err)
			failure.Attempts = attempt
			_ = s.tokens.ReportFailure(ctx, lease, failure)
			s.recordRequestLog(s.buildRequestLogEntry(req, lease.AccountID, false, attempt, appstats.RequestLogEntry{
				StatusCode:    failure.StatusCode,
				Error:         failure.Message,
				FailureReason: string(failure.Reason),
				BodySignal:    failure.BodySignal,
			}))
			s.recordFailure(req, failure)
			return err
		}
		if closeErr != nil {
			failure := failureMetaFromError(req, closeErr)
			failure.Attempts = attempt
			_ = s.tokens.ReportFailure(ctx, lease, failure)
			s.recordRequestLog(s.buildRequestLogEntry(req, lease.AccountID, false, attempt, appstats.RequestLogEntry{
				StatusCode:    failure.StatusCode,
				Error:         failure.Message,
				FailureReason: string(failure.Reason),
				BodySignal:    failure.BodySignal,
			}))
			s.recordFailure(req, failure)
			return closeErr
		}

		success := observed.SuccessMeta(req, attempt)
		_ = s.tokens.ReportSuccess(ctx, lease, success)
		if stickyActive {
			conversationID := observed.ConversationID()
			if conversationID == "" {
				conversationID = req.Metadata.ConversationID
			}
			if updated, ok := s.sessions.Update(req.Metadata.SessionKey, session.Update{
				ConversationID:   conversationID,
				AccountID:        lease.AccountID,
				WorkingDirectory: req.Metadata.WorkingDirectory,
				Model:            req.Model,
				ContextUsagePct:  observed.ContextUsagePercentage(),
				InputTokens:      success.InputTokens,
				MessageCount:     len(req.Messages),
				Touch:            true,
			}); ok {
				binding = updated
			}
			compactReason := ""
			if observed.ShouldAutoCompact(s.autoCompactThreshold()) {
				compactReason = "context_high"
			}
			if observed.WasTruncated() {
				compactReason = "truncated"
			}
			if compactReason != "" {
				req.Metadata.CompactReason = compactReason
				s.recordCompactReason(compactReason)
				binding = s.sessions.RotateWithReason(req.Metadata.SessionKey, req.Metadata.WorkingDirectory, compactReason)
			}
			_ = binding
		}
		if s.stats != nil {
			s.stats.RecordSuccess(success)
		}
		s.recordRequestLog(s.buildRequestLogEntry(req, lease.AccountID, true, attempt, appstats.RequestLogEntry{
			StatusCode:               200,
			InputTokens:              success.InputTokens,
			OutputTokens:             success.OutputTokens,
			TotalTokens:              success.Tokens,
			Credits:                  success.Credits,
			CacheCreationInputTokens: success.CacheCreationInputTokens,
			CacheReadInputTokens:     success.CacheReadInputTokens,
		}))

		// Save truncation info for recovery on the next request.
		if observed.WasTruncated() {
			for id, name := range observed.PendingToolCalls() {
				truncation.GlobalCache.SaveToolTruncation(id, name)
			}
			if content := observed.PartialContent(); content != "" {
				truncation.GlobalCache.SaveContentTruncation(content)
			}
		}

		return nil
	}

	if lastErr != nil {
		s.recordFailure(req, account.FailureMeta{
			RequestID: req.Metadata.ClientRequestID,
			Model:     req.Model,
			Reason:    account.FailureReasonUnknown,
			Message:   lastErr.Error(),
			Attempts:  maxSendAttempts,
		})
		return lastErr
	}
	err = domainerrors.New(domainerrors.CategoryUnknown, "upstream attempts exhausted")
	s.recordFailure(req, failureMetaFromError(req, err))
	return err
}

func (s *Service) formatterFor(format ports.ResponseFormat) ports.Formatter {
	switch format {
	case ports.ResponseFormatOpenAI:
		return s.openAIFormatter
	case ports.ResponseFormatAnthropic:
		return s.anthropicFormatter
	default:
		return nil
	}
}

func (s *Service) prepareRequestMetadata(req *message.UnifiedRequest) {
	if req == nil {
		return
	}

	if req.Protocol != message.ProtocolAnthropic {
		if strings.TrimSpace(req.Metadata.ChatTriggerType) == "" {
			req.Metadata.ChatTriggerType = "MANUAL"
		}
		return
	}

	if req.Metadata.EstimatedInputTokens <= 0 {
		req.Metadata.EstimatedInputTokens = EstimateAnthropicInputTokens(*req)
	}

	req.Metadata.RemainingInputTokens = req.Metadata.EstimatedInputTokens
	if req.Metadata.FakeCacheKey == 0 || s.cache == nil {
		if strings.TrimSpace(req.Metadata.ChatTriggerType) == "" {
			req.Metadata.ChatTriggerType = "MANUAL"
		}
		return
	}

	cacheHit := s.cache.Lookup(req.Metadata.FakeCacheKey)
	req.Metadata.CacheHit = cacheHit
	cacheCreation, cacheRead, remaining := ComputeCacheUsage(req.Metadata.EstimatedInputTokens, cacheHit)
	req.Metadata.CacheCreationInputTokens = cacheCreation
	req.Metadata.CacheReadInputTokens = cacheRead
	req.Metadata.RemainingInputTokens = remaining
	if strings.TrimSpace(req.Metadata.ChatTriggerType) == "" {
		req.Metadata.ChatTriggerType = "MANUAL"
	}
}

func (s *Service) prepareSessionBinding(req *message.UnifiedRequest) session.Binding {
	if req == nil || s.sessions == nil || !req.Metadata.StickyEnabled || strings.TrimSpace(req.Metadata.SessionKey) == "" {
		return session.Binding{}
	}

	workdir := strings.TrimSpace(req.Metadata.WorkingDirectory)
	var binding session.Binding
	if req.Metadata.CompactRequested {
		req.Metadata.CompactReason = "manual"
		binding = s.sessions.RotateWithReason(req.Metadata.SessionKey, workdir, req.Metadata.CompactReason)
	} else if existing, ok := s.sessions.Get(req.Metadata.SessionKey); ok {
		if s.shouldRotateForClientCompact(existing, *req) {
			req.Metadata.CompactReason = "client_compacted"
			binding = s.sessions.RotateWithReason(req.Metadata.SessionKey, workdir, req.Metadata.CompactReason)
		} else {
			binding = existing
		}
	} else {
		binding = s.sessions.Ensure(req.Metadata.SessionKey, workdir)
	}

	req.Metadata.ConversationID = binding.ConversationID
	req.Metadata.ConversationEpoch = binding.Epoch
	req.Metadata.PreferredAccountID = binding.AccountID
	return binding
}

func (s *Service) autoCompactThreshold() float64 {
	if s.sessions == nil {
		return 0.85
	}
	return s.sessions.ContextCompactThreshold()
}

func (s *Service) shouldRotateForClientCompact(binding session.Binding, req message.UnifiedRequest) bool {
	currentMessages := len(req.Messages)
	previousMessages := binding.LastMessageCount
	messageDrop := previousMessages >= autoCompactMinHistoryMessages &&
		currentMessages > 0 &&
		float64(currentMessages) <= float64(previousMessages)*autoCompactMessageDropRatio

	currentTokens := req.Metadata.EstimatedInputTokens
	previousTokens := binding.LastInputTokens
	tokenDrop := previousTokens >= autoCompactMinInputTokens &&
		currentTokens > 0 &&
		float64(currentTokens) <= float64(previousTokens)*autoCompactTokenDropRatio

	return messageDrop || tokenDrop
}

func (s *Service) determinePayloadStrategy(req message.UnifiedRequest) string {
	if !req.Metadata.StickyEnabled || strings.TrimSpace(req.Metadata.SessionKey) == "" {
		return payloadStrategyReplay
	}
	if strings.TrimSpace(req.Metadata.CompactReason) != "" {
		return payloadStrategyStickyCompact
	}
	if strings.TrimSpace(req.Metadata.PreferredAccountID) == "" {
		return payloadStrategyReplaySeed
	}
	return payloadStrategyStickyFull
}

func failureMetaFromError(req message.UnifiedRequest, err error) account.FailureMeta {
	meta := account.FailureMeta{
		RequestID: req.Metadata.ClientRequestID,
		Model:     req.Model,
		Reason:    account.FailureReasonUnknown,
		Message:   err.Error(),
	}

	if upstreamErr, ok := err.(*domainerrors.UpstreamError); ok {
		meta.BodySignal = upstreamErr.Signal
		meta.StatusCode = upstreamErr.StatusCode
		switch upstreamErr.Category {
		case domainerrors.CategoryAuth:
			meta.Reason = account.FailureReasonAuth
		case domainerrors.CategoryQuota:
			meta.Reason = account.FailureReasonQuota
		case domainerrors.CategoryBan:
			meta.Reason = account.FailureReasonBan
		case domainerrors.CategoryNetwork:
			meta.Reason = account.FailureReasonNetwork
		case domainerrors.CategoryNotImplemented:
			meta.Reason = account.FailureReasonNotImplemented
		default:
			meta.Reason = account.FailureReasonUnknown
		}
	}

	return meta
}

func shouldRetry(err error, attempt int) bool {
	if attempt >= maxSendAttempts {
		return false
	}
	upstreamErr, ok := err.(*domainerrors.UpstreamError)
	if !ok {
		return false
	}
	if upstreamErr.Category == domainerrors.CategoryQuota || upstreamErr.Category == domainerrors.CategoryNetwork {
		return true
	}
	return upstreamErr.Retryable
}

func (s *Service) recordFailure(req message.UnifiedRequest, meta account.FailureMeta) {
	if s.stats != nil {
		s.stats.RecordFailure(meta)
	}
}

func (s *Service) recordRequestLog(entry appstats.RequestLogEntry) {
	if s.requestLogs != nil {
		s.requestLogs.Add(entry)
	}
}

func (s *Service) buildRequestLogEntry(req message.UnifiedRequest, accountID string, success bool, attempts int, extra appstats.RequestLogEntry) appstats.RequestLogEntry {
	entry := appstats.RequestLogEntry{
		RequestID:         req.Metadata.ClientRequestID,
		Protocol:          string(req.Protocol),
		Endpoint:          req.Metadata.Endpoint,
		Model:             req.Model,
		AccountID:         accountID,
		APIKeyID:          req.Metadata.APIKeyID,
		StickySession:     req.Metadata.StickyEnabled,
		ConversationID:    req.Metadata.ConversationID,
		ConversationEpoch: req.Metadata.ConversationEpoch,
		CompactReason:     req.Metadata.CompactReason,
		PayloadStrategy:   req.Metadata.PayloadStrategy,
		CacheHit:          req.Metadata.CacheHit,
		Success:           success,
		Attempts:          attempts,
	}
	if extra.StatusCode != 0 {
		entry.StatusCode = extra.StatusCode
	}
	entry.Error = extra.Error
	entry.FailureReason = extra.FailureReason
	entry.BodySignal = extra.BodySignal
	entry.InputTokens = extra.InputTokens
	entry.OutputTokens = extra.OutputTokens
	entry.TotalTokens = extra.TotalTokens
	entry.Credits = extra.Credits
	entry.CacheCreationInputTokens = extra.CacheCreationInputTokens
	entry.CacheReadInputTokens = extra.CacheReadInputTokens
	return entry
}

func (s *Service) recordCompactReason(reason string) {
	if s.stats != nil && strings.TrimSpace(reason) != "" {
		s.stats.RecordCompact(reason)
	}
}
