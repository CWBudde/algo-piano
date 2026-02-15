package main

import (
	"fmt"
	"testing"

	"github.com/cwbudde/algo-piano/piano"
)

func TestParseOptimizeGroups(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]bool
		wantErr bool
	}{
		{
			name:  "single group",
			input: "piano",
			want:  map[string]bool{"piano": true},
		},
		{
			name:  "multiple groups",
			input: "piano,mix,body-ir",
			want:  map[string]bool{"piano": true, "mix": true, "body-ir": true},
		},
		{
			name:  "all groups",
			input: "piano,body-ir,room-ir,mix",
			want:  map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true},
		},
		{
			name:  "with whitespace",
			input: " piano , mix ",
			want:  map[string]bool{"piano": true, "mix": true},
		},
		{
			name:    "invalid group",
			input:   "piano,bogus",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "only whitespace",
			input:   "  ,  ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOptimizeGroups(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseOptimizeGroups(%q) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOptimizeGroups(%q) unexpected error: %v", tt.input, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseOptimizeGroups(%q) returned %d groups, want %d", tt.input, len(got), len(tt.want))
			}
			for k := range tt.want {
				if !got[k] {
					t.Fatalf("parseOptimizeGroups(%q) missing group %q", tt.input, k)
				}
			}
		})
	}
}

func TestNeedsIRSynthesis(t *testing.T) {
	tests := []struct {
		name   string
		groups map[string]bool
		want   bool
	}{
		{
			name:   "piano and mix only",
			groups: map[string]bool{"piano": true, "mix": true},
			want:   false,
		},
		{
			name:   "body-ir present",
			groups: map[string]bool{"body-ir": true, "mix": true},
			want:   true,
		},
		{
			name:   "room-ir present",
			groups: map[string]bool{"room-ir": true, "mix": true},
			want:   true,
		},
		{
			name:   "full set",
			groups: map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsIRSynthesis(tt.groups)
			if got != tt.want {
				t.Fatalf("needsIRSynthesis(%v) = %v, want %v", tt.groups, got, tt.want)
			}
		})
	}
}

func knobNameSet(defs []knobDef) map[string]bool {
	m := make(map[string]bool, len(defs))
	for _, d := range defs {
		m[d.Name] = true
	}
	return m
}

func TestInitCandidatePianoMixOnly(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 118, 3.5, groups)

	// piano: 13 knobs, legacy mix: 3 knobs = 16 total
	if len(defs) != 16 {
		t.Fatalf("defs len = %d, want 16", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}

	names := knobNameSet(defs)
	for _, name := range []string{"output_gain", "hammer_stiffness_scale", "render.velocity", "render.release_after"} {
		if !names[name] {
			t.Fatalf("expected knob %q", name)
		}
	}
	// Legacy mix knobs present (no dual-IR paths set).
	for _, name := range []string{"ir_wet_mix", "ir_dry_mix", "ir_gain"} {
		if !names[name] {
			t.Fatalf("expected legacy mix knob %q", name)
		}
	}
	// No body_modes (body-ir group not active).
	if names["body_modes"] {
		t.Fatal("unexpected body_modes knob in piano+mix mode")
	}
}

