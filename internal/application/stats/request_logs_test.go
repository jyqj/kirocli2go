package stats

import "testing"

func TestRequestLogRingKeepsNewestEntries(t *testing.T) {
	ring := NewRequestLogRing(3)
	ring.Add(RequestLogEntry{RequestID: "1"})
	ring.Add(RequestLogEntry{RequestID: "2"})
	ring.Add(RequestLogEntry{RequestID: "3"})
	ring.Add(RequestLogEntry{RequestID: "4"})

	entries := ring.List(10)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].RequestID != "4" || entries[1].RequestID != "3" || entries[2].RequestID != "2" {
		t.Fatalf("unexpected request log order: %+v", entries)
	}
}

func TestRequestLogRingQueryFiltersAndOffset(t *testing.T) {
	ring := NewRequestLogRing(10)
	ring.Add(RequestLogEntry{RequestID: "1", Protocol: "openai", Success: true})
	ring.Add(RequestLogEntry{RequestID: "2", Protocol: "anthropic", Success: false, FailureReason: "quota_error", BodySignal: "MONTHLY_REQUEST_COUNT", ConversationID: "conv-1", CompactReason: "manual", APIKeyID: "team-a"})
	ring.Add(RequestLogEntry{RequestID: "3", Protocol: "anthropic", Success: false, FailureReason: "network_error", BodySignal: "TEMPORARILY_SUSPENDED", ConversationID: "conv-2", CompactReason: "context_high", APIKeyID: "team-b"})
	ring.Add(RequestLogEntry{RequestID: "4", Protocol: "anthropic", Success: true, ConversationID: "conv-2", APIKeyID: "team-b"})

	success := false
	entries := ring.Query(RequestLogQuery{
		Protocol: "anthropic",
		Success:  &success,
		Limit:    10,
	})
	if len(entries) != 2 {
		t.Fatalf("expected 2 anthropic failures, got %d", len(entries))
	}
	if entries[0].RequestID != "3" || entries[1].RequestID != "2" {
		t.Fatalf("unexpected filtered order: %+v", entries)
	}

	entries = ring.Query(RequestLogQuery{
		Protocol: "anthropic",
		Limit:    1,
		Offset:   1,
	})
	if len(entries) != 1 || entries[0].RequestID != "3" {
		t.Fatalf("unexpected paged result: %+v", entries)
	}

	entries = ring.Query(RequestLogQuery{
		BodySignal: "TEMPORARILY_SUSPENDED",
		Limit:      10,
	})
	if len(entries) != 1 || entries[0].RequestID != "3" {
		t.Fatalf("unexpected body signal filtered result: %+v", entries)
	}

	entries = ring.Query(RequestLogQuery{
		ConversationID:  "conv-2",
		CompactReason:   "context_high",
		PayloadStrategy: "",
		Limit:           10,
	})
	if len(entries) != 1 || entries[0].RequestID != "3" {
		t.Fatalf("unexpected conversation/compact filtered result: %+v", entries)
	}

	entries = ring.Query(RequestLogQuery{
		APIKeyID: "team-b",
		Limit:    10,
	})
	if len(entries) != 2 || entries[0].RequestID != "4" || entries[1].RequestID != "3" {
		t.Fatalf("unexpected api key filtered result: %+v", entries)
	}
}
