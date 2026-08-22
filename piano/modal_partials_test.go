package piano

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

// This file is the quality half of PIANO-406: how far can `modal_partials` drop
// before quality suffers? The cost half is BenchmarkModalPartialsSweep in
// modal_bench_test.go, which sweeps the same partial counts
// (modalPartialSweepCounts) so the two halves tabulate the same points.
//
// # Two references, because one of them cannot answer the question
//
// Each partial count is scored twice.
//
// **Against DWG at the same note.** This is the cross-core reference PLAN.md
// 12.4 already uses (TestDWGModalDistanceIsBounded), and it is what keeps this
// sweep connected to the shipping-profile decision: the DWG profile is the
// high-accuracy reference, so "how far does modal drift from it" is a fair
// question to ask of a partial count. Its answer here is almost entirely
// negative, and that is a finding rather than a failure — see the note on
// saturation below.
//
// **Against the modal core at 32 partials**, the richest bank the parameter
// permits (Params.ModalPartials is valid in [1,32]). This one is added because
// the DWG reference cannot answer the question that was actually asked. DWG's
// own output is a sparse impulse train (see TestDWGModalDistanceIsBounded), so
// the cross-core distance is dominated by that defect and barely moves when the
// modal spectrum changes underneath it. The internal reference asks the
// question directly instead: how much does the rendered sound change when
// partials are taken away, holding everything else fixed? It is not an absolute
// quality measure either — nothing here says the 32-partial render is right —
// but it is a real, well-conditioned difference signal, and it is the column
// the PIANO-406 decision is read from.
//
// # Why the overall score is not the metric to read
//
// analysis.Compare's `score` under the default legacy-v1 profile saturates
// spectrally: at these distances the spectral component is far past
// analysis.NormSpectral = 30.0, clamp01 pins it at 1.0, and it contributes a
// constant with no gradient. The same caveat is written out at length in
// assets/thresholds/c4.json under "SPECTRAL CAVEAT". Against DWG the entire
// measured score range over a 24x change in partial count is 0.004 — the knee
// is simply not visible in it. The raw sub-metrics are reported and asserted on
// instead: partial_level_rmse_db, spectral_rmse_db and the per-band
// spectral_high_rmse_db.
//
// # A measurement ceiling that must be read with the numbers
//
// analysis tracks at most 16 harmonics (analysis/features.go, `maxPartials =
// 16`). partial_level_rmse_db is therefore blind to anything above the 16th
// partial: a 16-partial and a 32-partial render read as identical on it by
// construction, not because they sound the same. Every partial_level figure at
// 16 and above is at that ceiling and must not be read as "no difference".

// modalPartialRegisters are the three registers the sweep runs in — roughly C2,
// C4 and C6. They stay at or below MIDI 91 for the same reason the polyphony
// sweep does: the DWG core has a known defect from roughly MIDI 96 up (runaway
// DC, bit-exact silence at MIDI 106-108, see TestTrebleRegisterCollapsesInDWGCore
// in tuning_test.go), and a DWG-referenced comparison run through it would be
// measuring that defect.
var modalPartialRegisters = []struct {
	name string
	note int
}{
	{name: "C2", note: 36},
	{name: "C4", note: 60},
	{name: "C6", note: 84},
}

const (
	// modalPartialsBlocks matches TestDWGModalDistanceIsBounded (750 blocks of
	// 128 frames, 2.0 s at 48 kHz) so the DWG-referenced column of this sweep is
	// directly comparable with the numbers pinned there.
	modalPartialsBlocks = 750

	// modalPartialsMaxScore is the same 0.89 bound TestDWGModalDistanceIsBounded
	// uses. It is deliberately not a separate, looser bound: the shipped default
	// of 8 partials is exactly the configuration that test already measures, so
	// this sweep must not be able to pass a configuration that one would fail.
	modalPartialsMaxScore = 0.89

	// modalPartialsDefault is the shipped Params.ModalPartials default.
	modalPartialsDefault = 8

	// modalPartialsInternalRef is the partial count used as the internal
	// reference: the top of the valid [1,32] range.
	modalPartialsInternalRef = 32

	// modalPartialsMonotoneTolDB is how far a partial_level_rmse_db reading may
	// move in the wrong direction before the monotonicity assertion fails.
	// Two measured points need it, both small and both explained: C2 rises
	// 0.27 dB from 3 to 4 partials, and C4 rises 0.07 dB from 16 to 24, the
	// latter sitting on the 16-harmonic analysis ceiling described above. The
	// real steps this assertion has to catch are 3 to 27 dB, so 0.5 dB is a
	// tolerance for measurement grain, not a licence for a regression.
	modalPartialsMonotoneTolDB = 0.5

	// modalPartialsScoreMonotoneTol is the same idea for the DWG-referenced
	// score, and it needs stating precisely because the first draft of this
	// test asserted exact monotonicity and was wrong. The score IS
	// non-increasing in partial count to four decimals, but not to six: past
	// the point where it saturates it wobbles upward by at most 3.4e-05
	// (measured at C2, 6 to 8 partials). That wobble is two orders of magnitude
	// below the real signal — the whole span from 1 partial to 24 is 0.004 at
	// C2 — so the assertion allows it and would still catch any reversal worth
	// the name.
	modalPartialsScoreMonotoneTol = 1e-4
)

