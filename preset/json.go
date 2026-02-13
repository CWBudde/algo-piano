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
	OutputGain             *float32               `json:"output_gain"`
	IRWavPath              string                 `json:"ir_wav_path"`
	ResonanceEnabled       *bool                  `json:"resonance_enabled"`
	ResonanceGain          *float32               `json:"resonance_gain"`
	ResonancePerNoteFilter *bool                  `json:"resonance_per_note_filter"`
	PerNote                map[string]NoteSetting `json:"per_note"`
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

	if p.IRWavPath != "" && !filepath.IsAbs(p.IRWavPath) {
		base := filepath.Dir(path)
		p.IRWavPath = filepath.Clean(filepath.Join(base, p.IRWavPath))
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
