package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
