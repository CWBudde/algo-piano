package piano

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

// renderNoteMono renders one note through a full Piano with the given string
// core and returns the left channel as float64, which is what analysis.Compare
// consumes.
func renderNoteMono(model StringModel, note int, velocity int, blocks int) []float64 {
	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = false
	params.CouplingEnabled = false

	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)
	p.NoteOn(note, velocity)

	out := make([]float64, 0, blocks*128)
	for i := 0; i < blocks; i++ {
		block := p.Process(128)
		for j := 0; j < 128; j++ {
			out = append(out, float64(block[j*2]))
		}
	}
	return out
}

// TestDWGModalDistanceIsBounded is the DWG-vs-modal A/B required by PLAN.md
// 12.4. It renders the same note through both cores and bounds the objective
// distance from analysis.Compare, so a change that quietly pulls one core away
// from the other shows up as a number instead of a listening session.
//
// Re-measured on 2026-08-21 (Go 1.26.5, linux/amd64) after the DWG injection
// and loop DC fixes, 750 blocks of 128 frames at 48 kHz, sustain held,
// resonance and coupling disabled:
//
//	note 48: score 0.8421  similarity 3.45%  dominant=spectral
//	note 60: score 0.8386  similarity 3.49%  dominant=spectral
//	note 72: score 0.8292  similarity 3.63%  dominant=spectral
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
