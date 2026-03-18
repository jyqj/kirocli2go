package stats

import (
	"testing"

	"kirocli-go/internal/domain/account"
)

func TestCollectorAggregatesSuccessAndFailure(t *testing.T) {
	collector := NewCollector()

	collector.RecordRequest()
	collector.RecordSuccess(account.SuccessMeta{
		InputTokens:  10,
		OutputTokens: 20,
		Tokens:       30,
		Credits:      1.5,
		Attempts:     2,
	})

	collector.RecordRequest()
	collector.RecordFailure(account.FailureMeta{
		Attempts: 3,
	})

	snapshot := collector.Snapshot()
	if snapshot.TotalRequests != 2 {
		t.Fatalf("expected 2 total requests, got %d", snapshot.TotalRequests)
	}
	if snapshot.SuccessRequests != 1 {
		t.Fatalf("expected 1 success request, got %d", snapshot.SuccessRequests)
	}
	if snapshot.FailedRequests != 1 {
		t.Fatalf("expected 1 failed request, got %d", snapshot.FailedRequests)
	}
	if snapshot.TotalInputTokens != 10 || snapshot.TotalOutputTokens != 20 || snapshot.TotalTokens != 30 {
		t.Fatalf("unexpected token totals: %+v", snapshot)
	}
	if snapshot.TotalCredits != 1.5 {
		t.Fatalf("expected credits 1.5, got %v", snapshot.TotalCredits)
	}
	if snapshot.AttemptFailedRequests != 4 {
		t.Fatalf("expected 4 attempt failures, got %d", snapshot.AttemptFailedRequests)
	}
	if snapshot.TotalRetries != 3 {
		t.Fatalf("expected 3 total retries, got %d", snapshot.TotalRetries)
	}
}
