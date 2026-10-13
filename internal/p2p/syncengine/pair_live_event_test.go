package syncengine

import (
	"context"
	"testing"
)

func TestPairLiveEventDataScopesStateReports(t *testing.T) {
	legacy := PairLiveEventData(context.Background(), map[string]any{"manifestHash": "A"})
	if _, ok := legacy["pairLiveEpoch"]; ok {
		t.Fatal("legacy event gained a live session")
	}
	pair := PairLiveEventData(WithPairLiveEpoch(context.Background(), "decision-123"), map[string]any{"manifestHash": "B"})
	if pair["pairLiveEpoch"] != "decision-123" || pair["manifestHash"] != "B" {
		t.Fatalf("unbound completion payload %#v", pair)
	}
}