func TestInitCandidatePianoMixDualIR(t *testing.T) {
	base := piano.NewDefaultParams()
	base.BodyIRWavPath = "body.wav"
	base.RoomIRWavPath = "room.wav"
	groups := map[string]bool{"piano": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 118, 3.5, groups)

	// piano: 13 knobs, dual-IR mix: 4 knobs = 17 total
	if len(defs) != 17 {
		t.Fatalf("defs len = %d, want 17", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}

	names := knobNameSet(defs)
	for _, name := range []string{"body_dry", "body_gain", "room_wet", "room_gain"} {
		if !names[name] {
			t.Fatalf("expected dual-IR mix knob %q", name)
		}
	}
	if names["ir_wet_mix"] {
		t.Fatal("unexpected legacy knob ir_wet_mix in dual-IR mode")
	}
}

func TestInitCandidateBodyIRMix(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"body-ir": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 118, 3.5, groups)

	// body-ir: 7 knobs (incl fadeout), dual-IR mix (because body-ir triggers needsIRSynthesis): 4 knobs = 11 total
	if len(defs) != 11 {
		t.Fatalf("defs len = %d, want 11", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}

	names := knobNameSet(defs)
	for _, name := range []string{"body_modes", "body_brightness", "body_dry", "body_gain"} {
		if !names[name] {
			t.Fatalf("expected knob %q", name)
		}
	}
	if names["output_gain"] {
		t.Fatal("unexpected output_gain knob when piano group not active")
	}
}

func TestInitCandidateFullJoint(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 118, 3.5, groups)

	// piano: 13, body-ir: 7 (incl fadeout), room-ir: 8 (incl fadeout), dual-IR mix: 4 = 32 total
	if len(defs) != 32 {
		t.Fatalf("defs len = %d, want 32", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}

	names := knobNameSet(defs)
	// Spot-check a knob from each group.
	for _, name := range []string{
		"output_gain",          // piano
		"body_modes",           // body-ir
		"room_early",           // room-ir
		"body_dry",             // dual-IR mix
		"render.velocity",      // piano render
		"per_note.60.loss",     // piano per_note
		"hammer_damping_scale", // piano hammer
	} {
		if !names[name] {
			t.Fatalf("expected knob %q in full joint mode", name)
		}
	}
}

func TestApplyCandidatePianoKnobs(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true, "mix": true}
	defs, _ := initCandidate(base, 48000, 60, 118, 3.5, groups)

	// Build candidate with specific values for piano knobs.
	vals := make([]float64, len(defs))
	for i, d := range defs {
		vals[i] = (d.Min + d.Max) / 2 // default to midpoint
	}
	// Set specific known values.
	for i, d := range defs {
		switch d.Name {
		case "output_gain":
			vals[i] = 1.1
		case "hammer_stiffness_scale":
			vals[i] = 1.4
		case "unison_detune_scale":
			vals[i] = 0.5
		case fmt.Sprintf("per_note.%d.loss", 60):
			vals[i] = 0.997
		case "render.velocity":
			vals[i] = 100
		case "render.release_after":
			vals[i] = 2.0
		}
	}

	_, params, velocity, releaseAfter := applyCandidate(base, 48000, 60, 118, 3.5, defs, candidate{Vals: vals})

	if params.OutputGain != float32(1.1) {
		t.Fatalf("OutputGain = %v, want 1.1", params.OutputGain)
	}
	if params.HammerStiffnessScale != float32(1.4) {
		t.Fatalf("HammerStiffnessScale = %v, want 1.4", params.HammerStiffnessScale)
	}
	if params.UnisonDetuneScale != float32(0.5) {
		t.Fatalf("UnisonDetuneScale = %v, want 0.5", params.UnisonDetuneScale)
	}
	if params.PerNote[60] == nil || params.PerNote[60].Loss != float32(0.997) {
		t.Fatalf("PerNote[60].Loss = %v, want 0.997", params.PerNote[60].Loss)
	}
	if velocity != 100 {
		t.Fatalf("velocity = %d, want 100", velocity)
	}
	if releaseAfter != 2.0 {
		t.Fatalf("releaseAfter = %v, want 2.0", releaseAfter)
	}
}

func TestApplyCandidateDualIRMix(t *testing.T) {
	base := piano.NewDefaultParams()
	base.BodyIRWavPath = "body.wav"
	base.RoomIRWavPath = "room.wav"
	groups := map[string]bool{"piano": true, "mix": true}
	defs, _ := initCandidate(base, 48000, 60, 118, 3.5, groups)

	vals := make([]float64, len(defs))
	for i, d := range defs {
		vals[i] = (d.Min + d.Max) / 2
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

	_, params, _, _ := applyCandidate(base, 48000, 60, 118, 3.5, defs, candidate{Vals: vals})
	if params.BodyDryMix != float32(0.9) {
		t.Fatalf("BodyDryMix = %v, want 0.9", params.BodyDryMix)
	}
	if params.BodyIRGain != float32(1.5) {
		t.Fatalf("BodyIRGain = %v, want 1.5", params.BodyIRGain)
	}
	if params.RoomWetMix != float32(0.7) {
		t.Fatalf("RoomWetMix = %v, want 0.7", params.RoomWetMix)
	}
	if params.RoomGain != float32(1.2) {
		t.Fatalf("RoomGain = %v, want 1.2", params.RoomGain)
	}
}
