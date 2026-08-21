package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
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

	// Optional fields used by multi-note, per-aspect and polish runs. They are
	// omitted when unset so existing readers and existing report files stay
	// valid.
	Notes      []int        `json:"notes,omitempty"`
	PerNote    []noteReport `json:"per_note,omitempty"`
	Aggregate  string       `json:"aggregate,omitempty"`
	Pass       string       `json:"pass,omitempty"`
	PassWindow *windowSpec  `json:"pass_window,omitempty"`
	// ScoreProfile names the analysis weighting profile best_score was
	// produced with. Scores from different profiles are not comparable, so an
	// unlabelled report is a legacy-v1 report.
	ScoreProfile   string `json:"score_profile,omitempty"`
	RendersPerEval int    `json:"renders_per_eval,omitempty"`

	// Polish carries the deterministic polish-stage summary, when it ran.
	Polish *polishSummary `json:"polish,omitempty"`
	// OutputGainMatched is the closed-form multiplier applied to
	// piano.OutputGain after the search finished. It is score-invariant, so it
	// is reported separately instead of being folded into best_knobs.
	OutputGainMatched float64 `json:"output_gain_matched,omitempty"`
}

// noteReport carries the per-note breakdown of a multi-note fit.
type noteReport struct {
	Note          int              `json:"note"`
	ReferencePath string           `json:"reference_path,omitempty"`
	Score         float64          `json:"score"`
	Similarity    float64          `json:"similarity"`
	Metrics       analysis.Metrics `json:"metrics"`
}

// outputRequest bundles everything writeOutputs needs. It replaces a long
// positional parameter list so new optional fields can be added without
// touching every call site.
type outputRequest struct {
	outputIR      string
	outputPreset  string
	reportPath    string
	referencePath string
	presetPath    string
	sampleRate    int
	note          int
	velocity      int
	releaseAfter  float64
	elapsed       float64
	evals         int
	variant       string
	defs          []knobDef
	best          candidate
	bestScore     float64
	bestMetrics   analysis.Metrics
	bestParams    *piano.Params
	bestBodyIR    []float32
	bestRoomIRL   []float32
	bestRoomIRR   []float32
	checkpoints   int
	top           []topCandidate

	// Optional; zero values are omitted from the report.
	notes             []int
	perNote           []noteReport
	aggregate         string
	pass              string
	scoreProfile      string
	passWindow        *windowSpec
	rendersPerEval    int
	polish            *polishSummary
	outputGainMatched float64
}

func writeOutputs(req outputRequest) error {
	p := cloneParams(req.bestParams)

	// Write IR WAVs if outputIR is set and we have IR buffers.
	if req.outputIR != "" && (len(req.bestBodyIR) > 0 || len(req.bestRoomIRL) > 0) {
		ext := filepath.Ext(req.outputIR)
		base := strings.TrimSuffix(req.outputIR, ext)
		bodyIRPath := base + "-body" + ext
		roomIRPath := base + "-room" + ext

		if err := writeMonoWAV(bodyIRPath, req.bestBodyIR, req.sampleRate); err != nil {
			return err
		}
		if err := writeStereoWAV(roomIRPath, req.bestRoomIRL, req.bestRoomIRR, req.sampleRate); err != nil {
			return err
		}

		p.BodyIRWavPath = bodyIRPath
		p.RoomIRWavPath = roomIRPath
		// Clear legacy IR path since we use dual-IR now.
		p.IRWavPath = ""
	}

	if err := writePresetJSON(req.outputPreset, p); err != nil {
		return err
	}

	knobs := make(map[string]float64, len(req.defs))
	for i, d := range req.defs {
		knobs[d.Name] = req.best.Vals[i]
	}

	bestScore := req.bestScore
	if bestScore == 0 {
		bestScore = req.bestMetrics.Score
	}

	// Single-note runs keep the pre-multi-note report shape byte for byte: the
	// per-note breakdown would only restate best_score/best_metrics, and the
	// aggregate of one note is that note. Likewise an unrestricted run records
	// no pass.
	notes, perNote, aggregate, rendersPerEval := req.notes, req.perNote, req.aggregate, req.rendersPerEval
	if len(notes) <= 1 {
		notes, perNote, aggregate, rendersPerEval = nil, nil, "", 0
	}
	pass := req.pass
	if pass == passNone {
		pass = ""
	}
	// A legacy-v1 report is what every existing report already is, so leaving
	// the field out keeps new reports byte-comparable with old ones.
	scoreProfile := req.scoreProfile
	if scoreProfile == analysis.ProfileLegacyV1 {
		scoreProfile = ""
	}

	rep := runReport{
		ReferencePath:   req.referencePath,
		PresetPath:      req.presetPath,
		OutputPreset:    req.outputPreset,
		OutputIR:        req.outputIR,
		SampleRate:      req.sampleRate,
		Note:            req.note,
		Velocity:        req.velocity,
		ReleaseAfterSec: req.releaseAfter,
		DurationSec:     req.elapsed,
		Evaluations:     req.evals,
		MayflyVariant:   req.variant,
		BestScore:       bestScore,
		BestSimilarity:  req.bestMetrics.Similarity,
		BestMetrics:     req.bestMetrics,
		BestKnobs:       knobs,
		CheckpointCount: req.checkpoints,
		TopCandidates:   req.top,
		Notes:           notes,
		PerNote:         perNote,
		Aggregate:       aggregate,
		Pass:            pass,
		PassWindow:      req.passWindow,
		ScoreProfile:    scoreProfile,
		RendersPerEval:  rendersPerEval,

		Polish:            req.polish,
		OutputGainMatched: req.outputGainMatched,
	}

	// analysis.Metrics can legitimately carry NaN (an undefined decay slope,
	// for example) and encoding/json refuses to marshal it, which would turn a
	// finished run into a hard write failure. JSON has no NaN literal, so the
	// closest honest encoding is to record such values as 0.
	sanitizeNonFinite(reflect.ValueOf(&rep).Elem())

	reportPath := req.reportPath
	if reportPath == "" {
		reportPath = req.outputPreset + ".report.json"
	}
	return writeJSON(reportPath, rep)
}

