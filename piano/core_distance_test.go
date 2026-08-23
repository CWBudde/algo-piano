package piano

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

// coreRenderConfig describes a full-engine render scenario used by the
// DWG-vs-modal comparison and parity tests in this file and in
// core_parity_test.go. It is the Piano-level twin of modalRenderConfig in
// modal_parity_test.go: that one drives a bare StringBank, this one drives a
// complete Piano so the comparison covers the output stage the listener hears.
//
// The three event fields are block indices, and -1 disables the event. A
// sustainAt of 0 is special-cased to press the pedal before the NoteOn calls,
// which is the ordering the original renderNoteMono used and which
// TestDWGModalDistanceIsBounded's pinned numbers depend on.
type coreRenderConfig struct {
	model        StringModel
	notes        []int
	velocity     int
	blocks       int
	coupling     bool
	couplingMode CouplingMode
	resonance    bool
	sustainAt    int // block index at which the sustain pedal goes down, -1 to skip
	keyUpAt      int // block index at which all keys are released, -1 to skip
	sustainOffAt int // block index at which the sustain pedal lifts, -1 to skip
	sampleRate   int

	// modalPartials overrides Params.ModalPartials for the modal core. Zero
	// means "leave the engine default alone", so every pre-existing caller —
	// and every number pinned against one — keeps rendering at the shipped
	// default of 8. Only modal_partials_test.go sets it.
	modalPartials int
}

// defaultCoreRenderConfig is the isolated single-string scenario: no resonance
// engine and no coupling, so the only difference between two renders is the
// string core itself.
func defaultCoreRenderConfig() coreRenderConfig {
	return coreRenderConfig{
		notes:        []int{60},
		velocity:     100,
		blocks:       64,
		coupling:     false,
		couplingMode: CouplingModeStatic,
		resonance:    false,
		sustainAt:    -1,
		keyUpAt:      -1,
		sustainOffAt: -1,
		sampleRate:   48000,
	}
}

// renderCoreMono renders cfg through a full Piano and returns the left channel
// as float64, which is what analysis.Compare consumes. Mono is enough: the
// stereo image is produced downstream of the string core and would only add a
// constant pan difference to every comparison.
func renderCoreMono(cfg coreRenderConfig) []float64 {
	params := NewDefaultParams()
	params.StringModel = cfg.model
	params.ResonanceEnabled = cfg.resonance
	params.CouplingEnabled = cfg.coupling
	if cfg.coupling {
		params.CouplingMode = cfg.couplingMode
	}
	// Zero keeps NewDefaultParams' ModalPartials (8). The DWG core ignores the
	// field entirely.
	if cfg.modalPartials > 0 {
		params.ModalPartials = cfg.modalPartials
	}

	p := NewPiano(cfg.sampleRate, 16, params)
	// sustainAt == 0 presses the pedal before the NoteOn calls, matching the
	// ordering the original renderNoteMono used; the loop below then presses
	// it again at block 0, which is a no-op on an idempotent setter.
	if cfg.sustainAt == 0 {
		p.SetSustainPedal(true)
	}
	for _, n := range cfg.notes {
		p.NoteOn(n, cfg.velocity)
	}

	out := make([]float64, 0, cfg.blocks*128)
	for i := 0; i < cfg.blocks; i++ {
		// Deliberately three independent conditionals rather than a
		// switch: keyUpAt and sustainOffAt routinely coincide (release
		// the keys and lift the pedal in the same block), and a switch
		// would silently execute only the first matching case.
		if i == cfg.sustainAt {
			p.SetSustainPedal(true)
		}
		if i == cfg.keyUpAt {
			for _, n := range cfg.notes {
				p.NoteOff(n)
			}
		}
		if i == cfg.sustainOffAt {
			p.SetSustainPedal(false)
		}
		block := p.Process(128)
		for j := 0; j < 128; j++ {
			out = append(out, float64(block[j*2]))
		}
	}
	return out
}

