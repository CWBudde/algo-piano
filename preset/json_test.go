package preset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadJSONAppliesGlobalAndPerNote(t *testing.T) {
	dir := t.TempDir()
	irPath := filepath.Join(dir, "ir.wav")
	if err := os.WriteFile(irPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write ir: %v", err)
	}
	presetPath := filepath.Join(dir, "preset.json")
	content := `{
  "output_gain": 0.9,
  "ir_wav_path": "ir.wav",
  "resonance_enabled": true,
  "resonance_gain": 0.0004,
  "resonance_per_note_filter": false,
  "per_note": {
    "60": {
      "loss": 0.998,
      "inharmonicity": 0.15,
      "strike_position": 0.22
    }
  }
}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}

	p, err := LoadJSON(presetPath)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if p.OutputGain != 0.9 {
		t.Fatalf("output_gain mismatch: %f", p.OutputGain)
	}
	if p.IRWavPath != irPath {
		t.Fatalf("ir path mismatch: got=%q want=%q", p.IRWavPath, irPath)
	}
	if !p.ResonanceEnabled || p.ResonanceGain != 0.0004 || p.ResonancePerNoteFilter {
		t.Fatalf("resonance fields mismatch: %+v", p)
	}
	np := p.PerNote[60]
	if np == nil {
		t.Fatalf("missing note 60 override")
	}
	if np.Loss != 0.998 || np.Inharmonicity != 0.15 || np.StrikePosition != 0.22 {
		t.Fatalf("note params mismatch: %+v", np)
	}
}

func TestLoadJSONRejectsInvalidNoteKey(t *testing.T) {
	dir := t.TempDir()
	presetPath := filepath.Join(dir, "preset.json")
	content := `{"per_note": {"x": {"loss": 0.99}}}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
	if _, err := LoadJSON(presetPath); err == nil {
		t.Fatalf("expected error for invalid note key")
	}
}

func TestLoadJSONRejectsInvalidRanges(t *testing.T) {
	dir := t.TempDir()
	presetPath := filepath.Join(dir, "preset.json")
	content := `{"per_note": {"60": {"loss": 1.2}}}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
	if _, err := LoadJSON(presetPath); err == nil {
		t.Fatalf("expected error for out-of-range loss")
	}
}
