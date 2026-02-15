package main

import (
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-piano/piano"
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

func TestInitCandidateLegacyMixKnobs(t *testing.T) {
	base := piano.NewDefaultParams()
	defs, cand := initCandidate(base, 60)
	// Legacy mode: 16 knobs (output_gain + 3 legacy mix + 12 others)
	if len(defs) != 16 {
		t.Fatalf("legacy defs len = %d, want 16", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}
	knobNames := map[string]bool{}
	for _, d := range defs {
		knobNames[d.Name] = true
	}
	for _, name := range []string{"ir_wet_mix", "ir_dry_mix", "ir_gain"} {
		if !knobNames[name] {
			t.Fatalf("expected legacy knob %q", name)
		}
	}
	for _, name := range []string{"body_dry", "body_gain", "room_wet", "room_gain"} {
		if knobNames[name] {
			t.Fatalf("unexpected dual-IR knob %q in legacy mode", name)
		}
	}
}

func TestInitCandidateDualIRMixKnobs(t *testing.T) {
	base := piano.NewDefaultParams()
	base.BodyIRWavPath = "body.wav"
	base.RoomIRWavPath = "room.wav"
	defs, cand := initCandidate(base, 60)
	// Dual-IR mode: 17 knobs (output_gain + 4 dual mix + 12 others)
	if len(defs) != 17 {
		t.Fatalf("dual-IR defs len = %d, want 17", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}
	knobNames := map[string]bool{}
	for _, d := range defs {
		knobNames[d.Name] = true
	}
	for _, name := range []string{"body_dry", "body_gain", "room_wet", "room_gain"} {
		if !knobNames[name] {
			t.Fatalf("expected dual-IR knob %q", name)
		}
	}
	for _, name := range []string{"ir_wet_mix", "ir_dry_mix", "ir_gain"} {
		if knobNames[name] {
			t.Fatalf("unexpected legacy knob %q in dual-IR mode", name)
		}
	}
}

func TestApplyCandidateDualIRMix(t *testing.T) {
	base := piano.NewDefaultParams()
	base.BodyIRWavPath = "body.wav"
	base.RoomIRWavPath = "room.wav"
	defs, _ := initCandidate(base, 60)

	// Set specific values for dual-IR mix knobs.
	vals := make([]float64, len(defs))
	for i, d := range defs {
		vals[i] = (d.Min + d.Max) / 2 // midpoint
		switch d.Name {
		case "body_dry":
			vals[i] = 0.9
		case "body_gain":
			vals[i] = 1.5
		case "room_wet":
			vals[i] = 0.7
		case "room_gain":
			vals[i] = 1.2
		}
	}
	p, _, _ := applyCandidate(base, 60, defs, candidate{Vals: vals})
	if p.BodyDryMix != 0.9 {
		t.Fatalf("BodyDryMix = %v, want 0.9", p.BodyDryMix)
	}
	if p.BodyIRGain != 1.5 {
		t.Fatalf("BodyIRGain = %v, want 1.5", p.BodyIRGain)
	}
	if p.RoomWetMix != float32(0.7) {
		t.Fatalf("RoomWetMix = %v, want 0.7", p.RoomWetMix)
	}
	if p.RoomGain != float32(1.2) {
		t.Fatalf("RoomGain = %v, want 1.2", p.RoomGain)
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
