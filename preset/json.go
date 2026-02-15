package preset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cwbudde/algo-piano/piano"
)

// File is the JSON schema for piano presets.
type File struct {
	OutputGain *float32 `json:"output_gain"`
	// Legacy single-IR fields.
	IRWavPath string   `json:"ir_wav_path"`
	IRWetMix  *float32 `json:"ir_wet_mix"`
	IRDryMix  *float32 `json:"ir_dry_mix"`
	IRGain    *float32 `json:"ir_gain"`
	// Dual-IR fields.
	BodyIRWavPath string   `json:"body_ir_wav_path,omitempty"`
	BodyIRGain    *float32 `json:"body_ir_gain,omitempty"`
	BodyDryMix    *float32 `json:"body_dry_mix,omitempty"`
	RoomIRWavPath string   `json:"room_ir_wav_path,omitempty"`
	RoomWetMix    *float32 `json:"room_wet_mix,omitempty"`
	RoomGain      *float32 `json:"room_gain,omitempty"`

	ResonanceEnabled           *bool                  `json:"resonance_enabled"`
	ResonanceGain              *float32               `json:"resonance_gain"`
	ResonancePerNoteFilter     *bool                  `json:"resonance_per_note_filter"`
	HammerStiffnessScale       *float32               `json:"hammer_stiffness_scale"`
	HammerExponentScale        *float32               `json:"hammer_exponent_scale"`
	HammerDampingScale         *float32               `json:"hammer_damping_scale"`
	HammerInitialVelocityScale *float32               `json:"hammer_initial_velocity_scale"`
	HammerContactTimeScale     *float32               `json:"hammer_contact_time_scale"`
	UnisonDetuneScale          *float32               `json:"unison_detune_scale"`
	UnisonCrossfeed            *float32               `json:"unison_crossfeed"`
	CouplingEnabled            *bool                  `json:"coupling_enabled"`
	CouplingOctaveGain         *float32               `json:"coupling_octave_gain"`
	CouplingFifthGain          *float32               `json:"coupling_fifth_gain"`
	CouplingMaxForce           *float32               `json:"coupling_max_force"`
	SoftPedalStrikeOffset      *float32               `json:"soft_pedal_strike_offset"`
	SoftPedalHardness          *float32               `json:"soft_pedal_hardness"`
	PerNote                    map[string]NoteSetting `json:"per_note"`
}

// NoteSetting is a partial note override entry in a preset file.
type NoteSetting struct {
	F0             *float32 `json:"f0"`
	Inharmonicity  *float32 `json:"inharmonicity"`
	Loss           *float32 `json:"loss"`
	StrikePosition *float32 `json:"strike_position"`
}

// LoadJSON loads a preset JSON file and applies it on top of default params.
func LoadJSON(path string) (*piano.Params, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}

	p := piano.NewDefaultParams()
	if err := ApplyFile(p, &f); err != nil {
		return nil, err
	}

	base := filepath.Dir(path)
	if p.IRWavPath != "" && !filepath.IsAbs(p.IRWavPath) {
		p.IRWavPath = filepath.Clean(filepath.Join(base, p.IRWavPath))
	}
	if p.BodyIRWavPath != "" && !filepath.IsAbs(p.BodyIRWavPath) {
		p.BodyIRWavPath = filepath.Clean(filepath.Join(base, p.BodyIRWavPath))
	}
	if p.RoomIRWavPath != "" && !filepath.IsAbs(p.RoomIRWavPath) {
		p.RoomIRWavPath = filepath.Clean(filepath.Join(base, p.RoomIRWavPath))
	}
	return p, nil
}

