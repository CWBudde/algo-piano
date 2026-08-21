package main

import (
	"fmt"
	"math"
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
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups)

	// piano: 17 knobs (incl attack noise + high_freq_damping), legacy mix: 3 knobs = 20 total
	if len(defs) != 20 {
		t.Fatalf("defs len = %d, want 20", len(defs))
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
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups)

	// piano: 17 knobs (incl attack noise + high_freq_damping), dual-IR mix: 4 knobs = 21 total
	if len(defs) != 21 {
		t.Fatalf("defs len = %d, want 21", len(defs))
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
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups)

	// body-ir: 11 knobs (plate_ratio, stiffness_ratio, mode_warp, 2-way decay, crossover, fadeout, etc), dual-IR mix: 4 knobs = 15 total
	if len(defs) != 15 {
		t.Fatalf("defs len = %d, want 15", len(defs))
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
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups)

	// piano: 17, body-ir: 11 (Kirchhoff plate + mode_warp + 2-way decay + fadeout), room-ir: 8 (incl fadeout), dual-IR mix: 4 = 40 total
	if len(defs) != 40 {
		t.Fatalf("defs len = %d, want 40", len(defs))
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
	defs, _ := initCandidate(base, 48000, []int{60}, 118, 3.5, groups)

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

	_, params, velocity, releaseAfter := applyCandidate(base, 48000, 118, 3.5, defs, candidate{Vals: vals})

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
	defs, _ := initCandidate(base, 48000, []int{60}, 118, 3.5, groups)

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

	_, params, _, _ := applyCandidate(base, 48000, 118, 3.5, defs, candidate{Vals: vals})
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

func TestInitCandidateMultiNoteKnobCounts(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true, "mix": true}
	notes := []int{48, 60, 72}
	defs, cand := initCandidate(base, 48000, notes, 118, 3.5, groups)

	// 14 shared piano knobs + 3 per-note knobs x 3 notes + 3 legacy mix knobs.
	if len(defs) != 26 {
		t.Fatalf("defs len = %d, want 26", len(defs))
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}

	names := knobNameSet(defs)
	for _, n := range notes {
		for _, suffix := range []string{"loss", "inharmonicity", "strike_position"} {
			name := fmt.Sprintf("per_note.%d.%s", n, suffix)
			if !names[name] {
				t.Fatalf("expected knob %q", name)
			}
		}
	}

	perNoteCount := 0
	for _, d := range defs {
		if d.NoteField == noteFieldNone {
			continue
		}
		perNoteCount++
		if d.Note != 48 && d.Note != 60 && d.Note != 72 {
			t.Fatalf("knob %q has unexpected note %d", d.Name, d.Note)
		}
	}
	if perNoteCount != 9 {
		t.Fatalf("per-note knob count = %d, want 9", perNoteCount)
	}
}

func TestApplyCandidateMultiNotePerNote(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true}
	notes := []int{48, 60, 72}
	defs, _ := initCandidate(base, 48000, notes, 118, 3.5, groups)

	want := map[int][3]float64{
		48: {0.9900, 0.05, 0.10},
		60: {0.9950, 0.20, 0.20},
		72: {0.9990, 0.40, 0.30},
	}

	vals := make([]float64, len(defs))
	for i, d := range defs {
		vals[i] = (d.Min + d.Max) / 2
		if d.NoteField == noteFieldNone {
			continue
		}
		w := want[d.Note]
		switch d.NoteField {
		case noteFieldLoss:
			vals[i] = w[0]
		case noteFieldInharmonicity:
			vals[i] = w[1]
		case noteFieldStrikePosition:
			vals[i] = w[2]
		case noteFieldNone:
		}
	}

	_, params, _, _ := applyCandidate(base, 48000, 118, 3.5, defs, candidate{Vals: vals})
	for n, w := range want {
		np := params.PerNote[n]
		if np == nil {
			t.Fatalf("PerNote[%d] missing", n)
		}
		if np.Loss != float32(w[0]) {
			t.Fatalf("PerNote[%d].Loss = %v, want %v", n, np.Loss, float32(w[0]))
		}
		if np.Inharmonicity != float32(w[1]) {
			t.Fatalf("PerNote[%d].Inharmonicity = %v, want %v", n, np.Inharmonicity, float32(w[1]))
		}
		if np.StrikePosition != float32(w[2]) {
			t.Fatalf("PerNote[%d].StrikePosition = %v, want %v", n, np.StrikePosition, float32(w[2]))
		}
	}
}

func TestToNormalizedRoundTrip(t *testing.T) {
	defs := []knobDef{
		{Name: "linear", Min: 0.0, Max: 2.0},
		{Name: "linear_offset", Min: -12.0, Max: 0.0},
		{Name: "log", Min: 3.0, Max: 25.0, LogScale: true},
		{Name: "log_small", Min: 0.001, Max: 0.15, LogScale: true},
		{Name: "int", Min: 40, Max: 127, IsInt: true},
		{Name: "degenerate", Min: 1.0, Max: 1.0},
	}

	for _, pos := range [][]float64{
		{0, 0, 0, 0, 0, 0},
		{1, 1, 1, 1, 1, 1},
		{0.25, 0.5, 0.75, 0.125, 0.5, 0.5},
		{0.9, 0.1, 0.33, 0.66, 0.8, 0.0},
	} {
		cand := fromNormalized(pos, defs)
		back := toNormalized(cand, defs)
		round := fromNormalized(back, defs)
		for i := range defs {
			got, want := round.Vals[i], cand.Vals[i]
			tol := math.Abs(want) * 1e-9
			if tol < 1e-9 {
				tol = 1e-9
			}
			if math.Abs(got-want) > tol {
				t.Fatalf("knob %q round-trip = %v, want %v (pos %v)", defs[i].Name, got, want, pos)
			}
		}
	}

	// toNormalized clamps out-of-range values into the unit hypercube.
	out := toNormalized(candidate{Vals: []float64{-5, 5, 0.0001, 100, 1000, 7}}, defs)
	for i, x := range out {
		if x < 0 || x > 1 {
			t.Fatalf("toNormalized[%d] = %v, want within [0,1]", i, x)
		}
	}
}