// renderNoteMono is the single-note wrapper the original distance test used.
// It is kept verbatim in behaviour so TestDWGModalDistanceIsBounded's pinned
// numbers stay comparable across this refactor.
func renderNoteMono(model StringModel, note int, velocity int, blocks int) []float64 {
	cfg := defaultCoreRenderConfig()
	cfg.model = model
	cfg.notes = []int{note}
	cfg.velocity = velocity
	cfg.blocks = blocks
	cfg.sustainAt = 0
	return renderCoreMono(cfg)
}

// TestDWGModalDistanceIsBounded is the DWG-vs-modal A/B required by PLAN.md
// 12.4. It renders the same note through both cores and bounds the objective
// distance from analysis.Compare, so a change that quietly pulls one core away
// from the other shows up as a number instead of a listening session.
//
// Re-measured on 2026-08-23 (Go 1.26.5, linux/amd64) after the modal unison
// crossfeed was made passive, 750 blocks of 128 frames at 48 kHz, sustain held,
// resonance and coupling disabled (the 2026-08-21 reading, taken after the DWG
// injection and loop DC fixes, is in brackets):
//
//	note 48: score 0.8425 [0.8421]  similarity 3.44%  dominant=spectral
//	note 60: score 0.8365 [0.8386]  similarity 3.52%  dominant=spectral
//	note 72: score 0.8260 [0.8292]  similarity 3.67%  dominant=spectral
//
// The modal side moved because the coupling term changed; the cores came very
// slightly closer, which is what a modal core that no longer pumps its own
// unison should do, and nowhere near enough to matter.
//
// The bound is tightened from 0.90 to 0.89, about 6% of headroom over the worst
// measured score.
//
// Read those numbers as an alarm, not as an acceptance criterion: the two cores
// still agree very poorly, and removing the DC offset barely moved the number.
// The envelope term alone still reaches 92-149 dB RMSE, which is what happens
// when one signal is momentarily silent while the other is not. The DC was only
// half of the earlier explanation, and it turned out to be the half that did not
// matter here: what makes whole analysis windows read as digital silence is that
// the DWG bank output is a sparse impulse train. That is not the delay-line
// headroom bug either - it is the excitation. ExciteAtPosition writes an
// antisymmetric ramp whose width is only 4-25% of the delay line, so most of the
// loop stays at exactly 0.0 and circulates as a pulse: a bare A0 string has 444
// non-zero samples out of 4000, measured 2026-08-21. Closing this gap needs a
// distributed excitation or a loop filter with real bandwidth, not a DC fix, so
// a low score here is still not achievable and the test can only catch drift.
func TestDWGModalDistanceIsBounded(t *testing.T) {
	const blocks = 750
	const maxScore = 0.89

	for _, note := range []int{48, 60, 72} {
		dwg := renderNoteMono(StringModelDWG, note, 100, blocks)
		modal := renderNoteMono(StringModelModal, note, 100, blocks)

		m := analysis.Compare(dwg, modal, 48000)
		t.Logf("note %d: score=%.4f similarity=%.2f%% dominant=%s time=%.4f env=%.2fdB spec=%.2fdB decay=%.2fdB/s",
			note, m.Score, m.Similarity*100, m.Dominant,
			m.TimeRMSE, m.EnvelopeRMSEDB, m.SpectralRMSEDB, m.DecayDiffDBPerS)

		// The NaN check is not redundant: every comparison against NaN is
		// false, so a bare `m.Score > maxScore` would silently accept a
		// non-finite regression propagated out of a string core through
		// analysis.Compare's metrics and clamp01. Anything not demonstrably
		// within the bound has to fail.
		if math.IsNaN(m.Score) || m.Score > maxScore {
			t.Errorf("note %d: DWG/modal distance score %.4f is not within bound %.2f", note, m.Score, maxScore)
		}
		if m.AlignedFrames < blocks*128/2 {
			t.Errorf("note %d: only %d frames aligned out of %d, comparison is not meaningful",
				note, m.AlignedFrames, blocks*128)
		}
	}
}

