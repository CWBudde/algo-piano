package main

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cwbudde/algo-piano/piano"
)

func TestParsePass(t *testing.T) {
	tests := []struct {
		raw       string
		wantName  string
		wantAllow []string
		wantErr   bool
	}{
		{raw: "", wantName: passNone},
		{raw: "none", wantName: passNone},
		{raw: " ATTACK ", wantName: passAttack},
		{raw: "sustain", wantName: passSustain},
		{raw: "inharmonicity", wantName: passInharmonicity},
		{raw: "decay", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parsePass(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parsePass(%q) expected error", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parsePass(%q): %v", tt.raw, err)
		}
		if got.Name != tt.wantName {
			t.Fatalf("parsePass(%q).Name = %q, want %q", tt.raw, got.Name, tt.wantName)
		}
		if got.Name == passNone && !got.isNone() {
			t.Fatalf("parsePass(%q) should be none", tt.raw)
		}
	}

	attack, _ := parsePass("attack")
	for _, want := range []string{"hammer_stiffness_scale", "attack_noise_color", "render.velocity"} {
		if !knobAllowed(want, attack.Allow) {
			t.Fatalf("attack pass should allow %q", want)
		}
	}
	if knobAllowed("per_note.60.loss", attack.Allow) {
		t.Fatal("attack pass must not allow per-note loss")
	}
}

func TestKnobAllowedWildcard(t *testing.T) {
	sustain, _ := parsePass("sustain")
	if !knobAllowed("per_note.60.loss", sustain.Allow) {
		t.Fatal("per_note.60.loss should match per_note.*.loss")
	}
	if !knobAllowed("per_note.108.loss", sustain.Allow) {
		t.Fatal("per_note.108.loss should match per_note.*.loss")
	}
	if knobAllowed("per_note.60.inharmonicity", sustain.Allow) {
		t.Fatal("per_note.60.inharmonicity must not match per_note.*.loss")
	}
	if knobAllowed("per_note.loss", sustain.Allow) {
		t.Fatal("a two-segment name must not match a three-segment pattern")
	}

	inh, _ := parsePass("inharmonicity")
	if !knobAllowed("per_note.72.strike_position", inh.Allow) {
		t.Fatal("strike_position should be allowed in the inharmonicity pass")
	}
	if !knobAllowed("unison_detune_scale", inh.Allow) {
		t.Fatal("unison_detune_scale should be allowed in the inharmonicity pass")
	}
}

func TestFilterKnobsForPass(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true}
	defs, cand := initCandidate(base, 48000, []int{48, 60}, 118, 3.5, groups, false)

	t.Run("none is a pass-through", func(t *testing.T) {
		gotDefs, gotCand := filterKnobsForPass(defs, cand, passSpec{Name: passNone})
		if len(gotDefs) != len(defs) || !reflect.DeepEqual(gotCand.Vals, cand.Vals) {
			t.Fatal("none must not filter anything")
		}
	})

	for _, name := range []string{passAttack, passSustain, passInharmonicity} {
		t.Run(name, func(t *testing.T) {
			spec, err := parsePass(name)
			if err != nil {
				t.Fatalf("parsePass: %v", err)
			}
			gotDefs, gotCand := filterKnobsForPass(defs, cand, spec)
			if len(gotDefs) == 0 {
				t.Fatal("pass filtered everything away")
			}
			if len(gotDefs) >= len(defs) {
				t.Fatalf("pass %s kept %d of %d knobs; it should restrict", name, len(gotDefs), len(defs))
			}
			if len(gotCand.Vals) != len(gotDefs) {
				t.Fatalf("values %d != defs %d", len(gotCand.Vals), len(gotDefs))
			}
			// Values must be carried over unchanged, and defs order preserved.
			byName := make(map[string]float64, len(defs))
			for i, d := range defs {
				byName[d.Name] = cand.Vals[i]
			}
			lastIdx := -1
			origIdx := make(map[string]int, len(defs))
			for i, d := range defs {
				origIdx[d.Name] = i
			}
			for i, d := range gotDefs {
				if !knobAllowed(d.Name, spec.Allow) {
					t.Fatalf("knob %q survived the %s filter", d.Name, name)
				}
				if gotCand.Vals[i] != byName[d.Name] {
					t.Fatalf("knob %q value changed: %v -> %v", d.Name, byName[d.Name], gotCand.Vals[i])
				}
				if origIdx[d.Name] <= lastIdx {
					t.Fatalf("defs order not preserved at %q", d.Name)
				}
				lastIdx = origIdx[d.Name]
			}
		})
	}

	t.Run("sustain covers every per-note loss knob", func(t *testing.T) {
		spec, _ := parsePass(passSustain)
		gotDefs, _ := filterKnobsForPass(defs, cand, spec)
		found := 0
		for _, d := range gotDefs {
			if d.NoteField == noteFieldLoss {
				found++
			}
		}
		if found != 2 {
			t.Fatalf("per-note loss knobs kept = %d, want 2 (one per note)", found)
		}
	})
}

