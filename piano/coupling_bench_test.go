package piano

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

func BenchmarkStringBankCouplingModes(b *testing.B) {
	cases := benchmarkCases()

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
	cases := benchmarkCases()

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

type couplingBenchCase struct {
	name        string
	sustainDown bool
	notes       []int
}

func benchmarkCases() []couplingBenchCase {
	if envCases := benchmarkCasesFromEnv(); len(envCases) > 0 {
		return envCases
	}
	low8 := []int{21, 24, 27, 30, 33, 36, 39, 42}   // mostly 1-string notes
	mid8 := []int{40, 44, 48, 52, 56, 60, 64, 68}   // 2-string notes
	high8 := []int{72, 76, 80, 84, 88, 92, 96, 100} // 3-string notes
	mixed8 := []int{48, 52, 55, 60, 64, 67, 72, 76} // cross-register
	singleMid := []int{60}                          // 2-string single key
	return []couplingBenchCase{
		newDefaultBenchCase("poly1_singleMid", false, singleMid),
		newDefaultBenchCase("poly1_singleMid", true, singleMid),
		newDefaultBenchCase("poly8_low", false, low8),
		newDefaultBenchCase("poly8_low", true, low8),
		newDefaultBenchCase("poly8_mid", false, mid8),
		newDefaultBenchCase("poly8_mid", true, mid8),
		newDefaultBenchCase("poly8_high", false, high8),
		newDefaultBenchCase("poly8_high", true, high8),
		newDefaultBenchCase("poly8_mixed", false, mixed8),
		newDefaultBenchCase("poly8_mixed", true, mixed8),
	}
}

func newDefaultBenchCase(prefix string, sustainDown bool, notes []int) couplingBenchCase {
	pedal := "pedalUp"
	if sustainDown {
		pedal = "pedalDown"
	}
	keys := len(notes)
	strings := stringCountForNotes(notes)
	return couplingBenchCase{
		name:        fmt.Sprintf("%s_%s_keys%d_strings%d", prefix, pedal, keys, strings),
		sustainDown: sustainDown,
		notes:       notes,
	}
}

func benchmarkCasesFromEnv() []couplingBenchCase {
	start, okStart := lookupEnvInt("PIANO_BENCH_KEY_START")
	end, okEnd := lookupEnvInt("PIANO_BENCH_KEY_END")
	if !okStart || !okEnd {
		return nil
	}
	step, okStep := lookupEnvInt("PIANO_BENCH_KEY_STEP")
	if !okStep || step <= 0 {
		step = 1
	}
	if start > end {
		start, end = end, start
	}
	if start < 0 {
		start = 0
	}
	if end > 127 {
		end = 127
	}
	notes := make([]int, 0, 128)
	for n := start; n <= end; n += step {
		notes = append(notes, n)
	}
	if len(notes) == 0 {
		return nil
	}
	keys := len(notes)
	strings := stringCountForNotes(notes)
	return []couplingBenchCase{
		{
			name:        fmt.Sprintf("range_%d_%d_step%d_pedalUp_keys%d_strings%d", start, end, step, keys, strings),
			sustainDown: false,
			notes:       notes,
		},
		{
			name:        fmt.Sprintf("range_%d_%d_step%d_pedalDown_keys%d_strings%d", start, end, step, keys, strings),
			sustainDown: true,
			notes:       notes,
		},
	}
}

func lookupEnvInt(name string) (int, bool) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func stringCountForNotes(notes []int) int {
	total := 0
	for _, note := range notes {
		detunes, _ := defaultUnisonForNote(note)
		total += len(detunes)
	}
	return total
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
