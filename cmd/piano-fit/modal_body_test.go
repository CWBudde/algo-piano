package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
)

func fitterTestBodyTransfer() *irsynth.BodyModalTransfer {
	return &irsynth.BodyModalTransfer{
		SchemaVersion: irsynth.BodyModalTransferSchemaVersion,
		TransferKind:  irsynth.BodyModalTransferKind,
		ModelSHA256:   strings.Repeat("b", 64),
		InputUnit:     irsynth.BodyModalTransferInputUnit,
		OutputUnit:    irsynth.BodyModalTransferOutputUnit,
		SourceID:      "test-bridge",
		Modes: []irsynth.BodyTransferMode{
			{FrequencyHz: 100, LossFactor: 0.02, Residue: -0.2},
			{FrequencyHz: 300, LossFactor: 0.04, Residue: 0.1},
		},
	}
}

func TestInitCandidateModalBodyUsesOnlyCheapControls(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"body-ir": true, "mix": true, modalBodyGroup: true}
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups, false)
	if len(cand.Vals) != len(defs) {
		t.Fatalf("candidate values = %d, defs = %d", len(cand.Vals), len(defs))
	}
	names := knobNameSet(defs)
	for _, name := range []string{"body_transfer_gain", "body_loss_scale", "body_duration", "body_fadeout"} {
		if !names[name] {
			t.Errorf("missing modal body knob %q", name)
		}
	}
	for _, name := range []string{"body_modes", "body_brightness", "body_plate_ratio", "body_stiffness_ratio", "body_mode_warp"} {
		if names[name] {
			t.Errorf("offline solve knob %q must not be optimized", name)
		}
	}
	if !needsIRSynthesis(map[string]bool{modalBodyGroup: true}) {
		t.Fatal("a selected body transfer must activate buffered IR rendering")
	}
}

func TestApplyCandidateModalBodyControls(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"body-ir": true, modalBodyGroup: true}
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups, false)
	want := map[string]float64{
		"body_transfer_gain": 4,
		"body_loss_scale":    2,
		"body_duration":      0.2,
		"body_fadeout":       0.03,
	}
	for i, def := range defs {
		if v, ok := want[def.Name]; ok {
			cand.Vals[i] = v
		}
	}
	configs, _, _, _ := applyCandidate(base, 48000, 118, 3.5, defs, cand)
	if configs.modalBody.TransferGain != 4 || configs.modalBody.LossScale != 2 ||
		configs.modalBody.DurationS != 0.2 || configs.modalBody.FadeOutS != 0.03 {
		t.Fatalf("modal controls not applied: %+v", configs.modalBody)
	}
}

func TestModalTransferIsLoadedOnceOutsideEvaluations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.json")
	data := `{
		"schema_version":1,
		"transfer_kind":"bridge_force_to_area_velocity",
		"model_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"input_unit":"N*s",
		"output_unit":"m/s",
		"source_id":"bridge",
		"modes":[{"frequency_hz":100,"loss_factor":0.02,"residue":0.25}]
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	transfer, err := irsynth.LoadBodyModalTransfer(path)
	if err != nil {
		t.Fatal(err)
	}
	// Removing the only on-disk copy makes a per-evaluation reload observable.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cfg := &optimizationConfig{
		groups:       map[string]bool{modalBodyGroup: true},
		bodyTransfer: transfer,
	}
	irCfgs := irConfigs{modalBody: irsynth.ModalBodyConfig{
		SampleRate: 8000, DurationS: 0.01, LossScale: 1, TransferGain: 1,
	}}
	first, _, _, err := synthesizeIRs(cfg, irCfgs)
	if err != nil {
		t.Fatalf("first evaluation synthesis: %v", err)
	}
	second, _, _, err := synthesizeIRs(cfg, irCfgs)
	if err != nil {
		t.Fatalf("second evaluation synthesis: %v", err)
	}
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("unexpected response lengths: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("non-deterministic synthesis at sample %d", i)
		}
	}
}

func TestBodyTransferReportRecordsArtifactAndAppliedGain(t *testing.T) {
	transfer := fitterTestBodyTransfer()
	report := makeBodyTransferReport("model.json", transfer, map[string]float64{
		"body_transfer_gain": 3.5,
		"body_loss_scale":    1.25,
	})
	if report == nil || report.Path != "model.json" || report.ModelSHA256 != transfer.ModelSHA256 || report.SourceID != transfer.SourceID {
		t.Fatalf("artifact provenance missing from report: %+v", report)
	}
	if report.TransferGain != 3.5 || report.LossScale != 1.25 {
		t.Fatalf("applied controls missing from report: %+v", report)
	}
}
