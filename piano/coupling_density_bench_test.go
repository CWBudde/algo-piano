package piano

import (
	"fmt"
	"testing"
)

// couplingDensityNotes is the fixed load every density case runs under: eight
// mid-register two-string keys with the sustain pedal held. The struck keys are
// held constant on purpose — the only thing that moves between sub-benchmarks
// is how dense the precomputed coupling graph is. The number of *active* notes
// is not constant: a denser graph recruits more targets into the active set,
// which is exactly the effect the benchmark is there to expose.
var couplingDensityNotes = []int{40, 44, 48, 52, 56, 60, 64, 68}

// BenchmarkStringBankCouplingGraphDensity measures how the cost of a 128-frame
// block at 48 kHz scales with the density of the sparse coupling graph. This is
// PLAN.md 9.6's "coupling graph density/top-K scaling vs CPU".
//
// Two independent density knobs are swept, both against the same fixed
// 8-struck-key load:
//
//   - topK sweeps Params.CouplingMaxNeighbors, the top-K cap that
//     initPhysicalCouplingGraph applies after ranking every candidate target by
//     its physical coupling weight. This is the knob a preset actually exposes.
//   - weightThreshold sweeps an edge-weight floor applied on top of a fixed
//     top-K of 32. The production floor (couplingPhysicalMinScore) is a compile
//     time constant, so the sweep prunes the built graph instead, dropping every
//     edge whose share of its source's total gain falls below the threshold.
//     Pruning deliberately does not renormalise the surviving gains: the point
//     is to measure what a sparser graph costs, not to keep the render
//     identical.
//
// Each case reports three custom metrics alongside sec/op:
//
//	edges        total directed edges in the whole 88-key graph
//	active-edges directed edges leaving the active set at the end of the run
//	active       notes in the active set at the end of the run
//
// active-edges is the number that predicts CPU: applySparseCoupling only walks
// edges leaving active sources. It is larger than the eight struck keys imply,
// because injecting force into a target enrols that target in the active set,
// so a denser graph both costs more per source and recruits more sources.
func BenchmarkStringBankCouplingGraphDensity(b *testing.B) {
	b.Run("topK", func(b *testing.B) {
		for _, k := range []int{1, 2, 4, 8, 10, 16, 32, 64, 87} {
			b.Run(fmt.Sprintf("maxNeighbors%d", k), func(b *testing.B) {
				benchmarkCouplingGraphDensity(b, k, 0)
			})
		}
	})

	b.Run("weightThreshold", func(b *testing.B) {
		for _, share := range []float32{0, 0.02, 0.05, 0.10, 0.20} {
			b.Run(fmt.Sprintf("minShare%03.0f", share*1000), func(b *testing.B) {
				benchmarkCouplingGraphDensity(b, 32, share)
			})
		}
	})
}

func benchmarkCouplingGraphDensity(b *testing.B, maxNeighbors int, minShare float32) {
	b.Helper()

	params := NewDefaultParams()
	params.ResonanceEnabled = false
	params.CouplingEnabled = true
	params.CouplingMode = CouplingModePhysical
	params.CouplingAmount = 1.0
	params.CouplingMaxNeighbors = maxNeighbors

	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)
	pruneCouplingEdgesByShare(sb, minShare)
	totalEdges := couplingEdgeCount(sb)

	sb.SetSustain(true)
	for _, note := range couplingDensityNotes {
		sb.SetKeyDown(note, true)
		h.Trigger(note, 110)
	}
	for i := 0; i < 64; i++ {
		_ = sb.Process(128, h)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 && i%benchmarkRetriggerEvery == 0 {
			b.StopTimer()
			retriggerBenchmarkBank(sb, h, couplingDensityNotes)
			b.StartTimer()
		}
		_ = sb.Process(128, nil)
	}
	b.StopTimer()

	b.ReportMetric(float64(totalEdges), "edges")
	b.ReportMetric(float64(activeCouplingEdgeCount(sb)), "active-edges")
	b.ReportMetric(float64(len(sb.activeNotes)), "active")
}

// couplingEdgeCount counts every directed edge in the precomputed graph.
func couplingEdgeCount(sb *StringBank) int {
	total := 0
	for src := range sb.coupling {
		total += len(sb.coupling[src])
	}
	return total
}

// activeCouplingEdgeCount counts the directed edges applySparseCoupling would
// actually walk for the bank's current active set — the per-sample edge work.
func activeCouplingEdgeCount(sb *StringBank) int {
	total := 0
	for _, src := range sb.activeNotes {
		total += len(sb.coupling[src])
	}
	return total
}

// pruneCouplingEdgesByShare drops every edge whose gain is a smaller share of
// its source's total outgoing gain than minShare, emulating a higher edge
// weight threshold than the compile-time couplingPhysicalMinScore. Surviving
// gains are left untouched, so this thins the graph without redistributing
// energy onto the edges that remain.
func pruneCouplingEdgesByShare(sb *StringBank, minShare float32) {
	if minShare <= 0 {
		return
	}
	for src := range sb.coupling {
		edges := sb.coupling[src]
		if len(edges) == 0 {
			continue
		}
		var sum float32
		for _, e := range edges {
			sum += e.gain
		}
		if sum <= 0 {
			continue
		}
		kept := edges[:0]
		for _, e := range edges {
			if e.gain/sum >= minShare {
				kept = append(kept, e)
			}
		}
		sb.coupling[src] = kept
	}
}
