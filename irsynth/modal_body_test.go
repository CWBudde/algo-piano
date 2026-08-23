package irsynth

import (
	"math"
	"strings"
	"testing"
)

const validBodyTransferJSON = `{
  "schema_version": 1,
  "transfer_kind": "bridge_force_to_area_velocity",
  "model_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "input_unit": "N*s",
  "output_unit": "m/s",
  "source_id": "bridge-main",
  "modes": [
    {"frequency_hz": 100, "loss_factor": 0.02, "residue": -0.25},
    {"frequency_hz": 250, "loss_factor": 0.03, "residue": 0.1}
  ]
}`

func TestParseBodyModalTransferStrict(t *testing.T) {
	transfer, err := ParseBodyModalTransfer([]byte(validBodyTransferJSON))
	if err != nil {
		t.Fatalf("ParseBodyModalTransfer: %v", err)
	}
	if transfer.SourceID != "bridge-main" || len(transfer.Modes) != 2 {
		t.Fatalf("unexpected transfer: %+v", transfer)
	}

	unknown := strings.Replace(validBodyTransferJSON, `"source_id"`, `"unknown": true, "source_id"`, 1)
	if _, err := ParseBodyModalTransfer([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := ParseBodyModalTransfer([]byte(validBodyTransferJSON + ` {}`)); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestBodyModalTransferValidation(t *testing.T) {
	valid, err := ParseBodyModalTransfer([]byte(validBodyTransferJSON))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*BodyModalTransfer)
		want   string
	}{
		{"schema", func(v *BodyModalTransfer) { v.SchemaVersion = 2 }, "schema_version"},
		{"kind", func(v *BodyModalTransfer) { v.TransferKind = "displacement" }, "transfer_kind"},
		{"hash", func(v *BodyModalTransfer) { v.ModelSHA256 = "ABC" }, "model_sha256"},
		{"input unit", func(v *BodyModalTransfer) { v.InputUnit = "N" }, "input_unit"},
		{"output unit", func(v *BodyModalTransfer) { v.OutputUnit = "m" }, "output_unit"},
		{"source", func(v *BodyModalTransfer) { v.SourceID = " " }, "source_id"},
		{"empty modes", func(v *BodyModalTransfer) { v.Modes = nil }, "modes"},
		{"frequency order", func(v *BodyModalTransfer) { v.Modes[1].FrequencyHz = v.Modes[0].FrequencyHz }, "strictly increasing"},
		{"loss", func(v *BodyModalTransfer) { v.Modes[0].LossFactor = -1 }, "loss_factor"},
		{"residue", func(v *BodyModalTransfer) { v.Modes[0].Residue = math.NaN() }, "residue"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clone := *valid
			clone.Modes = append([]BodyTransferMode(nil), valid.Modes...)
			tc.mutate(&clone)
			if err := clone.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestGenerateModalBodyOneModeExactVelocityResponse(t *testing.T) {
	transfer := &BodyModalTransfer{
		SchemaVersion: 1,
		TransferKind:  BodyModalTransferKind,
		ModelSHA256:   strings.Repeat("a", 64),
		InputUnit:     BodyModalTransferInputUnit,
		OutputUnit:    BodyModalTransferOutputUnit,
		SourceID:      "test",
		Modes: []BodyTransferMode{{
			FrequencyHz: 100,
			LossFactor:  0.04,
			Residue:     -0.25,
		}},
	}
	cfg := ModalBodyConfig{SampleRate: 8000, DurationS: 0.01, LossScale: 1.5, TransferGain: 2}
	got, err := GenerateModalBody(transfer, cfg)
	if err != nil {
		t.Fatalf("GenerateModalBody: %v", err)
	}
	if got[0] != -0.5 {
		t.Fatalf("sample 0 = %.9g, want exact signed gain/residue -0.5", got[0])
	}
	i := 17
	omega := 2 * math.Pi * transfer.Modes[0].FrequencyHz
	zeta := transfer.Modes[0].LossFactor * cfg.LossScale / 2
	want := cfg.TransferGain * transfer.Modes[0].Residue * modalVelocityImpulse(omega, zeta, float64(i)/float64(cfg.SampleRate))
	if diff := math.Abs(float64(got[i]) - want); diff > 1e-7 {
		t.Fatalf("sample %d = %.12g, want %.12g (diff %.3g)", i, got[i], want, diff)
	}
}

func TestGenerateModalBodyNoNormalizationOrRandomness(t *testing.T) {
	transfer, err := ParseBodyModalTransfer([]byte(validBodyTransferJSON))
	if err != nil {
		t.Fatal(err)
	}
	cfg := ModalBodyConfig{SampleRate: 8000, DurationS: 0.02, LossScale: 1, TransferGain: 1}
	a, err := GenerateModalBody(transfer, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateModalBody(transfer, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TransferGain = 3
	c, err := GenerateModalBody(transfer, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("render is not deterministic at sample %d", i)
		}
		if diff := math.Abs(float64(c[i]) - 3*float64(a[i])); diff > 2e-7 {
			t.Fatalf("gain was normalized away at sample %d: got %.9g, want %.9g", i, c[i], 3*a[i])
		}
	}
}

func TestGenerateModalBodyRejectsAliasing(t *testing.T) {
	transfer, err := ParseBodyModalTransfer([]byte(validBodyTransferJSON))
	if err != nil {
		t.Fatal(err)
	}
	transfer.Modes[1].FrequencyHz = 4000
	cfg := ModalBodyConfig{SampleRate: 8000, DurationS: 0.01, LossScale: 1, TransferGain: 1}
	if _, err := GenerateModalBody(transfer, cfg); err == nil || !strings.Contains(err.Error(), "Nyquist") {
		t.Fatalf("GenerateModalBody error = %v, want Nyquist rejection", err)
	}
}