// modalPartialsRender renders one note through one core at a given partial
// count, with the same scenario TestDWGModalDistanceIsBounded uses: sustain
// held from before the strike, resonance and coupling disabled, so the only
// thing varying across the sweep is the string core itself.
func modalPartialsRender(model StringModel, note int, partials int) []float64 {
	cfg := defaultCoreRenderConfig()
	cfg.model = model
	cfg.notes = []int{note}
	cfg.blocks = modalPartialsBlocks
	cfg.sustainAt = 0
	cfg.modalPartials = partials
	return renderCoreMono(cfg)
}

// TestModalPartialsDefaultDistanceGuard is the narrow, always-on half of the
// suite guard, and it still runs under -short when the wide sweep below is
// skipped. It asserts the two properties that must hold whatever the sweep
// says: the shipped default is no worse against DWG than the bound
// TestDWGModalDistanceIsBounded already enforces, and cutting the partial count
// to the minimum does not somehow score better.
func TestModalPartialsDefaultDistanceGuard(t *testing.T) {
	const note = 60

	dwg := modalPartialsRender(StringModelDWG, note, 0)

	atDefault := analysis.Compare(dwg, modalPartialsRender(StringModelModal, note, modalPartialsDefault), 48000)
	atOne := analysis.Compare(dwg, modalPartialsRender(StringModelModal, note, 1), 48000)

	t.Logf("note %d vs DWG: partials=%d score=%.6f partial_level=%.2fdB | partials=1 score=%.6f partial_level=%.2fdB",
		note, modalPartialsDefault, atDefault.Score, atDefault.PartialLevelRMSEDB,
		atOne.Score, atOne.PartialLevelRMSEDB)

	// The explicit NaN check is not redundant: every comparison against NaN is
	// false, so a bare `Score > bound` would silently accept a non-finite
	// regression propagated through analysis.Compare's metrics and clamp01.
	if math.IsNaN(atDefault.Score) || atDefault.Score > modalPartialsMaxScore {
		t.Errorf("note %d at the default %d partials: score %.4f is not within bound %.2f",
			note, modalPartialsDefault, atDefault.Score, modalPartialsMaxScore)
	}
	if math.IsNaN(atOne.Score) || atOne.Score < atDefault.Score {
		t.Errorf("note %d: 1 partial scores %.6f, better than the default %d partials at %.6f; "+
			"fewer partials must never be closer to DWG",
			note, atOne.Score, modalPartialsDefault, atDefault.Score)
	}
}

