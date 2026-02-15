package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-piano/piano"
)

func TestInitCandidateDefaultKnobs(t *testing.T) {
	defs, cand := initCandidate(piano.NewDefaultParams(), 48000, 60, 118, 3.5, false, false)
	// 6 body + 7 room + 3 mix = 16 knobs
	if len(defs) != 16 {
		t.Fatalf("defs len = %d, want 16", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}
}

func TestApplyCandidateSetsIRAndMix(t *testing.T) {
	base := piano.NewDefaultParams()
	base.PerNote[60] = &piano.NoteParams{Loss: 0.994, Inharmonicity: 0.2, StrikePosition: 0.12}
	defs := []knobDef{
		{Name: "body_modes", Min: 8, Max: 96, IsInt: true},
		{Name: "body_brightness", Min: 0.5, Max: 2.5},
		{Name: "body_density", Min: 0.5, Max: 4.0},
		{Name: "body_direct", Min: 0.1, Max: 1.2},
		{Name: "body_decay", Min: 0.01, Max: 0.5},
		{Name: "body_duration", Min: 0.02, Max: 0.3},
		{Name: "room_early", Min: 0, Max: 64, IsInt: true},
		{Name: "room_late", Min: 0.0, Max: 0.15},
		{Name: "room_stereo_width", Min: 0.0, Max: 1.0},
		{Name: "room_brightness", Min: 0.3, Max: 2.0},
		{Name: "room_low_decay", Min: 0.3, Max: 3.0},
		{Name: "room_high_decay", Min: 0.05, Max: 0.8},
		{Name: "room_duration", Min: 0.3, Max: 2.0},
		{Name: "body_gain", Min: 0.3, Max: 2.0},
		{Name: "body_dry", Min: 0.2, Max: 1.5},
		{Name: "room_wet", Min: 0.0, Max: 1.0},
		{Name: "render.velocity", Min: 40, Max: 127, IsInt: true},
		{Name: "render.release_after", Min: 0.2, Max: 3.5},
		{Name: "output_gain", Min: 0.4, Max: 1.8},
		{Name: "hammer_stiffness_scale", Min: 0.6, Max: 1.8},
		{Name: "per_note.60.loss", Min: 0.985, Max: 0.99995},
	}
	cand := candidate{
		Vals: []float64{
			// body: modes, brightness, density, direct, decay, duration
			48, 1.5, 2.0, 0.9, 0.1, 0.05,
			// room: early, late, stereo_width, brightness, low_decay, high_decay, duration
			12, 0.06, 0.6, 0.8, 1.2, 0.2, 1.0,
			// mix: body_gain, body_dry, room_wet
			1.8, 0.8, 0.3,
			// joint: velocity, release_after, output_gain, hammer_stiffness, loss
			126, 2.8, 1.3, 1.7, 0.991,
		},
	}

	cfg, params, velocity, releaseAfter := applyCandidate(base, 48000, 60, 118, 3.5, defs, cand)
	if cfg.body.Modes != 48 {
		t.Fatalf("cfg.body.Modes = %d, want 48", cfg.body.Modes)
	}
	if cfg.room.EarlyCount != 12 {
		t.Fatalf("cfg.room.EarlyCount = %d, want 12", cfg.room.EarlyCount)
	}
	if params.BodyIRGain != float32(1.8) {
		t.Fatalf("BodyIRGain = %v, want 1.8", params.BodyIRGain)
	}
	if params.BodyDryMix != float32(0.8) {
		t.Fatalf("BodyDryMix = %v, want 0.8", params.BodyDryMix)
	}
	if params.RoomWetMix != float32(0.3) {
		t.Fatalf("RoomWetMix = %v, want 0.3", params.RoomWetMix)
	}
	if velocity != 126 {
		t.Fatalf("velocity = %d, want 126", velocity)
	}
	if releaseAfter != 2.8 {
		t.Fatalf("releaseAfter = %.3f, want 2.8", releaseAfter)
	}
	if params.OutputGain != float32(1.3) {
		t.Fatalf("OutputGain = %v, want 1.3", params.OutputGain)
	}
	if params.HammerStiffnessScale != float32(1.7) {
		t.Fatalf("HammerStiffnessScale = %v, want 1.7", params.HammerStiffnessScale)
	}
	if params.PerNote[60].Loss != float32(0.991) {
		t.Fatalf("PerNote[60].Loss = %v, want 0.991", params.PerNote[60].Loss)
	}
}

func TestLoadCandidateFromReportBestIRKnobs(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "rep.json")
	if err := os.WriteFile(reportPath, []byte(`{"best_ir_knobs":{"modes":128,"brightness":1.25}}`), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	defs := []knobDef{
		{Name: "modes", Min: 32, Max: 256, IsInt: true},
		{Name: "brightness", Min: 0.5, Max: 2.5},
	}
	fallback := candidate{Vals: []float64{64, 1.0}}

	got, ok, err := loadCandidateFromReport(reportPath, defs, fallback)
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if !ok {
		t.Fatalf("expected resume candidate")
	}
	if got.Vals[0] != 128 {
		t.Fatalf("modes = %v, want 128", got.Vals[0])
	}
	if got.Vals[1] != 1.25 {
		t.Fatalf("brightness = %v, want 1.25", got.Vals[1])
	}
}

func TestInitCandidateJointAddsRenderAndPerNoteKnobs(t *testing.T) {
	base := piano.NewDefaultParams()
	base.PerNote[60] = &piano.NoteParams{Loss: 0.994, Inharmonicity: 0.25, StrikePosition: 0.11}
	defs, _ := initCandidate(base, 48000, 60, 118, 3.5, false, true)

	have := map[string]bool{}
	for _, d := range defs {
		have[d.Name] = true
	}
	for _, name := range []string{
		"render.velocity",
		"render.release_after",
		"per_note.60.loss",
		"per_note.60.inharmonicity",
		"per_note.60.strike_position",
		"hammer_stiffness_scale",
		"unison_detune_scale",
		"output_gain",
	} {
		if !have[name] {
			t.Fatalf("expected knob %q in joint mode", name)
		}
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
