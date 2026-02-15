package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
)

type runReport struct {
	ReferencePath   string             `json:"reference_path"`
	PresetPath      string             `json:"preset_path"`
	OutputPreset    string             `json:"output_preset"`
	OutputIR        string             `json:"output_ir,omitempty"`
	SampleRate      int                `json:"sample_rate"`
	Note            int                `json:"note"`
	Velocity        int                `json:"velocity"`
	ReleaseAfterSec float64            `json:"release_after_seconds"`
	DurationSec     float64            `json:"elapsed_seconds"`
	Evaluations     int                `json:"evaluations"`
	MayflyVariant   string             `json:"mayfly_variant"`
	BestScore       float64            `json:"best_score"`
	BestSimilarity  float64            `json:"best_similarity"`
	BestMetrics     analysis.Metrics   `json:"best_metrics"`
	BestKnobs       map[string]float64 `json:"best_knobs"`
	CheckpointCount int                `json:"checkpoint_count"`
	TopCandidates   []topCandidate     `json:"top_candidates,omitempty"`
}

func writeOutputs(
	outputIR string,
	outputPreset string,
	reportPath string,
	referencePath string,
	presetPath string,
	sampleRate int,
	note int,
	velocity int,
	releaseAfter float64,
	elapsed float64,
	evals int,
	variant string,
	defs []knobDef,
	best candidate,
	bestM analysis.Metrics,
	bestParams *piano.Params,
	bestBodyIR []float32,
	bestRoomIRL []float32,
	bestRoomIRR []float32,
	checkpoints int,
	top []topCandidate,
) error {
	p := cloneParams(bestParams)

	// Write IR WAVs if outputIR is set and we have IR buffers.
	if outputIR != "" && (len(bestBodyIR) > 0 || len(bestRoomIRL) > 0) {
		ext := filepath.Ext(outputIR)
		base := strings.TrimSuffix(outputIR, ext)
		bodyIRPath := base + "-body" + ext
		roomIRPath := base + "-room" + ext

		if err := writeMonoWAV(bodyIRPath, bestBodyIR, sampleRate); err != nil {
			return err
		}
		if err := writeStereoWAV(roomIRPath, bestRoomIRL, bestRoomIRR, sampleRate); err != nil {
			return err
		}

		p.BodyIRWavPath = presetIRPath(outputPreset, bodyIRPath)
		p.RoomIRWavPath = presetIRPath(outputPreset, roomIRPath)
		// Clear legacy IR path since we use dual-IR now.
		p.IRWavPath = ""
	}

	if err := writePresetJSON(outputPreset, p); err != nil {
		return err
	}

	knobs := make(map[string]float64, len(defs))
	for i, d := range defs {
		knobs[d.Name] = best.Vals[i]
	}

	rep := runReport{
		ReferencePath:   referencePath,
		PresetPath:      presetPath,
		OutputPreset:    outputPreset,
		OutputIR:        outputIR,
		SampleRate:      sampleRate,
		Note:            note,
		Velocity:        velocity,
		ReleaseAfterSec: releaseAfter,
		DurationSec:     elapsed,
		Evaluations:     evals,
		MayflyVariant:   variant,
		BestScore:       bestM.Score,
		BestSimilarity:  bestM.Similarity,
		BestMetrics:     bestM,
		BestKnobs:       knobs,
		CheckpointCount: checkpoints,
		TopCandidates:   top,
	}

	if reportPath == "" {
		reportPath = outputPreset + ".report.json"
	}
	return writeJSON(reportPath, rep)
}

