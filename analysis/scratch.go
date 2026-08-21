package analysis

import (
	"math"
	"sync"
)

var (
	hannCache    sync.Map // map[int][]float64
	scratchPools sync.Map // map[int]*sync.Pool
)

// hannWindow returns a cached periodic-denominator Hann window of length n.
// The returned slice is shared; callers must treat it as read-only.
func hannWindow(n int) []float64 {
	if n <= 0 {
		return nil
	}
	if v, ok := hannCache.Load(n); ok {
		return v.([]float64)
	}
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
	} else {
		for i := range w {
			w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		}
	}
	actual, _ := hannCache.LoadOrStore(n, w)
	return actual.([]float64)
}

// scratch holds the reusable buffers a windowed two-signal spectral comparison
// needs: two windowed time-domain buffers and their two spectra.
type scratch struct {
	aw, bw       []float64
	specA, specB []complex128
}

func scratchPool(n int) *sync.Pool {
	if v, ok := scratchPools.Load(n); ok {
		return v.(*sync.Pool)
	}
	p := &sync.Pool{New: func() any {
		return &scratch{
			aw:    make([]float64, n),
			bw:    make([]float64, n),
			specA: make([]complex128, n/2+1),
			specB: make([]complex128, n/2+1),
		}
	}}
	actual, _ := scratchPools.LoadOrStore(n, p)
	return actual.(*sync.Pool)
}

// getScratch returns buffers sized for an n-point real FFT.
func getScratch(n int) *scratch {
	if n <= 0 {
		return &scratch{}
	}
	return scratchPool(n).Get().(*scratch)
}

// putScratch returns buffers obtained from getScratch. Buffers of the wrong
// size are dropped rather than poisoning the pool.
func putScratch(n int, s *scratch) {
	if s == nil || n <= 0 || len(s.aw) != n || len(s.bw) != n {
		return
	}
	scratchPool(n).Put(s)
}