// TestModalPartialsQualityCurve is the wide sweep. It emits the full table via
// t.Log so the BENCHMARKS.md transcription is reproducible, and asserts a bound
// and a direction rather than pinning a table of numbers.
//
// It is skipped under -short. At roughly 1.5 s it is not expensive enough to
// need gating in the normal suite, but -short exists so a fast run stays fast,
// and TestModalPartialsDefaultDistanceGuard keeps a guard in place there.
func TestModalPartialsQualityCurve(t *testing.T) {
	if testing.Short() {
		t.Skip("wide modal_partials sweep skipped in -short; TestModalPartialsDefaultDistanceGuard still guards the default")
	}

	for _, reg := range modalPartialRegisters {
		dwg := modalPartialsRender(StringModelDWG, reg.note, 0)
		internal := modalPartialsRender(StringModelModal, reg.note, modalPartialsInternalRef)

		var (
			prevPartialLevel = math.Inf(1)
			prevDWGScore     = math.Inf(1)
			sawDefault       bool
		)

		for _, partials := range modalPartialSweepCounts {
			modal := modalPartialsRender(StringModelModal, reg.note, partials)

			vsDWG := analysis.Compare(dwg, modal, 48000)
			vsRef := analysis.Compare(internal, modal, 48000)

			t.Logf("%s (note %d) partials=%2d | vs DWG: score=%.6f partial_level=%6.2fdB spectral=%6.2fdB spectral_high=%6.2fdB"+
				" | vs modal-%d: score=%.6f partial_level=%6.2fdB spectral=%5.2fdB spectral_high=%5.2fdB",
				reg.name, reg.note, partials,
				vsDWG.Score, vsDWG.PartialLevelRMSEDB, vsDWG.SpectralRMSEDB, vsDWG.SpectralHighRMSEDB,
				modalPartialsInternalRef,
				vsRef.Score, vsRef.PartialLevelRMSEDB, vsRef.SpectralRMSEDB, vsRef.SpectralHighRMSEDB)

			if math.IsNaN(vsDWG.Score) || math.IsNaN(vsRef.Score) {
				t.Fatalf("%s partials=%d: non-finite score (vs DWG %v, vs modal-%d %v)",
					reg.name, partials, vsDWG.Score, modalPartialsInternalRef, vsRef.Score)
			}
			// partial_level_rmse_db carries the only quality assertion in this
			// test, and analysis.Compare leaves it NaN when partial extraction
			// fails on either render. The default legacy-v1 score gives that
			// component zero weight, so the score checks above stay green in
			// that case, and every NaN comparison below is false — the
			// monotonicity check would pass silently for the rest of the sweep
			// exactly when the analyzer or the render has regressed. Fail here
			// instead, before the value is either compared or carried forward.
			if math.IsNaN(vsRef.PartialLevelRMSEDB) || math.IsInf(vsRef.PartialLevelRMSEDB, 0) {
				t.Fatalf("%s partials=%d: partial_level_rmse_db against modal-%d is %v, so the monotonicity "+
					"assertion below has nothing to assert; partial extraction failed on one of the two renders",
					reg.name, partials, modalPartialsInternalRef, vsRef.PartialLevelRMSEDB)
			}
			if vsDWG.AlignedFrames < modalPartialsBlocks*128/2 {
				t.Errorf("%s partials=%d: only %d frames aligned out of %d, comparison is not meaningful",
					reg.name, partials, vsDWG.AlignedFrames, modalPartialsBlocks*128)
			}

			if partials == modalPartialsDefault {
				sawDefault = true
				if vsDWG.Score > modalPartialsMaxScore {
					t.Errorf("%s at the default %d partials: score %.4f is not within bound %.2f",
						reg.name, partials, vsDWG.Score, modalPartialsMaxScore)
				}
			}

			// Direction, on the two axes that can carry it.
			//
			// partial_level_rmse_db against the internal reference is the one
			// with a real gradient — 3 to 46 dB across the sweep — so the
			// monotonicity assertion lives there: adding partials must not make
			// the render drift further from the full-partial bank.
			if vsRef.PartialLevelRMSEDB > prevPartialLevel+modalPartialsMonotoneTolDB {
				t.Errorf("%s partials=%d: partial_level_rmse_db against modal-%d rose to %.2f dB from %.2f dB "+
					"at the previous, lower partial count; more partials must not drift further from the full bank",
					reg.name, partials, modalPartialsInternalRef, vsRef.PartialLevelRMSEDB, prevPartialLevel)
			}
			prevPartialLevel = vsRef.PartialLevelRMSEDB

			// The DWG-referenced score is asserted only for direction, never
			// for magnitude: it saturates (see the file comment) and its whole
			// measured range across the sweep is about 0.004, against a
			// residual upward wobble of 3.4e-05 once saturated — hence
			// modalPartialsScoreMonotoneTol rather than an exact comparison.
			if vsDWG.Score > prevDWGScore+modalPartialsScoreMonotoneTol {
				t.Errorf("%s partials=%d: score against DWG rose to %.6f from %.6f at the previous, lower "+
					"partial count; fewer partials must never score better",
					reg.name, partials, vsDWG.Score, prevDWGScore)
			}
			prevDWGScore = vsDWG.Score
		}

		if !sawDefault {
			t.Errorf("%s: the sweep never visited the shipped default of %d partials, so nothing was bounded",
				reg.name, modalPartialsDefault)
		}
	}
}