func writePresetJSON(path string, p *piano.Params) error {
	type noteEntry struct {
		F0             float32 `json:"f0,omitempty"`
		Inharmonicity  float32 `json:"inharmonicity,omitempty"`
		Loss           float32 `json:"loss,omitempty"`
		StrikePosition float32 `json:"strike_position,omitempty"`
	}
	type out struct {
		OutputGain                 float32              `json:"output_gain,omitempty"`
		IRWavPath                  string               `json:"ir_wav_path,omitempty"`
		IRWetMix                   float32              `json:"ir_wet_mix,omitempty"`
		IRDryMix                   float32              `json:"ir_dry_mix,omitempty"`
		IRGain                     float32              `json:"ir_gain,omitempty"`
		BodyIRWavPath              string               `json:"body_ir_wav_path,omitempty"`
		BodyIRGain                 float32              `json:"body_ir_gain,omitempty"`
		BodyDryMix                 float32              `json:"body_dry_mix,omitempty"`
		RoomIRWavPath              string               `json:"room_ir_wav_path,omitempty"`
		RoomWetMix                 float32              `json:"room_wet_mix,omitempty"`
		RoomGain                   float32              `json:"room_gain,omitempty"`
		ResonanceEnabled           bool                 `json:"resonance_enabled,omitempty"`
		ResonanceGain              float32              `json:"resonance_gain,omitempty"`
		ResonancePerNoteFilter     bool                 `json:"resonance_per_note_filter,omitempty"`
		HammerStiffnessScale       float32              `json:"hammer_stiffness_scale,omitempty"`
		HammerExponentScale        float32              `json:"hammer_exponent_scale,omitempty"`
		HammerDampingScale         float32              `json:"hammer_damping_scale,omitempty"`
		HammerInitialVelocityScale float32              `json:"hammer_initial_velocity_scale,omitempty"`
		HammerContactTimeScale     float32              `json:"hammer_contact_time_scale,omitempty"`
		UnisonDetuneScale          float32              `json:"unison_detune_scale,omitempty"`
		UnisonCrossfeed            float32              `json:"unison_crossfeed,omitempty"`
		SoftPedalStrikeOffset      float32              `json:"soft_pedal_strike_offset,omitempty"`
		SoftPedalHardness          float32              `json:"soft_pedal_hardness,omitempty"`
		PerNote                    map[string]noteEntry `json:"per_note,omitempty"`
	}

	o := out{
		OutputGain:                 p.OutputGain,
		IRWavPath:                  presetIRPath(path, p.IRWavPath),
		IRWetMix:                   p.IRWetMix,
		IRDryMix:                   p.IRDryMix,
		IRGain:                     p.IRGain,
		BodyIRWavPath:              presetIRPath(path, p.BodyIRWavPath),
		BodyIRGain:                 p.BodyIRGain,
		BodyDryMix:                 p.BodyDryMix,
		RoomIRWavPath:              presetIRPath(path, p.RoomIRWavPath),
		RoomWetMix:                 p.RoomWetMix,
		RoomGain:                   p.RoomGain,
		ResonanceEnabled:           p.ResonanceEnabled,
		ResonanceGain:              p.ResonanceGain,
		ResonancePerNoteFilter:     p.ResonancePerNoteFilter,
		HammerStiffnessScale:       p.HammerStiffnessScale,
		HammerExponentScale:        p.HammerExponentScale,
		HammerDampingScale:         p.HammerDampingScale,
		HammerInitialVelocityScale: p.HammerInitialVelocityScale,
		HammerContactTimeScale:     p.HammerContactTimeScale,
		UnisonDetuneScale:          p.UnisonDetuneScale,
		UnisonCrossfeed:            p.UnisonCrossfeed,
		SoftPedalStrikeOffset:      p.SoftPedalStrikeOffset,
		SoftPedalHardness:          p.SoftPedalHardness,
		PerNote:                    map[string]noteEntry{},
	}
	keys := make([]int, 0, len(p.PerNote))
	for k := range p.PerNote {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		np := p.PerNote[k]
		if np == nil {
			continue
		}
		o.PerNote[strconv.Itoa(k)] = noteEntry{
			F0:             np.F0,
			Inharmonicity:  np.Inharmonicity,
			Loss:           np.Loss,
			StrikePosition: np.StrikePosition,
		}
	}
	return writeJSON(path, o)
}

func presetIRPath(presetPath string, irPath string) string {
	irPath = strings.TrimSpace(irPath)
	if irPath == "" {
		return ""
	}

	presetDir := filepath.Dir(presetPath)
	presetDirAbs, err := filepath.Abs(presetDir)
	if err != nil {
		return irPath
	}

	irAbs := irPath
	if !filepath.IsAbs(irAbs) {
		irAbs, err = filepath.Abs(irAbs)
		if err != nil {
			return irPath
		}
	}

	rel, err := filepath.Rel(presetDirAbs, irAbs)
	if err != nil {
		return irPath
	}
	return filepath.ToSlash(rel)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
