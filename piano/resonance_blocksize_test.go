package piano

import (
	"math"
	"testing"
)

// resonanceBlockSizes are the caller block sizes the invariance tests compare
// against a 128-frame reference. 128 is the render quantum every tool and the
// AudioWorklet use; 333 is deliberately not a divisor of anything.
var resonanceBlockSizes = []int{1, 16, 64, 333}

// blockSizeProbeParams builds the configuration both invariance tests share: the
// sympathetic loop turned up until it dominates the signal, and every
// block-rate mechanism that is NOT the loop switched off.
//
// Coupling has to go: applySparseCouplingBlockwise divides its drive by
// numFrames, so it is block-size dependent by construction and would mask what
// is being measured here.
func blockSizeProbeParams(model StringModel) *Params {
	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = true
	params.ResonancePerNoteFilter = true
	// Well above the 0.00018 default and the 0.00025 every shipped preset pins,
	// but under the ~0.0009 the loop is stable to. The point is to make the loop
	// a large term in the output rather than to render a plausible piano: at the
	// default gain a mismatch could hide under the struck note's round-off.
	params.ResonanceGain = 0.0005
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff
	return params
}

// renderResonanceTail plays a note with the pedal held, runs a fixed 128-frame
// prologue, and then renders the tail in blocks of blockSize.
//
// The prologue is identical for every block size on purpose. syncResonatingNotes
// enrolls resonating groups once per block, so during the first block after a
// strike a 1-frame caller enrolls the 87 sympathetic targets at sample 1 where a
// 128-frame caller enrolls them at sample 128 — a genuine difference, and one
// that has nothing to do with whether the injection itself is interleaved. After
// the prologue, with the pedal down, every group is undamped, endBlock returns
// true unconditionally, and the active set is frozen and identical.
func renderResonanceTail(model StringModel, blockSize int, tailFrames int) []float32 {
	const (
		sampleRate    = 48000
		prologueBlock = 128
		prologueCount = 8
	)
	params := blockSizeProbeParams(model)
	state := NewRingingState(sampleRate, params)
	hammer := NewHammerExciter(sampleRate, params)
	state.SetResonanceEngine(NewResonanceEngine(sampleRate, params.ResonanceGain, params.ResonancePerNoteFilter))

	state.SetSustainAmount(1)
	state.SetKeyDown(60, true)
	hammer.Trigger(60, 110)
	for range prologueCount {
		_ = state.Process(prologueBlock, hammer)
	}

	tail := make([]float32, 0, tailFrames)
	for len(tail) < tailFrames {
		n := blockSize
		if remaining := tailFrames - len(tail); n > remaining {
			n = remaining
		}
		tail = append(tail, state.Process(n, hammer)...)
	}
	return tail
}

// TestResonanceLoopIsBlockSizeInvariant is the assertion that the sympathetic
// loop really is interleaved with rendering.
//
// Every element of the loop is a per-sample recursion — the DC blocker and the
// 3.2 kHz pole in ResonanceEngine.bandLimit, the per-note noteResonator bank,
// the delay lines and the modal rotation — and StringBank.prevMix carries the
// one-sample loop delay across the call boundary. So with coupling off the
// rendered tail cannot depend on how the caller happens to slice its Process
// calls, and this compares float32 samples for exact equality.
//
// It fails immediately on the pre-2026-08-22 code, where the loop was closed
// once per block: a 1-frame caller then deposited one sample of force per closed
// loop and a 128-frame caller deposited a 128-sample coherent sum, so the two
// are not remotely the same signal.
//
// The one element that is not invariant by construction is
// flushDenormalModes, which runs at endBlock and therefore at different sample
// indices. It zeroes modes below modalFlushThreshold (1e-25); with ~1500 modes
// the worst case it can move a sample by is ~1.5e-22, against sustained
// magnitudes of order 1e-2. That ratio is ~1e-20, twelve orders below float32
// eps, so it cannot change the rounded value of a nonzero sum — but it is an
// argument, not a guarantee. If this ever flakes on a modal row, the honest
// weakening is `delta == 0 || |delta| < 1e-20`, never a percentage.
func TestResonanceLoopIsBlockSizeInvariant(t *testing.T) {
	const tailFrames = 4096

	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			reference := renderResonanceTail(model, 128, tailFrames)
			assertTailIsAudible(t, reference)

			for _, blockSize := range resonanceBlockSizes {
				candidate := renderResonanceTail(model, blockSize, tailFrames)
				if len(candidate) != len(reference) {
					t.Fatalf("block %d: rendered %d frames, want %d", blockSize, len(candidate), len(reference))
				}
				for i := range reference {
					if candidate[i] != reference[i] {
						t.Fatalf("block %d: sample %d is %v, want %v (delta %v) - the resonance loop is not block-size invariant",
							blockSize, i, candidate[i], reference[i], float64(candidate[i])-float64(reference[i]))
					}
				}
			}
		})
	}
}

