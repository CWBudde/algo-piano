package main

import (
	"testing"

	"github.com/cwbudde/algo-piano/internal/gate"
)

// The threshold-resolution tests moved with the code they cover, to
// internal/gate. What is left here is the part that did not move: the exact
// stderr line this command prints, which `just gate-c4` output is compared
// against byte for byte.

func TestFormatBreach(t *testing.T) {
	b := gate.Breach{Metric: "spectral_rmse_db", Got: 61.2, Max: 60.0}
	if line := formatBreach(b); line != "gate: FAIL spectral_rmse_db=61.20 > max 60.00 (+2.0%)" {
		t.Errorf("formatBreach = %q", line)
	}
}

func TestFormatBreachWithZeroMax(t *testing.T) {
	// Evaluate rejects a zero threshold before a breach can carry one, so this
	// only guards against a NaN reaching the output if that ever changes.
	b := gate.Breach{Metric: "score", Got: 1.0, Max: 0}
	if line := formatBreach(b); line != "gate: FAIL score=1.00 > max 0.00 (+0.0%)" {
		t.Errorf("formatBreach = %q", line)
	}
}
