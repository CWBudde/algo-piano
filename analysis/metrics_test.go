package analysis

import (
	"encoding/json"
	"math"
	"testing"
)

func TestMetricsSanitizedReplacesNonFinite(t *testing.T) {
	m := Metrics{
		TimeRMSE:               math.NaN(),
		EnvelopeRMSEDB:         math.Inf(1),
		SpectralRMSEDB:         math.Inf(-1),
		RefDecayDBPerS:         math.NaN(),
		CandDecayDBPerS:        math.NaN(),
		DecayDiffDBPerS:        math.NaN(),
		SpectralLowRMSEDB:      math.NaN(),
		SpectralMidRMSEDB:      math.NaN(),
		SpectralHighRMSEDB:     math.NaN(),
		F0Hz:                   math.NaN(),
		PartialLevelRMSEDB:     math.NaN(),
		PartialFreqRMSECents:   math.NaN(),
		TristimulusDistance:    math.NaN(),
		RefRiseTimeMS:          math.NaN(),
		CandRiseTimeMS:         math.NaN(),
		AttackRiseDiffMS:       math.NaN(),
		RefAttackCentroidHz:    math.NaN(),
		CandAttackCentroidHz:   math.NaN(),
		AttackCentroidRMSEOct:  math.NaN(),
		DecaySegmentRMSEDBPerS: math.NaN(),
		TimeNorm:               math.NaN(),
		EnvelopeNorm:           math.NaN(),
		SpectralNorm:           math.NaN(),
		DecayNorm:              math.NaN(),
		PartialLevelNorm:       math.NaN(),
		PartialFreqNorm:        math.NaN(),
		TristimulusNorm:        math.NaN(),
		AttackNorm:             math.NaN(),
		DecaySegmentNorm:       math.NaN(),
		Score:                  math.NaN(),
		Similarity:             math.NaN(),
		SpectralPositions: []SpectralPosition{
			{OffsetSec: math.NaN(), RMSEDB: math.NaN()},
		},
	}

	s := m.Sanitized()

	if s.TimeRMSE != 1.0 {
		t.Fatalf("expected TimeRMSE 1.000000, got %f", s.TimeRMSE)
	}
	if s.EnvelopeRMSEDB != 60.0 {
		t.Fatalf("expected EnvelopeRMSEDB 60.000000, got %f", s.EnvelopeRMSEDB)
	}
	if s.SpectralRMSEDB != 60.0 {
		t.Fatalf("expected SpectralRMSEDB 60.000000, got %f", s.SpectralRMSEDB)
	}
	if s.RefDecayDBPerS != 0 {
		t.Fatalf("expected RefDecayDBPerS 0.000000, got %f", s.RefDecayDBPerS)
	}
	if s.CandDecayDBPerS != 0 {
		t.Fatalf("expected CandDecayDBPerS 0.000000, got %f", s.CandDecayDBPerS)
	}
	if s.DecayDiffDBPerS != 60.0 {
		t.Fatalf("expected DecayDiffDBPerS 60.000000, got %f", s.DecayDiffDBPerS)
	}
	if s.PartialFreqRMSECents != 1200.0 {
		t.Fatalf("expected PartialFreqRMSECents 1200.000000, got %f", s.PartialFreqRMSECents)
	}
	if s.TristimulusDistance != 1.0 {
		t.Fatalf("expected TristimulusDistance 1.000000, got %f", s.TristimulusDistance)
	}
	if s.DecaySegmentRMSEDBPerS != 0 {
		t.Fatalf("expected DecaySegmentRMSEDBPerS 0.000000, got %f", s.DecaySegmentRMSEDBPerS)
	}
	if s.Score != 1.0 {
		t.Fatalf("expected Score 1.000000, got %f", s.Score)
	}
	if s.Similarity != 0.0 {
		t.Fatalf("expected Similarity 0.000000, got %f", s.Similarity)
	}
	if s.TimeNorm != 1.0 || s.AttackNorm != 1.0 || s.DecaySegmentNorm != 1.0 {
		t.Fatalf("expected all norms 1.000000, got time=%f attack=%f decaySeg=%f", s.TimeNorm, s.AttackNorm, s.DecaySegmentNorm)
	}
	if s.SpectralPositions[0].RMSEDB != 60.0 {
		t.Fatalf("expected spectral position RMSEDB 60.000000, got %f", s.SpectralPositions[0].RMSEDB)
	}

	// The original must not be mutated: Sanitized has a value receiver and
	// copies the positions slice.
	if !math.IsNaN(m.TimeRMSE) {
		t.Fatalf("expected original TimeRMSE to stay NaN, got %f", m.TimeRMSE)
	}
	if !math.IsNaN(m.SpectralPositions[0].RMSEDB) {
		t.Fatalf("expected original spectral position to stay NaN, got %f", m.SpectralPositions[0].RMSEDB)
	}
}

func TestMetricsSanitizedMarshalsToJSON(t *testing.T) {
	m := Metrics{RefDecayDBPerS: math.NaN()}

	if _, err := json.Marshal(m); err == nil {
		t.Fatalf("expected raw Metrics with NaN to fail json.Marshal, got no error")
	}

	buf, err := json.Marshal(m.Sanitized())
	if err != nil {
		t.Fatalf("expected sanitized Metrics to marshal, got error: %v", err)
	}

	var back Metrics
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("expected sanitized JSON to round-trip, got error: %v", err)
	}
	if back.RefDecayDBPerS != 0 {
		t.Fatalf("expected round-tripped RefDecayDBPerS 0.000000, got %f", back.RefDecayDBPerS)
	}
}

func TestCompareShortSignalMarshalsAfterSanitize(t *testing.T) {
	sr := 48000
	x := makeDecaySine(sr, 440.0, 0.02, 0.7)
	m := Compare(x, x, sr)

	if !math.IsNaN(m.RefDecayDBPerS) {
		t.Fatalf("expected NaN decay slope for a 20 ms signal, got %f", m.RefDecayDBPerS)
	}
	if _, err := json.Marshal(m); err == nil {
		t.Fatalf("expected unsanitized Compare output to fail json.Marshal, got no error")
	}
	if _, err := json.Marshal(m.Sanitized()); err != nil {
		t.Fatalf("expected sanitized Compare output to marshal, got error: %v", err)
	}
}