// TestResonanceLoopIsBlockSizeInvariantWithoutEngine is the negative control.
//
// A bank with no engine attached has no resonance loop at all, so this
// comparison passes both before and after the loop moved inside StringBank.
// That is what makes a failure of the test above attributable to the resonance
// path and to nothing else in the render loop.
func TestResonanceLoopIsBlockSizeInvariantWithoutEngine(t *testing.T) {
	const (
		sampleRate = 48000
		tailFrames = 4096
	)

	render := func(model StringModel, blockSize int) []float32 {
		params := blockSizeProbeParams(model)
		state := NewRingingState(sampleRate, params)
		hammer := NewHammerExciter(sampleRate, params)
		// Deliberately NO SetResonanceEngine.
		state.SetSustainAmount(1)
		state.SetKeyDown(60, true)
		hammer.Trigger(60, 110)
		for range 8 {
			_ = state.Process(128, hammer)
		}
		out := make([]float32, 0, tailFrames)
		for len(out) < tailFrames {
			n := blockSize
			if remaining := tailFrames - len(out); n > remaining {
				n = remaining
			}
			out = append(out, state.Process(n, hammer)...)
		}
		return out
	}

	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			reference := render(model, 128)
			assertTailIsAudible(t, reference)
			for _, blockSize := range resonanceBlockSizes {
				candidate := render(model, blockSize)
				for i := range reference {
					if candidate[i] != reference[i] {
						t.Fatalf("block %d: sample %d is %v, want %v", blockSize, i, candidate[i], reference[i])
					}
				}
			}
		})
	}
}

// TestPianoResonanceRenderIsBlockSizeStable is the same comparison through the
// public Piano.Process, where the convolvers sit in the path.
//
// Bit-identity is NOT available here and the test must not ask for it:
// convolver_stream.go documents that a block which is not a whole partition goes
// through a head/tail split rather than the single-FFT path and differs from it
// by float32 round-off, order 1e-6 relative. numFrames = 128 equals partSize, so
// the reference takes the aligned path and none of the candidates do. What this
// pins is that the difference stays round-off — that the loop itself contributes
// nothing block-size dependent on top of it.
func TestPianoResonanceRenderIsBlockSizeStable(t *testing.T) {
	const (
		sampleRate = 48000
		tailFrames = 4096
		// Two convolution stages of ~1e-6 relative round-off, compounded.
		tolerance = 1e-5
	)

	render := func(model StringModel, blockSize int) []float32 {
		params := blockSizeProbeParams(model)
		p := NewPiano(sampleRate, 64, params)
		p.SetSustainPedal(true)
		p.NoteOn(60, 110)
		for range 8 {
			_ = p.Process(128)
		}
		out := make([]float32, 0, tailFrames*2)
		for len(out) < tailFrames*2 {
			n := blockSize
			if remaining := (tailFrames*2 - len(out)) / 2; n > remaining {
				n = remaining
			}
			out = append(out, p.Process(n)...)
		}
		return out
	}

	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			reference := render(model, 128)
			peak := 0.0
			for _, v := range reference {
				if a := math.Abs(float64(v)); a > peak {
					peak = a
				}
			}
			if peak == 0 {
				t.Fatalf("reference render is silent; the comparison would be vacuous")
			}
			limit := tolerance * peak

			for _, blockSize := range resonanceBlockSizes {
				candidate := render(model, blockSize)
				worst, worstAt := 0.0, -1
				for i := range reference {
					if d := math.Abs(float64(candidate[i]) - float64(reference[i])); d > worst {
						worst, worstAt = d, i
					}
				}
				t.Logf("block %d: max |delta| %.3e at sample %d (peak %.3e, limit %.3e)", blockSize, worst, worstAt, peak, limit)
				if worst > limit {
					t.Fatalf("block %d: max |delta| %.3e at sample %d exceeds %.3e (%.1e x peak %.3e)",
						blockSize, worst, worstAt, limit, tolerance, peak)
				}
			}
		})
	}
}

// assertTailIsAudible guards against a comparison that would pass on two silent
// buffers.
func assertTailIsAudible(t *testing.T, tail []float32) {
	t.Helper()
	for _, v := range tail {
		if v != 0 {
			return
		}
	}
	t.Fatalf("rendered tail is digital silence; the comparison would be vacuous")
}

// TestSetStringModelKeepsResonanceWired guards the wiring risk this change
// introduced.
//
// SetStringModel rebuilds RingingState wholesale, and a fresh StringBank starts
// with no engine attached. Without the attachResonance call after the rebuild
// the sympathetic loop would silently go away on a core switch — audibly, but
// with no error anywhere.
func TestSetStringModelKeepsResonanceWired(t *testing.T) {
	const sampleRate = 48000

	params := blockSizeProbeParams(StringModelDWG)
	p := NewPiano(sampleRate, 64, params)
	p.SetSustainPedal(true)
	p.NoteOn(60, 110)
	for range 8 {
		_ = p.Process(128)
	}

	if !p.SetStringModel(StringModelModal) {
		t.Fatalf("SetStringModel(modal) refused")
	}
	if p.ringing == nil || p.ringing.bank == nil {
		t.Fatalf("ringing state missing after core switch")
	}
	if p.ringing.bank.resonance != p.resonance {
		t.Fatalf("resonance engine not re-attached after core switch: bank has %p, piano has %p",
			p.ringing.bank.resonance, p.resonance)
	}

	// And it actually drives: hold a silent note, strike another, and check the
	// silent one picks up energy through the loop alone.
	p.KeyDown(72)
	p.NoteOn(60, 110)
	for range 64 {
		_ = p.Process(128)
	}
	if energy := p.ringing.bank.blockEnergy[72]; energy <= 0 {
		t.Fatalf("silent held note gained no sympathetic energy after core switch: %e", energy)
	}
}
