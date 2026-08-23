package bodyiraudit

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

const testSampleRate = 8000

func TestAnalyzeIdentityControlIsNeutralAndDeterministic(t *testing.T) {
	signal := testSignal(testSampleRate)
	cfg := Config{SampleRate: testSampleRate, MIDINote: 60, ReleaseAfter: 0.5, Source: "identity-control"}

	first, err := Analyze(signal, signal, signal, []float64{1}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Analyze(signal, signal, signal, []float64{1}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("repeated reports differ")
	}
	if first.Attribution.Classification != "neutral" {
		t.Fatalf("classification = %q, want neutral", first.Attribution.Classification)
	}
	if first.Delta != (MetricDelta{}) {
		t.Fatalf("identity delta = %+v, want zeros", first.Delta)
	}
	one, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) {
		t.Fatal("JSON report is not deterministic")
	}
}

func TestAnalyzeScalarImpulseAttributesLevelCompensation(t *testing.T) {
	identity := testSignal(testSampleRate)
	selected := scale(identity, 2)
	reference := append([]float64(nil), selected...)

	report, err := Analyze(reference, identity, selected, []float64{2}, Config{
		SampleRate: testSampleRate, MIDINote: 60, ReleaseAfter: 0.5, Source: "scalar",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Attribution.Classification != "level_compensation" {
		t.Fatalf("classification = %q, want level_compensation (delta %+v)", report.Attribution.Classification, report.Delta)
	}
	if math.Abs(report.Delta.RMSDB-6.020599913) > 1e-6 {
		t.Fatalf("RMS delta = %.9f dB, want +6.0206 dB", report.Delta.RMSDB)
	}
	if math.Abs(report.IR.BandGains.Overall-6.020599913) > 1e-6 {
		t.Fatalf("scalar IR band gain = %.9f dB, want +6.0206 dB", report.IR.BandGains.Overall)
	}
	if math.Abs(report.Delta.Score) > 1e-12 || math.Abs(report.Delta.SpectralDB) > 1e-9 {
		t.Fatalf("equal-RMS scalar changed comparison: %+v", report.Delta)
	}
}

func TestAnalyzeLowPassAttributesColoration(t *testing.T) {
	identity := testSignal(testSampleRate)
	selected := fir(identity, []float64{0.5, 0.5})
	reference := append([]float64(nil), selected...)

	report, err := Analyze(reference, identity, selected, []float64{0.5, 0.5}, Config{
		SampleRate: testSampleRate, MIDINote: 60, ReleaseAfter: 0.5, Source: "low-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Attribution.Classification != "coloration_contribution" {
		t.Fatalf("classification = %q, want coloration_contribution (delta %+v)", report.Attribution.Classification, report.Delta)
	}
	if report.Delta.SpectralDB >= -0.05 {
		t.Fatalf("equal-RMS spectral delta = %.6f dB, want a material improvement", report.Delta.SpectralDB)
	}
	if report.IR.BandGains.High >= report.IR.BandGains.Low {
		t.Fatalf("low-pass response bands = %+v, want more high-band attenuation", report.IR.BandGains)
	}
}

func TestClassifyDoesNotLetCompositeScoreMaskSpectralRegression(t *testing.T) {
	report := Report{Delta: MetricDelta{
		RMSDB: 26.8, Score: -0.003, SpectralDB: 5.9,
	}}
	got := classify(report)
	if got.Classification != "level_effect_with_coloration_regression" {
		t.Fatalf("classification = %q, want level_effect_with_coloration_regression", got.Classification)
	}
}

func TestAnalyzeRejectsInvalidInput(t *testing.T) {
	valid := testSignal(testSampleRate)
	tests := []struct {
		name                        string
		ref, identity, selected, ir []float64
		cfg                         Config
	}{
		{name: "sample rate", ref: valid, identity: valid, selected: valid, ir: []float64{1}, cfg: Config{}},
		{name: "empty reference", identity: valid, selected: valid, ir: []float64{1}, cfg: Config{SampleRate: testSampleRate}},
		{name: "empty identity", ref: valid, selected: valid, ir: []float64{1}, cfg: Config{SampleRate: testSampleRate}},
		{name: "empty selected", ref: valid, identity: valid, ir: []float64{1}, cfg: Config{SampleRate: testSampleRate}},
		{name: "empty IR", ref: valid, identity: valid, selected: valid, cfg: Config{SampleRate: testSampleRate}},
		{name: "negative release", ref: valid, identity: valid, selected: valid, ir: []float64{1}, cfg: Config{SampleRate: testSampleRate, ReleaseAfter: -1}},
		{name: "non-finite", ref: valid, identity: valid, selected: []float64{math.NaN()}, ir: []float64{1}, cfg: Config{SampleRate: testSampleRate}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Analyze(tt.ref, tt.identity, tt.selected, tt.ir, tt.cfg); err == nil {
				t.Fatal("Analyze succeeded, want error")
			}
		})
	}
}

func testSignal(sampleRate int) []float64 {
	n := sampleRate
	out := make([]float64, n)
	for i := range out {
		t := float64(i) / float64(sampleRate)
		env := math.Exp(-1.7 * t)
		out[i] = env * (0.5*math.Sin(2*math.Pi*261.63*t) + 0.3*math.Sin(2*math.Pi*1700*t) + 0.2*math.Sin(2*math.Pi*3300*t))
	}
	return out
}

func scale(in []float64, gain float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * gain
	}
	return out
}

func fir(in, ir []float64) []float64 {
	out := make([]float64, len(in))
	for i := range out {
		for j, h := range ir {
			if i >= j {
				out[i] += h * in[i-j]
			}
		}
	}
	return out
}
