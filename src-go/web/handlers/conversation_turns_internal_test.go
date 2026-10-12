package handlers

// Internal coverage for the ported turn-window algorithm (unexported), with a
// synthetic turn list that loadConversationTurns cannot yet produce over HTTP.

import "testing"

func tt(role string, ms int64) convTurn {
	return convTurn{role: role, tsMillis: ms, hasTS: true, raw: map[string]any{"role": role}}
}

func TestPrefixFingerprintAndBounds(t *testing.T) {
	// empty prefix = FNV offset basis.
	if got := prefixFingerprint(nil); got != "cbf29ce484222325" {
		t.Fatalf("empty fingerprint = %s", got)
	}
	// deterministic + order-sensitive.
	a := []convTurn{tt("user", 1000), tt("assistant", 2000)}
	b := []convTurn{tt("assistant", 2000), tt("user", 1000)}
	if prefixFingerprint(a) == prefixFingerprint(b) {
		t.Fatalf("fingerprint not order-sensitive")
	}
	if prefixFingerprint(a) != prefixFingerprint([]convTurn{tt("user", 1000), tt("assistant", 2000)}) {
		t.Fatalf("fingerprint not deterministic")
	}

	// round-align walks a window start back to a user boundary.
	turns := []convTurn{tt("user", 1), tt("assistant", 2), tt("assistant", 3), tt("user", 4), tt("assistant", 5)}
	if got := roundAlignBackward(turns, 2); got != 0 {
		t.Fatalf("roundAlignBackward(2) = %d, want 0", got)
	}
	if got := roundAlignBackward(turns, 4); got != 3 {
		t.Fatalf("roundAlignBackward(4) = %d, want 3 (already user)", got)
	}

	// page bounds: end clamps to total, start aligns back to a user turn.
	start, end := resolvePageBounds(turns, 100, 2)
	if end != 5 {
		t.Fatalf("end = %d, want 5 (clamped to total)", end)
	}
	if turns[start].role != "user" && start != 0 {
		t.Fatalf("page start %d is not a user boundary", start)
	}

	// window meta: assistant_before counts assistants in the prefix.");
	meta := computeWindowMeta(turns, 4)
	if meta.total != 5 || meta.offset != 4 {
		t.Fatalf("meta offset/total = %d/%d", meta.offset, meta.total)
	}
	if meta.assistantBefore != 2 {
		t.Fatalf("assistantBefore = %d, want 2", meta.assistantBefore)
	}
	if meta.uncoveredMaxTS == nil {
		t.Fatalf("uncoveredMaxTS should be set for a non-empty prefix")
	}
}