func TestParseWindow(t *testing.T) {
	tests := []struct {
		raw     string
		want    windowSpec
		wantErr bool
	}{
		{raw: "", want: windowSpec{}},
		{raw: "0.1:0.5", want: windowSpec{StartSec: 0.1, EndSec: 0.5}},
		{raw: " 0.1 : 0.5 ", want: windowSpec{StartSec: 0.1, EndSec: 0.5}},
		{raw: ":0.5", want: windowSpec{EndSec: 0.5}},
		{raw: "0.5:", want: windowSpec{StartSec: 0.5}},
		{raw: "0.5", wantErr: true},
		{raw: "0.5:0.2", wantErr: true},
		{raw: "-1:2", wantErr: true},
		{raw: "a:b", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseWindow(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseWindow(%q) expected error", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseWindow(%q): %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("parseWindow(%q) = %+v, want %+v", tt.raw, got, tt.want)
		}
	}
}

func TestWindowSliceBounds(t *testing.T) {
	x := make([]float64, 1000)
	for i := range x {
		x[i] = float64(i)
	}

	if got := (windowSpec{}).slice(x, 1000); len(got) != 1000 {
		t.Fatalf("zero window sliced: %d", len(got))
	}
	got := windowSpec{StartSec: 0.1, EndSec: 0.4}.slice(x, 1000)
	if len(got) != 300 || got[0] != 100 {
		t.Fatalf("slice = len %d start %v", len(got), got[0])
	}
	// End past the signal clamps.
	got = windowSpec{StartSec: 0.5, EndSec: 99}.slice(x, 1000)
	if len(got) != 500 || got[0] != 500 {
		t.Fatalf("clamped slice = len %d start %v", len(got), got[0])
	}
	// A window entirely past the signal degrades to the full signal rather
	// than an empty one.
	if got := (windowSpec{StartSec: 50, EndSec: 60}).slice(x, 1000); len(got) != 1000 {
		t.Fatalf("out-of-range window = %d, want the untouched signal", len(got))
	}
}

func TestPassScorerWindowsBothSignals(t *testing.T) {
	if passScorer(passSpec{Name: passNone}) != nil {
		t.Fatal("the none pass must leave the scorer on analysis.Compare")
	}

	const sampleRate = 8000
	ref := syntheticReference(t, 60, sampleRate, 0.5)
	cand := syntheticReference(t, 60, sampleRate, 0.5)
	// Corrupt only the first 0.1 s of the candidate.
	for i := 0; i < sampleRate/10; i++ {
		cand[i] = 0
	}

	full := passScorer(passSpec{Name: passSustain})
	windowed := passScorer(passSpec{Name: passSustain, Window: windowSpec{StartSec: 0.2, EndSec: 0.5}})

	fullScore := full(ref, cand, sampleRate).Score
	windowedScore := windowed(ref, cand, sampleRate).Score
	if math.Abs(fullScore-windowedScore) < 1e-9 {
		t.Fatalf("windowing had no effect: %v vs %v", fullScore, windowedScore)
	}
	// The window excludes the damage, so it must score better.
	if windowedScore >= fullScore {
		t.Fatalf("windowed score %v should be better than full %v", windowedScore, fullScore)
	}
}

func TestSeedRenderControls(t *testing.T) {
	defs := []knobDef{
		{Name: "output_gain", Min: 0.01, Max: 5.0},
		{Name: "render.velocity", Min: 40, Max: 127, IsInt: true},
		{Name: "render.release_after", Min: 0.2, Max: 3.5},
	}
	c := candidate{Vals: []float64{1.2, 95, 1.25}}

	gotVel, gotRel := seedRenderControls(defs, c, 118, 3.5)
	if gotVel != 95 || gotRel != 1.25 {
		t.Fatalf("seedRenderControls = %d, %v; want 95, 1.25", gotVel, gotRel)
	}

	// Knobs that are absent leave the incoming defaults alone.
	gotVel, gotRel = seedRenderControls(defs[:1], candidate{Vals: []float64{1.2}}, 118, 3.5)
	if gotVel != 118 || gotRel != 3.5 {
		t.Fatalf("absent knobs changed the defaults: %d, %v", gotVel, gotRel)
	}
}

// TestPassSeedsRenderControlsFromReport covers the trap: render.velocity and
// render.release_after are not preset fields, they come from
// --velocity/--release-after. A pass that filters them out would otherwise
// silently revert them to the CLI defaults instead of keeping what a previous
// run fitted.
func TestPassSeedsRenderControlsFromReport(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "rep.json")
	report := `{"best_knobs":{
		"render.velocity": 95,
		"render.release_after": 1.25,
		"high_freq_damping": 0.31,
		"hammer_stiffness_scale": 1.44
	}}`
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true}
	defs, initCand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups, false)

	resumed, ok, err := loadCandidateFromReport(reportPath, defs, initCand)
	if err != nil || !ok {
		t.Fatalf("resume failed: ok=%v err=%v", ok, err)
	}

	// The CLI values, before seeding.
	velocity, releaseAfter := 118, 3.5
	velocity, releaseAfter = seedRenderControls(defs, resumed, velocity, releaseAfter)
	if velocity != 95 {
		t.Fatalf("velocity = %d, want the fitted 95 (not the CLI default 118)", velocity)
	}
	if math.Abs(releaseAfter-1.25) > 1e-12 {
		t.Fatalf("releaseAfter = %v, want the fitted 1.25 (not the CLI default 3.5)", releaseAfter)
	}

	// The sustain pass drops render.velocity but keeps render.release_after.
	spec, _ := parsePass(passSustain)
	filteredDefs, filteredCand := filterKnobsForPass(defs, resumed, spec)
	for _, d := range filteredDefs {
		if d.Name == "render.velocity" {
			t.Fatal("sustain pass should not keep render.velocity")
		}
	}

	// applyCandidate must now use the seeded velocity, not the CLI default,
	// while release_after still comes from the surviving knob.
	_, _, gotVel, gotRel := applyCandidate(base, 48000, velocity, releaseAfter, filteredDefs, filteredCand)
	if gotVel != 95 {
		t.Fatalf("applyCandidate velocity = %d, want the frozen 95", gotVel)
	}
	if math.Abs(gotRel-1.25) > 1e-12 {
		t.Fatalf("applyCandidate releaseAfter = %v, want 1.25", gotRel)
	}

	// And the frozen preset fields keep their preset values, because
	// applyCandidate only writes knobs present in defs.
	_, params, _, _ := applyCandidate(base, 48000, velocity, releaseAfter, filteredDefs, filteredCand)
	if params.HammerStiffnessScale != base.HammerStiffnessScale {
		t.Fatalf("hammer_stiffness_scale = %v, want the frozen preset value %v",
			params.HammerStiffnessScale, base.HammerStiffnessScale)
	}
	if math.Abs(float64(params.HighFreqDamping)-0.31) > 1e-6 {
		t.Fatalf("high_freq_damping = %v, want the resumed 0.31", params.HighFreqDamping)
	}
}
