package analysis

import (
	"math"
	"testing"
)

func TestHannWindowIsCachedAndCorrect(t *testing.T) {
	const n = 4096
	w1 := hannWindow(n)
	w2 := hannWindow(n)
	if len(w1) != n {
		t.Fatalf("expected a window of %f samples, got %f", float64(n), float64(len(w1)))
	}
	if &w1[0] != &w2[0] {
		t.Fatalf("expected hannWindow to return the cached slice, got a fresh allocation")
	}
	for i := 0; i < n; i++ {
		want := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		if w1[i] != want {
			t.Fatalf("expected window[%d] = %.17g, got %.17g", i, want, w1[i])
		}
	}
}

func TestScratchPoolReusesBuffers(t *testing.T) {
	const n = 2048
	s := getScratch(n)
	if len(s.aw) != n || len(s.bw) != n {
		t.Fatalf("expected time buffers of %f samples, got %f and %f", float64(n), float64(len(s.aw)), float64(len(s.bw)))
	}
	if len(s.specA) != n/2+1 || len(s.specB) != n/2+1 {
		t.Fatalf("expected spectra of %f bins, got %f and %f", float64(n/2+1), float64(len(s.specA)), float64(len(s.specB)))
	}
	// Reuse is best effort: under -race, sync.Pool.Put drops the value at
	// random roughly a quarter of the time, and a GC may clear the pool. Retry
	// so a miss cannot fail the test while a pool that never recycles still does.
	const attempts = 32
	reused := false
	for i := 0; i < attempts && !reused; i++ {
		first := &s.aw[0]
		putScratch(n, s)
		s = getScratch(n)
		reused = &s.aw[0] == first
	}
	if !reused {
		t.Fatalf("expected the pooled scratch buffer to be handed back out within %d attempts", attempts)
	}
	putScratch(n, s)
}

func TestPutScratchRejectsMismatchedSize(t *testing.T) {
	const n = 1024
	bad := &scratch{aw: make([]float64, 8), bw: make([]float64, 8)}
	putScratch(n, bad) // Must not poison the pool.
	s := getScratch(n)
	if len(s.aw) != n {
		t.Fatalf("expected a correctly sized buffer of %f samples, got %f", float64(n), float64(len(s.aw)))
	}
	putScratch(n, s)
}

func TestSpectralRMSEDBMatchesNaiveReference(t *testing.T) {
	a := randomSignal(4096, 31)
	b := randomSignal(4096, 32)
	aw, bw, bins := spectralWindowedInputs(a, b)
	want := spectralRMSEDBNaiveWindowed(aw, bw, bins)
	got := spectralRMSEDB(a, b)
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected spectral RMSE %f, got %f", want, got)
	}
}
