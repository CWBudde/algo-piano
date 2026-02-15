package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPresetIRPathRelativizesFromPresetDir(t *testing.T) {
	presetPath := filepath.Join("assets", "presets", "fitted-c4.json")
	irPath := filepath.Join("assets", "ir", "default_96k.wav")

	got := presetIRPath(presetPath, irPath)
	want := filepath.ToSlash(filepath.Join("..", "ir", "default_96k.wav"))
	if got != want {
		t.Fatalf("presetIRPath() = %q, want %q", got, want)
	}
}

func TestPresetIRPathEmpty(t *testing.T) {
	if got := presetIRPath("assets/presets/fitted.json", ""); got != "" {
		t.Fatalf("presetIRPath() = %q, want empty", got)
	}
}

func TestParseWorkersFlag(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "1", want: 1},
		{in: "8", want: 8},
		{in: "auto", want: 0},
		{in: "AUTO", want: 0},
		{in: "0", wantErr: true},
		{in: "-2", wantErr: true},
		{in: "abc", wantErr: true},
	}

	for _, tt := range tests {
		got, err := parseWorkersFlag(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseWorkersFlag(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseWorkersFlag(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseWorkersFlag(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestLoadCandidateFromReportBestKnobs(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "rep.json")
	if err := os.WriteFile(reportPath, []byte(`{"best_knobs":{"output_gain":1.3,"hammer_stiffness_scale":1.7}}`), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	defs := []knobDef{
		{Name: "output_gain", Min: 0.4, Max: 1.8},
		{Name: "hammer_stiffness_scale", Min: 0.6, Max: 1.8},
	}
	fallback := candidate{Vals: []float64{1.0, 1.0}}

	got, ok, err := loadCandidateFromReport(reportPath, defs, fallback)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if !ok {
		t.Fatal("expected resume candidate")
	}
	if got.Vals[0] != 1.3 {
		t.Fatalf("output_gain = %v, want 1.3", got.Vals[0])
	}
	if got.Vals[1] != 1.7 {
		t.Fatalf("hammer_stiffness_scale = %v, want 1.7", got.Vals[1])
	}
}

func TestLoadCandidateFromReportBestIRKnobs(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "rep.json")
	if err := os.WriteFile(reportPath, []byte(`{"best_ir_knobs":{"body_modes":128,"body_brightness":1.25}}`), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	defs := []knobDef{
		{Name: "body_modes", Min: 8, Max: 96, IsInt: true},
		{Name: "body_brightness", Min: 0.5, Max: 2.5},
	}
	fallback := candidate{Vals: []float64{48, 1.0}}

	got, ok, err := loadCandidateFromReport(reportPath, defs, fallback)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if !ok {
		t.Fatal("expected resume candidate")
	}
	// body_modes clamped to Max=96
	if got.Vals[0] != 96 {
		t.Fatalf("body_modes = %v, want 96 (clamped from 128)", got.Vals[0])
	}
	if got.Vals[1] != 1.25 {
		t.Fatalf("body_brightness = %v, want 1.25", got.Vals[1])
	}
}

func TestLoadCandidateFromReportMissingFile(t *testing.T) {
	defs := []knobDef{{Name: "x", Min: 0, Max: 1}}
	fallback := candidate{Vals: []float64{0.5}}

	_, ok, err := loadCandidateFromReport("/nonexistent/path.json", defs, fallback)
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for missing file")
	}
}
