package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

func TestSanitizeMetricsReplacesNonFiniteValues(t *testing.T) {
	in := analysis.Metrics{
		TimeRMSE:        math.NaN(),
		EnvelopeRMSEDB:  math.Inf(1),
		SpectralRMSEDB:  math.Inf(-1),
		RefDecayDBPerS:  math.NaN(),
		CandDecayDBPerS: math.NaN(),
		DecayDiffDBPerS: math.NaN(),
		Score:           math.NaN(),
		Similarity:      math.NaN(),
	}
	out := sanitizeMetrics(in)
	if !isFiniteFloat(out.TimeRMSE) ||
		!isFiniteFloat(out.EnvelopeRMSEDB) ||
		!isFiniteFloat(out.SpectralRMSEDB) ||
		!isFiniteFloat(out.DecayDiffDBPerS) ||
		!isFiniteFloat(out.Score) ||
		!isFiniteFloat(out.Similarity) {
		t.Fatalf("expected sanitized finite metrics: %+v", out)
	}
	if out.Score < 0 || out.Score > 1 {
		t.Fatalf("expected score in [0,1], got=%f", out.Score)
	}
	if out.Similarity < 0 || out.Similarity > 1 {
		t.Fatalf("expected similarity in [0,1], got=%f", out.Similarity)
	}
}

func TestWeightedScoreHandlesNaN(t *testing.T) {
	score := weightedScore(
		[]analysis.Metrics{
			{Score: math.NaN()},
			{Score: 0.2},
		},
		[]float64{0.5, 0.5},
	)
	if !isFiniteFloat(score) {
		t.Fatalf("expected finite weighted score")
	}
	if score < 0 || score > 1 {
		t.Fatalf("expected weighted score in [0,1], got=%f", score)
	}
}

func TestKnobsNormalizedRoundTrip(t *testing.T) {
	in := knobSet{
		ModalPartials:     9,
		ModalGainExponent: 1.25,
		ModalExcitation:   1.8,
		ModalUndampedLoss: 0.9,
		ModalDampedLoss:   1.4,
	}
	pos := knobsToNormalized(in)
	if len(pos) != modalKnobDims {
		t.Fatalf("expected %d dims, got %d", modalKnobDims, len(pos))
	}
	for i, v := range pos {
		if v < 0 || v > 1 {
			t.Fatalf("pos[%d] out of [0,1]: %f", i, v)
		}
	}
	out := knobsFromNormalized(pos)
	if out.ModalPartials != in.ModalPartials {
		t.Fatalf("partials round-trip mismatch: got=%d want=%d", out.ModalPartials, in.ModalPartials)
	}
	if math.Abs(out.ModalGainExponent-in.ModalGainExponent) > 1e-6 {
		t.Fatalf("gain exponent round-trip mismatch: got=%f want=%f", out.ModalGainExponent, in.ModalGainExponent)
	}
	if math.Abs(out.ModalExcitation-in.ModalExcitation) > 1e-6 {
		t.Fatalf("excitation round-trip mismatch: got=%f want=%f", out.ModalExcitation, in.ModalExcitation)
	}
	if math.Abs(out.ModalUndampedLoss-in.ModalUndampedLoss) > 1e-6 {
		t.Fatalf("undamped loss round-trip mismatch: got=%f want=%f", out.ModalUndampedLoss, in.ModalUndampedLoss)
	}
	if math.Abs(out.ModalDampedLoss-in.ModalDampedLoss) > 1e-6 {
		t.Fatalf("damped loss round-trip mismatch: got=%f want=%f", out.ModalDampedLoss, in.ModalDampedLoss)
	}
}

func TestNewMayflyConfigVariantValidation(t *testing.T) {
	if _, err := newMayflyConfig("invalid", 8, modalKnobDims, 5); err == nil {
		t.Fatalf("expected error for invalid variant")
	}
	cfg, err := newMayflyConfig("desma", 8, modalKnobDims, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ProblemSize != modalKnobDims {
		t.Fatalf("problem size mismatch: got=%d want=%d", cfg.ProblemSize, modalKnobDims)
	}
	if cfg.NPop != 8 || cfg.NPopF != 8 {
		t.Fatalf("population mismatch: male=%d female=%d", cfg.NPop, cfg.NPopF)
	}
}

// --no-resonance is a calibration-speed knob, not a model change. A preset
// fitted with resonance silenced must still be written with the input preset's
// own resonance_enabled, otherwise a staged pipeline would hand every later
// stage a preset with sympathetic resonance permanently switched off.
func TestFinalizeOutputParamsRestoresPresetResonance(t *testing.T) {
	for _, presetResonance := range []bool{true, false} {
		// The fitting copy is what --no-resonance silences.
		base := piano.NewDefaultParams()
		base.ResonanceEnabled = false

		out := finalizeOutputParams(base, knobSet{ModalPartials: 12}, presetResonance)
		if out.ResonanceEnabled != presetResonance {
			t.Fatalf("ResonanceEnabled: got %v want %v", out.ResonanceEnabled, presetResonance)
		}
		if out.StringModel != piano.StringModelModal {
			t.Fatalf("StringModel: got %v want modal", out.StringModel)
		}
		// The restore must not write back through the fitting params.
		if base.ResonanceEnabled {
			t.Fatalf("finalizeOutputParams mutated the fitting params")
		}
	}
}

// The restored value has to survive the write, so pin the whole round-trip:
// the JSON must state resonance_enabled explicitly (no omitempty dropping the
// false) and reload to the same value.
func TestWritePresetResonanceRoundTrip(t *testing.T) {
	for _, presetResonance := range []bool{true, false} {
		base := piano.NewDefaultParams()
		base.ResonanceEnabled = false
		out := finalizeOutputParams(base, knobSet{ModalPartials: 12}, presetResonance)

		path := filepath.Join(t.TempDir(), "modal-calibrated.json")
		if err := writePreset(path, out); err != nil {
			t.Fatalf("writePreset: %v", err)
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, ok := fields["resonance_enabled"]; !ok {
			t.Fatalf("resonance_enabled missing from preset written with %v:\n%s", presetResonance, raw)
		}

		loaded, err := preset.LoadJSON(path)
		if err != nil {
			t.Fatalf("LoadJSON: %v", err)
		}
		if loaded.ResonanceEnabled != presetResonance {
			t.Fatalf("resonance_enabled round-trip: got %v want %v", loaded.ResonanceEnabled, presetResonance)
		}
	}
}
