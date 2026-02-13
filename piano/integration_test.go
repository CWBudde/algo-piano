package piano

import (
	"math"
	"testing"

	algofft "github.com/cwbudde/algo-fft"
	pdefd "github.com/cwbudde/algo-pde/fd"
	pdepoisson "github.com/cwbudde/algo-pde/poisson"
)

func TestLongRenderHasNoNaNOrInf(t *testing.T) {
	const sampleRate = 48000
	params := NewDefaultParams()
	p := NewPiano(sampleRate, 16, params)
	p.NoteOn(48, 80)
	p.NoteOn(60, 90)
	p.NoteOn(72, 110)

	const numBlocks = 300
	const blockSize = 128
	for i := 0; i < numBlocks; i++ {
		out := p.Process(blockSize)
		for j, s := range out {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("non-finite sample at block %d sample %d: %v", i, j, s)
			}
		}
	}
}

func TestAlgoFFTConvolveRealMatchesDirect(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5}
	b := []float32{0.5, -0.25, 0.125}
	got := make([]float32, len(a)+len(b)-1)
	if err := algofft.ConvolveReal(got, a, b); err != nil {
		t.Fatalf("ConvolveReal error: %v", err)
	}

	want := directConvolve(a, b)
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > 1e-4 {
			t.Fatalf("fft convolution mismatch at %d: got=%f want=%f", i, got[i], want[i])
		}
	}
}

func TestAlgoPDEEigenspectrumSanity(t *testing.T) {
	const n = 64
	const h = 1.0 / 64.0

	periodic := pdefd.Eigenvalues(n, h, pdepoisson.Periodic)
	if len(periodic) != n {
		t.Fatalf("unexpected periodic eigenvalue count: %d", len(periodic))
	}
	if math.Abs(periodic[0]) > 1e-12 {
		t.Fatalf("expected periodic zero mode at index 0, got %g", periodic[0])
	}

	dirichlet := pdefd.Eigenvalues(n, h, pdepoisson.Dirichlet)
	if len(dirichlet) != n {
		t.Fatalf("unexpected dirichlet eigenvalue count: %d", len(dirichlet))
	}
	if dirichlet[0] <= 0 {
		t.Fatalf("expected strictly positive first dirichlet eigenvalue, got %g", dirichlet[0])
	}
	for i := 1; i < len(dirichlet); i++ {
		if dirichlet[i] < dirichlet[i-1] {
			t.Fatalf("expected non-decreasing dirichlet eigenspectrum at %d: %g < %g", i, dirichlet[i], dirichlet[i-1])
		}
	}
}