var coreDistanceChords = []struct {
	name  string
	notes []int
}{
	// C major, root position, spanning the register where both cores are known
	// to be well behaved.
	{name: "C3-major", notes: []int{48, 55, 60, 64}},
	// The same chord an octave up, so the comparison is not pinned to one
	// register. Both chords stay inside MIDI 48..84: the DWG core has a known
	// defect above roughly MIDI 96 (runaway DC, silent top notes, see
	// TestTrebleRegisterCollapsesInDWGCore in tuning_test.go), and letting that
	// into the render would measure the defect instead of the cores.
	{name: "C4-major", notes: []int{60, 67, 72, 76}},
}

// TestDWGModalDistanceIsBoundedOnChords is the polyphonic half of the PLAN.md
// 12.4 A/B. The single-note test above can only see how one string core renders
// one string; a chord additionally exercises voice summation, the shared output
// stage and whatever inter-voice interaction each core has, so a change that
// only shows up under polyphony has somewhere to fail.
//
// Re-measured on 2026-08-23 (Go 1.26.5, linux/amd64), 750 blocks of 128 frames
// at 48 kHz, sustain held from the first block, resonance and coupling disabled,
// after the modal unison crossfeed was made passive (2026-08-22 in brackets):
//
//	C3-major (48/55/60/64): score 0.8420 [0.8434]  similarity 3.45%  dominant=spectral
//	C4-major (60/67/72/76): score 0.8378 [0.8401]  similarity 3.50%  dominant=spectral
//
// The bound is 0.89, the same one the single-note test uses, which is about 5.5%
// of headroom over the worst measured chord score. Keeping the two tests on one
// bound is deliberate: the point of the pair is that polyphony must not make the
// two cores diverge *more* than a single note does, and a chord-specific bound
// would hide exactly that.
//
// The scores are as bad as the single-note ones for the same reason, and this
// test is no more an acceptance criterion than that one is: see the long note on
// TestDWGModalDistanceIsBounded above. The DWG bank emits a sparse impulse train
// because its excitation fills only 4-25% of the delay line, so whole analysis
// windows read as digital silence against a modal render that is never silent.
// A low score here is not achievable today; the test catches drift.
//
// Runtime trade-off: 750 blocks (2.0 s of audio) x 2 cores x 2 chords is about
// 0.3 s wall clock, so the render length matches the single-note test rather
// than being cut down. What is cut down is the chord count — two chords, not a
// sweep over inversions and velocities, since every extra chord costs a full
// pair of renders and buys very little given how coarse the measured contrast
// between the cores already is.
func TestDWGModalDistanceIsBoundedOnChords(t *testing.T) {
	const blocks = 750
	const maxScore = 0.89

	for _, chord := range coreDistanceChords {
		cfg := defaultCoreRenderConfig()
		cfg.notes = chord.notes
		cfg.blocks = blocks
		cfg.sustainAt = 0

		cfg.model = StringModelDWG
		dwg := renderCoreMono(cfg)
		cfg.model = StringModelModal
		modal := renderCoreMono(cfg)

		m := analysis.Compare(dwg, modal, 48000)
		t.Logf("chord %s %v: score=%.4f similarity=%.2f%% dominant=%s time=%.4f env=%.2fdB spec=%.2fdB decay=%.2fdB/s",
			chord.name, chord.notes, m.Score, m.Similarity*100, m.Dominant,
			m.TimeRMSE, m.EnvelopeRMSEDB, m.SpectralRMSEDB, m.DecayDiffDBPerS)

		// As in the single-note test, the explicit NaN check is not
		// redundant: every comparison against NaN is false, so a bare
		// `m.Score > maxScore` would accept a non-finite regression
		// propagated out of a string core.
		if math.IsNaN(m.Score) || m.Score > maxScore {
			t.Errorf("chord %s: DWG/modal distance score %.4f is not within bound %.2f", chord.name, m.Score, maxScore)
		}
		if m.AlignedFrames < blocks*128/2 {
			t.Errorf("chord %s: only %d frames aligned out of %d, comparison is not meaningful",
				chord.name, m.AlignedFrames, blocks*128)
		}
	}
}
