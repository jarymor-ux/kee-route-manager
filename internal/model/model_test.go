package model

import (
	"fmt"
	"testing"
	"time"
)

func TestNewStateStartsUnconfiguredWithUniqueSlots(t *testing.T) {
	before := time.Now()
	state := NewState("1.0.0-rc.2", "krm-slot-", 12)
	if state.SchemaVersion != StateSchema || state.Version != "1.0.0-rc.2" || state.ActiveSlot != -1 || state.ActiveNodeID != "" || state.DirectMode || state.XrayConfigured {
		t.Fatalf("new state incorrectly claims a working route: %+v", state)
	}
	if state.StartedAt.Before(before) || state.StartedAt.After(time.Now()) || !state.StartedAt.Equal(state.UpdatedAt) || state.StartedAt.Location() != time.UTC {
		t.Fatalf("invalid initial timestamps: %s %s", state.StartedAt, state.UpdatedAt)
	}
	if len(state.Pool) != 12 || state.Measurements == nil || state.Sources == nil {
		t.Fatal("missing initial containers")
	}
	for i, slot := range state.Pool {
		if slot.Index != i || slot.Tag != fmt.Sprintf("krm-slot-%d", i) || slot.NodeID != "" || slot.Healthy {
			t.Errorf("slot %d: %+v", i, slot)
		}
	}
	state.Sources["provider"] = SourceState{ID: "provider"}
	state.Measurements["node"] = Measurement{NodeID: "node"}
	other := NewState("version", "other-", 1)
	if len(other.Sources) != 0 || len(other.Measurements) != 0 || other.Pool[0].Tag != "other-0" {
		t.Fatal("new controller inherited another controller's mutable state")
	}
}
