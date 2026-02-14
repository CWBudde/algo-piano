package fitcommon

import (
	"fmt"
	"strconv"
	"strings"
)

func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ParseWorkers(raw string) (int, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return 0, fmt.Errorf("empty value (use integer >= 1 or 'auto')")
	}
	if v == "auto" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%q (use integer >= 1 or 'auto')", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("%d (must be >= 1 or 'auto')", n)
	}
	return n, nil
}
