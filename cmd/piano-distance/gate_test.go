package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

func ptr(v float64) *float64 { return &v }

// TestMetricsByJSONTagCoversAllFloatFields is the guard that keeps the gate
// from silently rotting. evaluateGate treats an unknown key as an error, so a
// float64 metric missing from metricsByJSONTag would be un-gateable and would
// report as a typo instead.
func TestMetricsByJSONTagCoversAllFloatFields(t *testing.T) {
	got := metricsByJSONTag(analysis.Metrics{})
	typ := reflect.TypeOf(analysis.Metrics{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.Float64 {
			continue
		}
		name := jsonTagName(f)
		if name == "" {
			continue
		}
		if _, ok := got[name]; !ok {
			t.Errorf("float64 field %s (json %q) missing from metricsByJSONTag", f.Name, name)
		}
	}
}

func TestMetricsByJSONTagReadsValues(t *testing.T) {
	m := analysis.Metrics{SpectralRMSEDB: 58.18, Score: 0.5194}
	got := metricsByJSONTag(m)
	if got["spectral_rmse_db"] != 58.18 {
		t.Errorf("spectral_rmse_db = %v, want 58.18", got["spectral_rmse_db"])
	}
	if got["score"] != 0.5194 {
		t.Errorf("score = %v, want 0.5194", got["score"])
	}
	if _, ok := got["sample_rate"]; ok {
		t.Error("sample_rate is an int field and must not appear")
	}
	if _, ok := got["attack_available"]; ok {
		t.Error("attack_available is a bool field and must not appear")
	}
}

func TestEvaluateGateReportsBreach(t *testing.T) {
	spec := gateSpec{Max: map[string]*float64{
		"spectral_rmse_db": ptr(60.0),
		"score":            ptr(0.60),
	}}
	m := analysis.Metrics{SpectralRMSEDB: 61.2, Score: 0.30}

	breaches, worstUsed, worstMetric, err := evaluateGate(spec, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(breaches) != 1 {
		t.Fatalf("breaches = %v, want exactly 1", breaches)
	}
	if breaches[0].Metric != "spectral_rmse_db" || breaches[0].Got != 61.2 || breaches[0].Max != 60.0 {
		t.Errorf("breach = %+v, want spectral_rmse_db 61.2 > 60", breaches[0])
	}
	if worstMetric != "spectral_rmse_db" {
		t.Errorf("worstMetric = %q, want spectral_rmse_db", worstMetric)
	}
	if worstUsed <= 1.0 {
		t.Errorf("worstUsedFraction = %v, want > 1", worstUsed)
	}
	if line := formatBreach(breaches[0]); line != "gate: FAIL spectral_rmse_db=61.20 > max 60.00 (+2.0%)" {
		t.Errorf("formatBreach = %q", line)
	}
}

func TestEvaluateGateIgnoresNullThreshold(t *testing.T) {
	// A null threshold parses to a nil pointer and must be neither enforced nor
	// counted, however bad the measured value is.
	var spec gateSpec
	if err := json.Unmarshal([]byte(`{"max":{"partial_freq_rmse_cents":null,"score":0.60}}`), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := spec.enforcedCount(); got != 1 {
		t.Fatalf("enforcedCount = %d, want 1", got)
	}
	m := analysis.Metrics{PartialFreqRMSECents: 9999, Score: 0.30}

	breaches, _, worstMetric, err := evaluateGate(spec, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(breaches) != 0 {
		t.Fatalf("breaches = %v, want none", breaches)
	}
	if worstMetric != "score" {
		t.Errorf("worstMetric = %q, want score (the only enforced metric)", worstMetric)
	}
}

func TestEvaluateGateRejectsUnknownMetricKey(t *testing.T) {
	spec := gateSpec{Max: map[string]*float64{"spectral_rmse_dB": ptr(60.0)}}
	_, _, _, err := evaluateGate(spec, analysis.Metrics{})
	if err == nil {
		t.Fatal("want an error for an unknown metric key")
	}
	if !strings.Contains(err.Error(), "spectral_rmse_dB") {
		t.Errorf("error %q should name the offending key", err)
	}
}

func TestEvaluateGateRejectsUnknownKeyEvenWhenNull(t *testing.T) {
	// A typo must be caught whether or not it carries a value; otherwise a
	// null-ed typo would look like a legitimately uncalibrated metric.
	spec := gateSpec{Max: map[string]*float64{"attack_available": nil}}
	if _, _, _, err := evaluateGate(spec, analysis.Metrics{}); err == nil {
		t.Fatal("want an error: attack_available is a bool, not a gateable float64")
	}
}

func TestEvaluateGatePassesWithHeadroom(t *testing.T) {
	spec := gateSpec{Max: map[string]*float64{
		"spectral_rmse_db": ptr(60.0),
		"time_rmse":        ptr(0.5),
	}}
	m := analysis.Metrics{SpectralRMSEDB: 58.18, TimeRMSE: 0.11}

	breaches, worstUsed, worstMetric, err := evaluateGate(spec, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(breaches) != 0 {
		t.Fatalf("breaches = %v, want none", breaches)
	}
	if worstMetric != "spectral_rmse_db" {
		t.Errorf("worstMetric = %q, want spectral_rmse_db", worstMetric)
	}
	if worstUsed < 0.96 || worstUsed > 0.98 {
		t.Errorf("worstUsedFraction = %v, want about 0.97", worstUsed)
	}
}

func TestTrackedC4ThresholdFileIsValid(t *testing.T) {
	// The tracked gate file must parse, name only real metrics, and enforce the
	// five legacy metrics. A broken file would otherwise only surface when
	// someone happens to have reference/c4.wav.
	raw := mustReadFile(t, "../../assets/thresholds/c4.json")
	var spec gateSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, _, _, err := evaluateGate(spec, analysis.Metrics{}); err != nil {
		t.Fatalf("tracked thresholds name an unknown metric: %v", err)
	}
	if got := spec.enforcedCount(); got != 5 {
		t.Errorf("enforcedCount = %d, want 5", got)
	}
	for _, name := range []string{"score", "time_rmse", "envelope_rmse_db", "spectral_rmse_db", "decay_diff_db_per_s"} {
		if v, ok := spec.Max[name]; !ok || v == nil {
			t.Errorf("%s should be enforced", name)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}