// sanitizeNonFinite replaces every NaN and +/-Inf float reachable from v with
// 0 so the value can be marshalled to JSON.
func sanitizeNonFinite(v reflect.Value) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); (math.IsNaN(f) || math.IsInf(f, 0)) && v.CanSet() {
			v.SetFloat(0)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				continue // unexported, and not marshalled either
			}
			sanitizeNonFinite(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sanitizeNonFinite(v.Index(i))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			sanitizeNonFinite(v.Elem())
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			elem := v.MapIndex(key)
			switch elem.Kind() {
			case reflect.Float32, reflect.Float64:
				if f := elem.Float(); math.IsNaN(f) || math.IsInf(f, 0) {
					v.SetMapIndex(key, reflect.Zero(elem.Type()))
				}
			default:
				// Map values are not addressable; rebuild through a copy.
				tmp := reflect.New(elem.Type()).Elem()
				tmp.Set(elem)
				sanitizeNonFinite(tmp)
				v.SetMapIndex(key, tmp)
			}
		}
	}
}

func writePresetJSON(path string, p *piano.Params) error {
	type noteEntry struct {
		F0             float32 `json:"f0,omitempty"`
		Inharmonicity  float32 `json:"inharmonicity,omitempty"`
		Loss           float32 `json:"loss,omitempty"`
		StrikePosition float32 `json:"strike_position,omitempty"`
	}
	type out struct {
		OutputGain    float32 `json:"output_gain,omitempty"`
		MinNote       int     `json:"min_note"`
		MaxNote       int     `json:"max_note"`
		IRWavPath     string  `json:"ir_wav_path,omitempty"`
		IRWetMix      float32 `json:"ir_wet_mix,omitempty"`
		IRDryMix      float32 `json:"ir_dry_mix,omitempty"`
		IRGain        float32 `json:"ir_gain,omitempty"`
		BodyIRWavPath string  `json:"body_ir_wav_path,omitempty"`
		BodyIRGain    float32 `json:"body_ir_gain,omitempty"`
		BodyDryMix    float32 `json:"body_dry_mix,omitempty"`
		RoomIRWavPath string  `json:"room_ir_wav_path,omitempty"`
		RoomWetMix    float32 `json:"room_wet_mix,omitempty"`
		RoomGain      float32 `json:"room_gain,omitempty"`
		// No omitempty: a preset that deliberately disables resonance must say
		// so, otherwise the key vanishes and reloading falls back to the
		// piano.Params default rather than the value that was written.
		ResonanceEnabled           bool                 `json:"resonance_enabled"`
		ResonanceGain              float32              `json:"resonance_gain,omitempty"`
		ResonancePerNoteFilter     bool                 `json:"resonance_per_note_filter,omitempty"`
		HammerStiffnessScale       float32              `json:"hammer_stiffness_scale,omitempty"`
		HammerExponentScale        float32              `json:"hammer_exponent_scale,omitempty"`
		HammerDampingScale         float32              `json:"hammer_damping_scale,omitempty"`
		HammerInitialVelocityScale float32              `json:"hammer_initial_velocity_scale,omitempty"`
		HammerContactTimeScale     float32              `json:"hammer_contact_time_scale,omitempty"`
		HighFreqDamping            float32              `json:"high_freq_damping,omitempty"`
		UnisonDetuneScale          float32              `json:"unison_detune_scale,omitempty"`
		UnisonCrossfeed            float32              `json:"unison_crossfeed,omitempty"`
		SoftPedalStrikeOffset      float32              `json:"soft_pedal_strike_offset,omitempty"`
		SoftPedalHardness          float32              `json:"soft_pedal_hardness,omitempty"`
		AttackNoiseLevel           float32              `json:"attack_noise_level,omitempty"`
		AttackNoiseDurationMs      float32              `json:"attack_noise_duration_ms,omitempty"`
		AttackNoiseColor           float32              `json:"attack_noise_color,omitempty"`
		StringModel                string               `json:"string_model,omitempty"`
		ModalPartials              int                  `json:"modal_partials,omitempty"`
		ModalGainExponent          float32              `json:"modal_gain_exponent,omitempty"`
		ModalExcitation            float32              `json:"modal_excitation,omitempty"`
		ModalUndampedLoss          float32              `json:"modal_undamped_loss,omitempty"`
		ModalDampedLoss            float32              `json:"modal_damped_loss,omitempty"`
		CouplingEnabled            bool                 `json:"coupling_enabled"`
		CouplingOctaveGain         float32              `json:"coupling_octave_gain,omitempty"`
		CouplingFifthGain          float32              `json:"coupling_fifth_gain,omitempty"`
		CouplingMaxForce           float32              `json:"coupling_max_force,omitempty"`
		CouplingMode               string               `json:"coupling_mode,omitempty"`
		CouplingAmount             float32              `json:"coupling_amount,omitempty"`
		CouplingHarmonicFalloff    float32              `json:"coupling_harmonic_falloff,omitempty"`
		CouplingDetuneSigmaCents   float32              `json:"coupling_detune_sigma_cents,omitempty"`
		CouplingDistanceExponent   float32              `json:"coupling_distance_exponent,omitempty"`
		CouplingMaxNeighbors       int                  `json:"coupling_max_neighbors,omitempty"`
		PerNote                    map[string]noteEntry `json:"per_note,omitempty"`
	}

	o := out{
		OutputGain:                 p.OutputGain,
		MinNote:                    p.MinNote,
		MaxNote:                    p.MaxNote,
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
		HighFreqDamping:            p.HighFreqDamping,
		UnisonDetuneScale:          p.UnisonDetuneScale,
		UnisonCrossfeed:            p.UnisonCrossfeed,
		SoftPedalStrikeOffset:      p.SoftPedalStrikeOffset,
		SoftPedalHardness:          p.SoftPedalHardness,
		AttackNoiseLevel:           p.AttackNoiseLevel,
		AttackNoiseDurationMs:      p.AttackNoiseDurationMs,
		AttackNoiseColor:           p.AttackNoiseColor,
		StringModel:                string(p.StringModel),
		ModalPartials:              p.ModalPartials,
		ModalGainExponent:          p.ModalGainExponent,
		ModalExcitation:            p.ModalExcitation,
		ModalUndampedLoss:          p.ModalUndampedLoss,
		ModalDampedLoss:            p.ModalDampedLoss,
		CouplingEnabled:            p.CouplingEnabled,
		CouplingOctaveGain:         p.CouplingOctaveGain,
		CouplingFifthGain:          p.CouplingFifthGain,
		CouplingMaxForce:           p.CouplingMaxForce,
		CouplingMode:               string(p.CouplingMode),
		CouplingAmount:             p.CouplingAmount,
		CouplingHarmonicFalloff:    p.CouplingHarmonicFalloff,
		CouplingDetuneSigmaCents:   p.CouplingDetuneSigmaCents,
		CouplingDistanceExponent:   p.CouplingDistanceExponent,
		CouplingMaxNeighbors:       p.CouplingMaxNeighbors,
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
