package openai

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStoredOpenAIResponseStore_AppendsLifecycleReplayEvents(t *testing.T) {
	store := &storedOpenAIResponseStore{
		items: make(map[string]storedOpenAIResponse),
		tasks: make(map[string]storedOpenAIResponseTask),
	}

	store.Store("resp_store_replay", []byte(`{"id":"resp_store_replay","object":"response","status":"queued"}`), []byte(`[]`))

	if ok := store.AppendReplayEvent("resp_store_replay", []byte(`{"type":"response.created","response":{"id":"resp_store_replay","status":"queued"}}`)); !ok {
		t.Fatal("AppendReplayEvent(created) = false, want true")
	}
	if ok := store.AppendReplayEvent("resp_store_replay", []byte(`{"type":"response.queued","response":{"id":"resp_store_replay","status":"queued"}}`)); !ok {
		t.Fatal("AppendReplayEvent(queued) = false, want true")
	}
	if ok := store.AppendReplayEvent("resp_store_replay", []byte(`{"type":"response.in_progress","response":{"id":"resp_store_replay","status":"in_progress"}}`)); !ok {
		t.Fatal("AppendReplayEvent(in_progress) = false, want true")
	}

	stored, ok := store.Load("resp_store_replay")
	if !ok {
		t.Fatal("Load(resp_store_replay) = false, want true")
	}

	gotTypes := responseReplayEventTypes(stored.ReplayEvents)
	wantTypes := []string{"response.created", "response.queued", "response.in_progress"}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("replay event types = %v, want %v", gotTypes, wantTypes)
	}
}

func TestStoredOpenAIResponseStore_CancelledResponsesReplayTerminalEvents(t *testing.T) {
	store := &storedOpenAIResponseStore{
		items: make(map[string]storedOpenAIResponse),
		tasks: make(map[string]storedOpenAIResponseTask),
	}

	store.Store("resp_store_cancelled", []byte(`{"id":"resp_store_cancelled","object":"response","status":"queued"}`), []byte(`[]`))
	if ok := store.AppendReplayEvent("resp_store_cancelled", []byte(`{"type":"response.created","response":{"id":"resp_store_cancelled","status":"queued"}}`)); !ok {
		t.Fatal("AppendReplayEvent(created) = false, want true")
	}
	if ok := store.AppendReplayEvent("resp_store_cancelled", []byte(`{"type":"response.cancelled","response":{"id":"resp_store_cancelled","status":"cancelled"}}`)); !ok {
		t.Fatal("AppendReplayEvent(cancelled) = false, want true")
	}
	if ok := store.AppendReplayEvent("resp_store_cancelled", []byte(`{"type":"response.done","response":{"id":"resp_store_cancelled","status":"cancelled"}}`)); !ok {
		t.Fatal("AppendReplayEvent(done) = false, want true")
	}

	stored, ok := store.Load("resp_store_cancelled")
	if !ok {
		t.Fatal("Load(resp_store_cancelled) = false, want true")
	}

	gotTypes := responseReplayEventTypes(stored.ReplayEvents)
	wantTypes := []string{"response.created", "response.cancelled", "response.done"}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("replay event types = %v, want %v", gotTypes, wantTypes)
	}
}

func TestStoredOpenAIResponseStore_LegacyStoreCallersStillLoadWithoutReplayEvents(t *testing.T) {
	store := &storedOpenAIResponseStore{
		items: make(map[string]storedOpenAIResponse),
		tasks: make(map[string]storedOpenAIResponseTask),
	}

	store.Store("resp_store_legacy", []byte(`{"id":"resp_store_legacy","object":"response","status":"completed"}`), []byte(`[]`))

	stored, ok := store.Load("resp_store_legacy")
	if !ok {
		t.Fatal("Load(resp_store_legacy) = false, want true")
	}
	if len(stored.ReplayEvents) != 0 {
		t.Fatalf("legacy Store replay events len = %d, want 0", len(stored.ReplayEvents))
	}
}

func responseReplayEventTypes(events [][]byte) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, gjson.GetBytes(event, "type").String())
	}
	return types
}
