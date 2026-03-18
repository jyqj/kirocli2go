package ports

import (
	"context"

	"kirocli-go/internal/domain/account"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/model"
	"kirocli-go/internal/domain/stream"
)

type UpstreamRequest struct {
	Lease   account.Lease
	Model   model.ResolvedModel
	Request message.UnifiedRequest
}

type UpstreamStream interface {
	Next(ctx context.Context) (stream.Event, error)
	Close() error
}

type UpstreamClient interface {
	Send(ctx context.Context, req UpstreamRequest) (UpstreamStream, error)
}
