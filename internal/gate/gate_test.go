package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

func ptr(v float64) *float64 { return &v }

// TestMetricsByJSONTagCoversAllFloatFields is the guard that keeps the gate
// from silently rotting. Evaluate treats an unknown key as an error, so a
// float64 metric missing from MetricsByJSONTag would be un-gateable and would
// report as a typo instead.
func TestMetricsByJSONTagCoversAllFloatFields(t *testing.T) {
	got := MetricsByJSONTag(analysis.Metrics{})
	typ := reflect.TypeOf(analysis.Metrics{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.Float64 {
			continue
		}
		name := JSONTagName(f)
		if name == "" {
			continue
		}
		if _, ok := got[name]; !ok {
			t.Errorf("float64 field %s (json %q) missing from MetricsByJSONTag", f.Name, name)
		}
	}
}

func TestMetricsByJSONTagReadsValues(t *testing.T) {
	m := analysis.Metrics{SpectralRMSEDB: 58.18, Score: 0.5194}
	got := MetricsByJSONTag(m)
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

func TestEvaluateReportsBreach(t *testing.T) {
	spec := Spec{Max: map[string]*float64{
		"spectral_rmse_db": ptr(60.0),
		"score":            ptr(0.60),
	}}
	m := analysis.Metrics{SpectralRMSEDB: 61.2, Score: 0.30}

	breaches, worstUsed, worstMetric, err := Evaluate(spec, m)
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
}

func TestEvaluateIgnoresNullThreshold(t *testing.T) {
	// A null threshold parses to a nil pointer and must be neither enforced nor
	// counted, however bad the measured value is.
	var spec Spec
	if err := json.Unmarshal([]byte(`{"max":{"partial_freq_rmse_cents":null,"score":0.60}}`), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := spec.EnforcedCount(); got != 1 {
		t.Fatalf("EnforcedCount = %d, want 1", got)
	}
	m := analysis.Metrics{PartialFreqRMSECents: 9999, Score: 0.30}

	breaches, _, worstMetric, err := Evaluate(spec, m)
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

func TestEvaluateRejectsUnknownMetricKey(t *testing.T) {
	spec := Spec{Max: map[string]*float64{"spectral_rmse_dB": ptr(60.0)}}
	_, _, _, err := Evaluate(spec, analysis.Metrics{})
	if err == nil {
		t.Fatal("want an error for an unknown metric key")
	}
	if !strings.Contains(err.Error(), "spectral_rmse_dB") {
		t.Errorf("error %q should name the offending key", err)
	}
}

func TestEvaluateRejectsUnknownKeyEvenWhenNull(t *testing.T) {
	// A typo must be caught whether or not it carries a value; otherwise a
	// null-ed typo would look like a legitimately uncalibrated metric.
	spec := Spec{Max: map[string]*float64{"attack_available": nil}}
	if _, _, _, err := Evaluate(spec, analysis.Metrics{}); err == nil {
		t.Fatal("want an error: attack_available is a bool, not a gateable float64")
	}
}

func TestEvaluatePassesWithHeadroom(t *testing.T) {
	spec := Spec{Max: map[string]*float64{
		"spectral_rmse_db": ptr(60.0),
		"time_rmse":        ptr(0.5),
	}}
	m := analysis.Metrics{SpectralRMSEDB: 58.18, TimeRMSE: 0.11}

	breaches, worstUsed, worstMetric, err := Evaluate(spec, m)
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

func TestLoadSpecErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := LoadSpec(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Fatal("want an error for a missing threshold file")
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadSpec(path); err == nil {
			t.Fatal("want an error for a malformed threshold file")
		}
	})
}

func TestTrackedC4ThresholdFileIsValid(t *testing.T) {
	// The tracked gate file must parse, name only real metrics, and enforce the
	// five legacy metrics. A broken file would otherwise only surface when
	// someone happens to have reference/c4.wav.
	spec, err := LoadSpec("../../assets/thresholds/c4.json")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if _, _, _, err := Evaluate(spec, analysis.Metrics{}); err != nil {
		t.Fatalf("tracked thresholds name an unknown metric: %v", err)
	}
	if got := spec.EnforcedCount(); got != 5 {
		t.Errorf("EnforcedCount = %d, want 5", got)
	}
	for _, name := range []string{"score", "time_rmse", "envelope_rmse_db", "spectral_rmse_db", "decay_diff_db_per_s"} {
		if v, ok := spec.Max[name]; !ok || v == nil {
			t.Errorf("%s should be enforced", name)
		}
	}
}
