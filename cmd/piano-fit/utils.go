package main

import (
	"fmt"
	"os"

	fitcommon "github.com/cwbudde/algo-piano/internal/fitcommon"
)

func clamp(v, lo, hi float64) float64 {
	return fitcommon.Clamp(v, lo, hi)
}

func minInt(a, b int) int {
	return fitcommon.MinInt(a, b)
}

func maxInt(a, b int) int {
	return fitcommon.MaxInt(a, b)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
