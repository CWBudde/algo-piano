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
  "ir_wet_mix": 0.7,
  "ir_dry_mix": 0.2,
  "ir_gain": 1.1,
  "resonance_enabled": true,
  "resonance_gain": 0.0004,
  "resonance_per_note_filter": false,
  "hammer_stiffness_scale": 1.2,
  "hammer_exponent_scale": 0.95,
  "hammer_damping_scale": 1.1,
  "hammer_initial_velocity_scale": 1.05,
  "hammer_contact_time_scale": 0.9,
  "unison_detune_scale": 0.8,
  "unison_crossfeed": 0.001,
  "string_model": "modal",
  "modal_partials": 10,
  "modal_gain_exponent": 1.4,
  "modal_excitation": 0.85,
  "modal_undamped_loss": 0.9,
  "modal_damped_loss": 1.2,
  "coupling_enabled": true,
  "coupling_octave_gain": 0.00031,
  "coupling_fifth_gain": 0.00012,
  "coupling_max_force": 0.0007,
  "coupling_mode": "physical",
  "coupling_amount": 0.6,
  "coupling_harmonic_falloff": 1.5,
  "coupling_detune_sigma_cents": 19,
  "coupling_distance_exponent": 1.3,
  "coupling_max_neighbors": 12,
  "soft_pedal_strike_offset": 0.1,
  "soft_pedal_hardness": 0.75,
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
	if p.IRWetMix != 0.7 || p.IRDryMix != 0.2 || p.IRGain != 1.1 {
		t.Fatalf("ir mix fields mismatch: %+v", p)
	}
	if !p.ResonanceEnabled || p.ResonanceGain != 0.0004 || p.ResonancePerNoteFilter {
		t.Fatalf("resonance fields mismatch: %+v", p)
	}
	if p.HammerStiffnessScale != 1.2 ||
		p.HammerExponentScale != 0.95 ||
		p.HammerDampingScale != 1.1 ||
		p.HammerInitialVelocityScale != 1.05 ||
		p.HammerContactTimeScale != 0.9 ||
		p.UnisonDetuneScale != 0.8 ||
		p.UnisonCrossfeed != 0.001 ||
		p.StringModel != "modal" ||
		p.ModalPartials != 10 ||
		p.ModalGainExponent != 1.4 ||
		p.ModalExcitation != 0.85 ||
		p.ModalUndampedLoss != 0.9 ||
		p.ModalDampedLoss != 1.2 ||
		!p.CouplingEnabled ||
		p.CouplingOctaveGain != 0.00031 ||
		p.CouplingFifthGain != 0.00012 ||
		p.CouplingMaxForce != 0.0007 ||
		p.CouplingMode != "physical" ||
		p.CouplingAmount != 0.6 ||
		p.CouplingHarmonicFalloff != 1.5 ||
		p.CouplingDetuneSigmaCents != 19 ||
		p.CouplingDistanceExponent != 1.3 ||
		p.CouplingMaxNeighbors != 12 ||
		p.SoftPedalStrikeOffset != 0.1 ||
		p.SoftPedalHardness != 0.75 {
		t.Fatalf("extended tuning fields mismatch: %+v", p)
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

func TestLoadJSONRejectsInvalidExtendedFields(t *testing.T) {
	dir := t.TempDir()
	presetPath := filepath.Join(dir, "preset.json")
	content := `{"ir_wet_mix": -1}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
	if _, err := LoadJSON(presetPath); err == nil {
		t.Fatalf("expected error for invalid ir_wet_mix")
	}
}

func TestLoadJSONRejectsInvalidCouplingMode(t *testing.T) {
	dir := t.TempDir()
	presetPath := filepath.Join(dir, "preset.json")
	content := `{"coupling_mode":"invalid"}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
	if _, err := LoadJSON(presetPath); err == nil {
		t.Fatalf("expected error for invalid coupling_mode")
	}
}

func TestLoadJSONRejectsInvalidStringModel(t *testing.T) {
	dir := t.TempDir()
	presetPath := filepath.Join(dir, "preset.json")
	content := `{"string_model":"invalid"}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
	if _, err := LoadJSON(presetPath); err == nil {
		t.Fatalf("expected error for invalid string_model")
	}
}

func TestLoadJSONRejectsInvalidModalFields(t *testing.T) {
	dir := t.TempDir()
	presetPath := filepath.Join(dir, "preset.json")
	content := `{"modal_partials":0}`
	if err := os.WriteFile(presetPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write preset: %v", err)
	}
	if _, err := LoadJSON(presetPath); err == nil {
		t.Fatalf("expected error for invalid modal_partials")
	}
}
