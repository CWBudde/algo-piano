package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

// A written preset must state its resonance setting explicitly. With omitempty
// the false was dropped and reloading fell through to whatever piano.Params
// happens to default to, so the preset silently stopped describing the fit it
// came from.
func TestWritePresetJSONStatesResonanceExplicitly(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		params := piano.NewDefaultParams()
		params.ResonanceEnabled = enabled

		path := filepath.Join(t.TempDir(), "preset.json")
		if err := writePresetJSON(path, params); err != nil {
			t.Fatalf("writePresetJSON: %v", err)
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, ok := fields["resonance_enabled"]; !ok {
			t.Fatalf("resonance_enabled missing from preset written with ResonanceEnabled=%v:\n%s", enabled, raw)
		}

		loaded, err := preset.LoadJSON(path)
		if err != nil {
			t.Fatalf("LoadJSON: %v", err)
		}
		if loaded.ResonanceEnabled != enabled {
			t.Fatalf("resonance_enabled round-trip: got %v want %v", loaded.ResonanceEnabled, enabled)
		}
	}
}

// A checkpoint write must label the profile its best_score was produced with.
// The optimizer's initial, checkpoint and polish writes are separate call
// sites, so the label is derived from the scored metrics rather than passed in
// — an interrupted attack-v1 run that reported no profile would be read back as
// legacy-v1, silently comparing two different objectives.
func TestReportLabelsTheProfileOfTheScoredMetrics(t *testing.T) {
	tests := []struct {
		profile string
		want    string
	}{
		{profile: analysis.ProfileAttackV1, want: analysis.ProfileAttackV1},
		{profile: analysis.ProfileDecayV1, want: analysis.ProfileDecayV1},
		// legacy-v1 is what every existing report already is, so it stays
		// implicit and the key is omitted.
		{profile: analysis.ProfileLegacyV1, want: ""},
		{profile: "", want: ""},
	}
	for _, tc := range tests {
		dir := t.TempDir()
		reportPath := filepath.Join(dir, "out.json.report.json")
		err := writeOutputs(outputRequest{
			outputPreset:  filepath.Join(dir, "out.json"),
			reportPath:    reportPath,
			referencePath: "c4.wav",
			presetPath:    "base.json",
			sampleRate:    48000,
			note:          60,
			velocity:      118,
			releaseAfter:  3.5,
			variant:       "desma",
			defs:          []knobDef{{Name: "output_gain", Min: 0.01, Max: 5.0}},
			best:          candidate{Vals: []float64{1.25}},
			bestScore:     0.4,
			bestMetrics:   analysis.Metrics{Score: 0.4, ScoreProfile: tc.profile},
			bestParams:    piano.NewDefaultParams(),
			// A checkpoint write names the pass but never the profile.
			pass: passAttack,
		})
		if err != nil {
			t.Fatalf("writeOutputs(%q): %v", tc.profile, err)
		}
		raw, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		var rep struct {
			ScoreProfile string `json:"score_profile"`
		}
		if err := json.Unmarshal(raw, &rep); err != nil {
			t.Fatalf("decode report: %v", err)
		}
		if rep.ScoreProfile != tc.want {
			t.Fatalf("metrics profile %q wrote score_profile %q, want %q", tc.profile, rep.ScoreProfile, tc.want)
		}
	}
}

// A written preset must state its bridge coupling explicitly, for exactly the
// reason the resonance test above exists - and this field is the sharper case,
// because zero is the value every fitted preset in assets/presets deliberately
// carries until PLAN.md 17.1 re-voices them.
//
// DefaultBridgeCoupling is NON-ZERO, so with omitempty that deliberate zero
// vanished on write and reloading resurrected it as 0.035. The written preset
// then rendered with double decay the fitter had never evaluated, which is the
// one thing a fit output must never do.
func TestWritePresetJSONStatesBridgeCouplingExplicitly(t *testing.T) {
	for _, bridge := range []float32{0, piano.DefaultBridgeCoupling} {
		params := piano.NewDefaultParams()
		params.BridgeCoupling = bridge

		path := filepath.Join(t.TempDir(), "preset.json")
		if err := writePresetJSON(path, params); err != nil {
			t.Fatalf("writePresetJSON: %v", err)
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, ok := fields["bridge_coupling"]; !ok {
			t.Fatalf("bridge_coupling missing from preset written with BridgeCoupling=%v:\n%s", bridge, raw)
		}

		loaded, err := preset.LoadJSON(path)
		if err != nil {
			t.Fatalf("LoadJSON: %v", err)
		}
		if loaded.BridgeCoupling != bridge {
			t.Fatalf("bridge_coupling round-trip: got %v want %v", loaded.BridgeCoupling, bridge)
		}
	}
}