// ApplyFile applies a parsed preset file onto an existing params object.
func ApplyFile(dst *piano.Params, f *File) error {
	if dst == nil {
		return fmt.Errorf("nil destination params")
	}
	if f == nil {
		return nil
	}

	if f.OutputGain != nil {
		if *f.OutputGain <= 0 {
			return fmt.Errorf("output_gain must be > 0")
		}
		dst.OutputGain = *f.OutputGain
	}
	if f.IRWavPath != "" {
		dst.IRWavPath = strings.TrimSpace(f.IRWavPath)
	}
	if f.IRWetMix != nil {
		if *f.IRWetMix < 0 {
			return fmt.Errorf("ir_wet_mix must be >= 0")
		}
		dst.IRWetMix = *f.IRWetMix
	}
	if f.IRDryMix != nil {
		if *f.IRDryMix < 0 {
			return fmt.Errorf("ir_dry_mix must be >= 0")
		}
		dst.IRDryMix = *f.IRDryMix
	}
	if f.IRGain != nil {
		if *f.IRGain <= 0 {
			return fmt.Errorf("ir_gain must be > 0")
		}
		dst.IRGain = *f.IRGain
	}
	// Dual-IR fields.
	if f.BodyIRWavPath != "" {
		dst.BodyIRWavPath = strings.TrimSpace(f.BodyIRWavPath)
	}
	if f.BodyIRGain != nil {
		if *f.BodyIRGain <= 0 {
			return fmt.Errorf("body_ir_gain must be > 0")
		}
		dst.BodyIRGain = *f.BodyIRGain
	}
	if f.BodyDryMix != nil {
		if *f.BodyDryMix < 0 {
			return fmt.Errorf("body_dry_mix must be >= 0")
		}
		dst.BodyDryMix = *f.BodyDryMix
	}
	if f.RoomIRWavPath != "" {
		dst.RoomIRWavPath = strings.TrimSpace(f.RoomIRWavPath)
	}
	if f.RoomWetMix != nil {
		if *f.RoomWetMix < 0 {
			return fmt.Errorf("room_wet_mix must be >= 0")
		}
		dst.RoomWetMix = *f.RoomWetMix
	}
	if f.RoomGain != nil {
		if *f.RoomGain <= 0 {
			return fmt.Errorf("room_gain must be > 0")
		}
		dst.RoomGain = *f.RoomGain
	}
	if f.ResonanceEnabled != nil {
		dst.ResonanceEnabled = *f.ResonanceEnabled
	}
	if f.ResonanceGain != nil {
		if *f.ResonanceGain < 0 {
			return fmt.Errorf("resonance_gain must be >= 0")
		}
		dst.ResonanceGain = *f.ResonanceGain
	}
	if f.ResonancePerNoteFilter != nil {
		dst.ResonancePerNoteFilter = *f.ResonancePerNoteFilter
	}
	if f.HammerStiffnessScale != nil {
		if *f.HammerStiffnessScale <= 0 {
			return fmt.Errorf("hammer_stiffness_scale must be > 0")
		}
		dst.HammerStiffnessScale = *f.HammerStiffnessScale
	}
	if f.HammerExponentScale != nil {
		if *f.HammerExponentScale <= 0 {
			return fmt.Errorf("hammer_exponent_scale must be > 0")
		}
		dst.HammerExponentScale = *f.HammerExponentScale
	}
	if f.HammerDampingScale != nil {
		if *f.HammerDampingScale <= 0 {
			return fmt.Errorf("hammer_damping_scale must be > 0")
		}
		dst.HammerDampingScale = *f.HammerDampingScale
	}
	if f.HammerInitialVelocityScale != nil {
		if *f.HammerInitialVelocityScale <= 0 {
			return fmt.Errorf("hammer_initial_velocity_scale must be > 0")
		}
		dst.HammerInitialVelocityScale = *f.HammerInitialVelocityScale
	}
	if f.HammerContactTimeScale != nil {
		if *f.HammerContactTimeScale <= 0 {
			return fmt.Errorf("hammer_contact_time_scale must be > 0")
		}
		dst.HammerContactTimeScale = *f.HammerContactTimeScale
	}
	if f.UnisonDetuneScale != nil {
		if *f.UnisonDetuneScale < 0 {
			return fmt.Errorf("unison_detune_scale must be >= 0")
		}
		dst.UnisonDetuneScale = *f.UnisonDetuneScale
	}
	if f.UnisonCrossfeed != nil {
		if *f.UnisonCrossfeed < 0 {
			return fmt.Errorf("unison_crossfeed must be >= 0")
		}
		dst.UnisonCrossfeed = *f.UnisonCrossfeed
	}
	if f.CouplingEnabled != nil {
		dst.CouplingEnabled = *f.CouplingEnabled
	}
	if f.CouplingOctaveGain != nil {
		if *f.CouplingOctaveGain < 0 {
			return fmt.Errorf("coupling_octave_gain must be >= 0")
		}
		dst.CouplingOctaveGain = *f.CouplingOctaveGain
	}
	if f.CouplingFifthGain != nil {
		if *f.CouplingFifthGain < 0 {
			return fmt.Errorf("coupling_fifth_gain must be >= 0")
		}
		dst.CouplingFifthGain = *f.CouplingFifthGain
	}
	if f.CouplingMaxForce != nil {
		if *f.CouplingMaxForce <= 0 {
			return fmt.Errorf("coupling_max_force must be > 0")
		}
		dst.CouplingMaxForce = *f.CouplingMaxForce
	}
	if f.SoftPedalStrikeOffset != nil {
		if *f.SoftPedalStrikeOffset < 0 {
			return fmt.Errorf("soft_pedal_strike_offset must be >= 0")
		}
		dst.SoftPedalStrikeOffset = *f.SoftPedalStrikeOffset
	}
	if f.SoftPedalHardness != nil {
		if *f.SoftPedalHardness <= 0 {
			return fmt.Errorf("soft_pedal_hardness must be > 0")
		}
		dst.SoftPedalHardness = *f.SoftPedalHardness
	}

	if len(f.PerNote) == 0 {
		return nil
	}
	if dst.PerNote == nil {
		dst.PerNote = make(map[int]*piano.NoteParams)
	}

	keys := make([]string, 0, len(f.PerNote))
	for k := range f.PerNote {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		note, err := strconv.Atoi(k)
		if err != nil || note < 0 || note > 127 {
			return fmt.Errorf("invalid per_note key %q (expected 0..127)", k)
		}
		override := f.PerNote[k]
		np, ok := dst.PerNote[note]
		if !ok || np == nil {
			np = &piano.NoteParams{}
			dst.PerNote[note] = np
		}
		if override.F0 != nil {
			if *override.F0 <= 0 {
				return fmt.Errorf("per_note[%d].f0 must be > 0", note)
			}
			np.F0 = *override.F0
		}
		if override.Inharmonicity != nil {
			if *override.Inharmonicity < 0 {
				return fmt.Errorf("per_note[%d].inharmonicity must be >= 0", note)
			}
			np.Inharmonicity = *override.Inharmonicity
		}
		if override.Loss != nil {
			if *override.Loss <= 0 || *override.Loss > 1 {
				return fmt.Errorf("per_note[%d].loss must be in (0,1]", note)
			}
			np.Loss = *override.Loss
		}
		if override.StrikePosition != nil {
			if *override.StrikePosition <= 0 || *override.StrikePosition >= 1 {
				return fmt.Errorf("per_note[%d].strike_position must be in (0,1)", note)
			}
			np.StrikePosition = *override.StrikePosition
		}
	}
	return nil
}
