package analysis

import (
	"math"
	"testing"
)

func BenchmarkSpectralRMSEDB_FFT(b *testing.B) {
	const n = 4096
	a, c := benchmarkSignals(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = spectralRMSEDB(a, c)
	}
}

func BenchmarkSpectralRMSEDB_Naive(b *testing.B) {
	const n = 4096
	a, c := benchmarkSignals(n)
	aw, cw, bins := spectralWindowedInputs(a, c)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = spectralRMSEDBNaiveWindowed(aw, cw, bins)
	}
}

func BenchmarkCompare(b *testing.B) {
	const n = 48000 * 3
	ref, cand := benchmarkSignals(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compare(ref, cand, 48000)
	}
}

func benchmarkSignals(n int) ([]float64, []float64) {
	a := make([]float64, n)
	c := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n)
		a[i] = 0.7*math.Sin(2*math.Pi*57*t) + 0.25*math.Sin(2*math.Pi*311*t)
		c[i] = 0.68*math.Sin(2*math.Pi*57*t+0.05) + 0.27*math.Sin(2*math.Pi*320*t)
	}
	return a, c
}
