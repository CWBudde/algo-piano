package piano

import (
	"fmt"
	"math"
	"testing"
)

func BenchmarkStringBankCouplingModes(b *testing.B) {
	cases := []struct {
		name        string
		sustainDown bool
		notes       []int
	}{
		{name: "poly1_pedalUp", sustainDown: false, notes: []int{60}},
		{name: "poly1_pedalDown", sustainDown: true, notes: []int{60}},
		{name: "poly8_pedalUp", sustainDown: false, notes: []int{48, 52, 55, 60, 64, 67, 72, 76}},
		{name: "poly8_pedalDown", sustainDown: true, notes: []int{48, 52, 55, 60, 64, 67, 72, 76}},
	}

	modes := []CouplingMode{
		CouplingModeOff,
		CouplingModeStatic,
		CouplingModePhysical,
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			for _, mode := range modes {
				mode := mode
				b.Run(fmt.Sprintf("mode_%s", mode), func(b *testing.B) {
					benchmarkStringBankCouplingMode(b, mode, tc.notes, tc.sustainDown)
				})
			}
		})
	}
}

func BenchmarkStringBankPhysicalCouplingTargetPolicy(b *testing.B) {
	cases := []struct {
		name        string
		sustainDown bool
		notes       []int
	}{
		{name: "poly1_pedalUp", sustainDown: false, notes: []int{60}},
		{name: "poly1_pedalDown", sustainDown: true, notes: []int{60}},
		{name: "poly8_pedalUp", sustainDown: false, notes: []int{48, 52, 55, 60, 64, 67, 72, 76}},
		{name: "poly8_pedalDown", sustainDown: true, notes: []int{48, 52, 55, 60, 64, 67, 72, 76}},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			b.Run("mode_physical_allTargets", func(b *testing.B) {
				benchmarkStringBankCouplingMode(b, CouplingModePhysical, tc.notes, tc.sustainDown)
			})
			b.Run("mode_physical_undampedTargetsOnly", func(b *testing.B) {
				benchmarkStringBankCouplingModeUndampedTargetsOnly(b, tc.notes, tc.sustainDown)
			})
		})
	}
}

func benchmarkStringBankCouplingMode(b *testing.B, mode CouplingMode, notes []int, sustainDown bool) {
	sb, _ := setupBenchmarkStringBank(mode, notes, sustainDown)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sb.Process(128, nil)
	}
}

func benchmarkStringBankCouplingModeUndampedTargetsOnly(b *testing.B, notes []int, sustainDown bool) {
	sb, _ := setupBenchmarkStringBank(CouplingModePhysical, notes, sustainDown)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sb.ProcessUndampedTargetCouplingOnly(128, nil)
	}
}

func setupBenchmarkStringBank(mode CouplingMode, notes []int, sustainDown bool) (*StringBank, *HammerExciter) {
	params := NewDefaultParams()
	params.ResonanceEnabled = false
	params.CouplingEnabled = true
	params.CouplingMode = mode
	params.CouplingAmount = 1.0
	params.CouplingMaxNeighbors = 10

	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)
	sb.SetSustain(sustainDown)
	for _, note := range notes {
		sb.SetKeyDown(note, true)
		h.Trigger(note, 110)
	}
	for i := 0; i < 64; i++ {
		_ = sb.Process(128, h)
	}
	return sb, h
}

func (sb *StringBank) ProcessUndampedTargetCouplingOnly(numFrames int, hammer *HammerExciter) []float32 {
	out := sb.ensureOutputBuffer(numFrames)
	if numFrames <= 0 {
		return out
	}
	if len(sb.activeNotes) == 0 {
		for i := 0; i < numFrames; i++ {
			if hammer != nil {
				hammer.ProcessSample(sb)
			}
			out[i] = 0
		}
		return out
	}

	for _, note := range sb.activeNotes {
		sb.blockEnergy[note] = 0
	}

	for i := 0; i < numFrames; i++ {
		if hammer != nil {
			hammer.ProcessSample(sb)
		}
		var mix float32
		for _, note := range sb.activeNotes {
			sb.sampleOut[note] = 0
			g := sb.groups[note]
			if g == nil || !g.active {
				continue
			}
			s := g.processSample(sb.unisonCrossfeed)
			sb.sampleOut[note] = s
			mix += s
			sf := float64(s)
			sb.blockEnergy[note] += sf * sf
		}
		if sb.couplingEnabled {
			sb.applySparseCouplingUndampedTargetsOnly()
		}
		out[i] = mix
	}

	next := sb.activeNotes[:0]
	for _, note := range sb.activeNotes {
		g := sb.groups[note]
		if g == nil {
			sb.active[note] = false
			continue
		}
		if g.endBlock(sb.blockEnergy[note], numFrames) {
			sb.active[note] = true
			next = append(next, note)
			continue
		}
		sb.active[note] = false
	}
	sb.activeNotes = next

	return out
}

func (sb *StringBank) applySparseCouplingUndampedTargetsOnly() {
	const eps = 1e-9
	polyScale := float32(1.0)
	if n := len(sb.activeNotes); n > 1 {
		polyScale = float32(1.0 / math.Sqrt(float64(n)))
	}
	for _, src := range sb.activeNotes {
		srcSample := sb.sampleOut[src]
		if srcSample > -eps && srcSample < eps {
			continue
		}
		edges := sb.coupling[src]
		for _, e := range edges {
			target := sb.groups[e.to]
			if target == nil || !target.isUndamped() {
				continue
			}
			sb.InjectCouplingForce(e.to, srcSample*e.gain*polyScale)
		}
	}
}
