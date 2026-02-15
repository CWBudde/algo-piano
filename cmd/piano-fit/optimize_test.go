package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewMayflyConfig(t *testing.T) {
	tests := []struct {
		variant string
		wantErr bool
	}{
		{variant: "ma"},
		{variant: "desma"},
		{variant: "olce"},
		{variant: "eobbma"},
		{variant: "gsasma"},
		{variant: "mpma"},
		{variant: "aoblmoa"},
		{variant: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.variant, func(t *testing.T) {
			cfg, err := newMayflyConfig(tt.variant, 10, 5, 20)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("newMayflyConfig(%q) expected error", tt.variant)
				}
				return
			}
			if err != nil {
				t.Fatalf("newMayflyConfig(%q) unexpected error: %v", tt.variant, err)
			}
			if cfg.ProblemSize != 5 {
				t.Fatalf("ProblemSize = %d, want 5", cfg.ProblemSize)
			}
			if cfg.NPop != 10 {
				t.Fatalf("NPop = %d, want 10", cfg.NPop)
			}
			if cfg.MaxIterations != 20 {
				t.Fatalf("MaxIterations = %d, want 20", cfg.MaxIterations)
			}
		})
	}
}

func TestReserveEvalCapsAtMax(t *testing.T) {
	const (
		maxEvals = 47
		workers  = 8
	)

	var evals int64
	var granted int64
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, ok := reserveEval(&evals, maxEvals); !ok {
					return
				}
				atomic.AddInt64(&granted, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&granted); got != maxEvals {
		t.Fatalf("granted evaluations = %d, want %d", got, maxEvals)
	}
	if got := atomic.LoadInt64(&evals); got != maxEvals {
		t.Fatalf("eval counter = %d, want %d", got, maxEvals)
	}
}

func TestCloneCandidateCopiesSlice(t *testing.T) {
	orig := candidate{Vals: []float64{1.0, 2.0, 3.0}}
	cloned := cloneCandidate(orig)
	cloned.Vals[0] = 99.0

	if orig.Vals[0] != 1.0 {
		t.Fatalf("clone mutated original: got %.1f want 1.0", orig.Vals[0])
	}
}
