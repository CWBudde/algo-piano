package main

import (
	"fmt"
	"math"

	"github.com/cwbudde/algo-piano/internal/gate"
)

// The threshold model itself lives in internal/gate, because cmd/piano-fit now
// enforces the very same file DURING a search (see cmd/piano-fit/
// metric_constraint.go). Only the reporting stays here: what this command
// prints and the exit-2 behaviour are its own contract, and `just gate-c4`
// output is compared byte for byte across changes.

// formatBreach renders one breach as the single-line stderr form.
func formatBreach(b gate.Breach) string {
	over := 0.0
	if b.Max != 0 {
		over = (b.Got/b.Max - 1.0) * 100.0
	}
	if math.IsNaN(over) || math.IsInf(over, 0) {
		over = 0
	}
	return fmt.Sprintf("gate: FAIL %s=%.2f > max %.2f (+%.1f%%)", b.Metric, b.Got, b.Max, over)
}
