package piano

import (
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
// Measured on 2026-08-21 (Go 1.26.5, linux/amd64), 750 blocks of 128 frames
// at 48 kHz, sustain held, resonance and coupling disabled:
//
//	note 48: score 0.8462  similarity 3.39%  dominant=spectral
//	note 60: score 0.8484  similarity 3.36%  dominant=spectral
//	note 72: score 0.7082  similarity 5.89%  dominant=spectral
//
// The bound is set to 0.90, about 6% of headroom over the worst measured score.
//
// Read those numbers as an alarm, not as an acceptance criterion: the two cores
// currently agree very poorly. The envelope term alone reaches 92-154 dB RMSE,
// which is what happens when one signal is momentarily silent while the other
// is not — the DWG core's raw bank output is a sparse impulse train riding on a
// large DC offset (see TestTrebleRegisterCollapsesInDWGCore), so whole analysis
// windows read as digital silence. Until that is fixed, a low score here is not
// achievable and the test can only catch further drift.
func TestDWGModalDistanceIsBounded(t *testing.T) {
	const blocks = 750
	const maxScore = 0.90

	for _, note := range []int{48, 60, 72} {
		dwg := renderNoteMono(StringModelDWG, note, 100, blocks)
		modal := renderNoteMono(StringModelModal, note, 100, blocks)

		m := analysis.Compare(dwg, modal, 48000)
		t.Logf("note %d: score=%.4f similarity=%.2f%% dominant=%s time=%.4f env=%.2fdB spec=%.2fdB decay=%.2fdB/s",
			note, m.Score, m.Similarity*100, m.Dominant,
			m.TimeRMSE, m.EnvelopeRMSEDB, m.SpectralRMSEDB, m.DecayDiffDBPerS)

		if m.Score > maxScore {
			t.Errorf("note %d: DWG/modal distance score %.4f exceeds bound %.2f", note, m.Score, maxScore)
		}
		if m.AlignedFrames < blocks*128/2 {
			t.Errorf("note %d: only %d frames aligned out of %d, comparison is not meaningful",
				note, m.AlignedFrames, blocks*128)
		}
	}
}
