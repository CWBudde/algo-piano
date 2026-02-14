package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
	fitcommon "github.com/cwbudde/algo-piano/internal/fitcommon"
	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

type knobDef struct {
	Name  string
	Min   float64
	Max   float64
	IsInt bool
}

type candidate struct {
	Vals []float64
}

type topCandidate struct {
	Eval       int                `json:"eval"`
	Score      float64            `json:"score"`
	Similarity float64            `json:"similarity"`
	Knobs      map[string]float64 `json:"knobs"`
}

type runReport struct {
	ReferencePath   string             `json:"reference_path"`
	PresetPath      string             `json:"preset_path"`
	OutputPreset    string             `json:"output_preset"`
	OutputIR        string             `json:"output_ir"`
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
	BestIRKnobs     map[string]float64 `json:"best_ir_knobs"`
	CheckpointCount int                `json:"checkpoint_count"`
	TopCandidates   []topCandidate     `json:"top_candidates,omitempty"`
}

func main() {
	referencePath := flag.String("reference", "reference/c4.wav", "Reference WAV path")
	presetPath := flag.String("preset", "assets/presets/default.json", "Base preset JSON path")
	outputIR := flag.String("output-ir", "assets/ir/fitted/c4-best.wav", "Path to write best synthesized IR WAV")
	outputPreset := flag.String("output-preset", "assets/presets/fitted-c4-ir.json", "Path to write best fitted preset JSON")
	reportPath := flag.String("report", "", "Optional report JSON path (default: <output-preset>.report.json)")
	workDir := flag.String("work-dir", "out/ir-fit", "Directory for temporary IR candidates")
	note := flag.Int("note", 60, "MIDI note to fit")
	velocity := flag.Int("velocity", 118, "MIDI velocity for rendering during fit")
	releaseAfter := flag.Float64("release-after", 3.5, "Seconds before NoteOff for each evaluation render")
	sampleRate := flag.Int("sample-rate", 48000, "Render/analysis sample rate")
	seed := flag.Int64("seed", 1, "Random seed")
	timeBudget := flag.Float64("time-budget", 120.0, "Optimization time budget in seconds")
	maxEvals := flag.Int("max-evals", 10000, "Maximum objective evaluations")
	reportEvery := flag.Int("report-every", 20, "Print progress every N evaluations")
	checkpointEvery := flag.Int("checkpoint-every", 1, "Write checkpoint every N best-score improvements")
	decayDBFS := flag.Float64("decay-dbfs", -90.0, "Auto-stop threshold in dBFS")
	decayHoldBlocks := flag.Int("decay-hold-blocks", 6, "Consecutive below-threshold blocks for stop")
	minDuration := flag.Float64("min-duration", 2.0, "Minimum render duration in seconds")
	maxDuration := flag.Float64("max-duration", 30.0, "Maximum render duration in seconds")
	topK := flag.Int("top-k", 5, "How many top candidates to keep in report")
	resume := flag.Bool("resume", true, "Resume from previous best_ir_knobs report when available")
	resumeReport := flag.String("resume-report", "", "Optional report JSON path to resume from (default: current report path)")
	optimizeIRMix := flag.Bool("optimize-ir-mix", false, "Also optimize ir_wet_mix/ir_dry_mix/ir_gain")
	optimizeJoint := flag.Bool("optimize-joint", false, "Jointly optimize selected non-IR piano knobs with IR knobs")
	workers := flag.String("workers", "1", "Parallel optimization workers running independent Mayfly rounds (number or 'auto')")

	mayflyVariant := flag.String("mayfly-variant", "desma", "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	mayflyPop := flag.Int("mayfly-pop", 10, "Male and female population size per Mayfly run")
	mayflyRoundEvals := flag.Int("mayfly-round-evals", 240, "Target eval budget per Mayfly round")
	flag.Parse()

	if *outputIR == "" {
		die("output-ir must not be empty")
	}
	if *outputPreset == "" {
		die("output-preset must not be empty")
	}
	if *maxEvals < 1 {
		die("max-evals must be >= 1")
	}
	if *timeBudget <= 0 {
		die("time-budget must be > 0")
	}
	if *releaseAfter < 0.05 {
		*releaseAfter = 0.05
	}
	if *reportEvery < 1 {
		*reportEvery = 1
	}
	if *checkpointEvery < 1 {
		*checkpointEvery = 1
	}
	if *mayflyPop < 2 {
		*mayflyPop = 2
	}
	if *mayflyRoundEvals < *mayflyPop*2 {
		*mayflyRoundEvals = *mayflyPop * 2
	}
	if *topK < 1 {
		*topK = 1
	}
	parsedWorkers, err := parseWorkersFlag(*workers)
	if err != nil {
		die("invalid workers value: %v", err)
	}

	baseParams, err := preset.LoadJSON(*presetPath)
	if err != nil {
		die("failed to load preset: %v", err)
	}
	if baseParams.IRWavPath == "" {
		baseParams.IRWavPath = piano.DefaultIRWavPath
	}

	ref, refSR, err := readWAVMono(*referencePath)
	if err != nil {
		die("failed to read reference: %v", err)
	}
	ref, err = resampleIfNeeded(ref, refSR, *sampleRate)
	if err != nil {
		die("failed to resample reference: %v", err)
	}

	defs, initCand := initCandidate(
		baseParams,
		*sampleRate,
		*note,
		*velocity,
		*releaseAfter,
		*optimizeIRMix,
		*optimizeJoint,
	)
	if *resume {
		resumePath := *resumeReport
		if resumePath == "" {
			if *reportPath != "" {
				resumePath = *reportPath
			} else {
				resumePath = *outputPreset + ".report.json"
			}
		}
		if resumed, ok, err := loadCandidateFromReport(resumePath, defs, initCand); err != nil {
			fmt.Fprintf(os.Stderr, "resume skipped (%s): %v\n", resumePath, err)
		} else if ok {
			initCand = resumed
			fmt.Printf("Resumed candidate from %s\n", resumePath)
		}
	}

	cfg := &optimizationConfig{
		reference:        ref,
		baseParams:       baseParams,
		defs:             defs,
		initCandidate:    initCand,
		note:             *note,
		baseVelocity:     *velocity,
		baseReleaseAfter: *releaseAfter,
		sampleRate:       *sampleRate,
		seed:             *seed,
		timeBudget:       *timeBudget,
		maxEvals:         *maxEvals,
		reportEvery:      *reportEvery,
		checkpointEvery:  *checkpointEvery,
		decayDBFS:        *decayDBFS,
		decayHoldBlocks:  *decayHoldBlocks,
		minDuration:      *minDuration,
		maxDuration:      *maxDuration,
		mayflyVariant:    *mayflyVariant,
		mayflyPop:        *mayflyPop,
		mayflyRoundEvals: *mayflyRoundEvals,
		workers:          parsedWorkers,
		topK:             *topK,
		workDir:          *workDir,
		outputIR:         *outputIR,
		outputPreset:     *outputPreset,
		reportPath:       *reportPath,
		referencePath:    *referencePath,
		presetPath:       *presetPath,
	}

	result, err := runOptimization(cfg)
	if err != nil {
		die("optimization failed: %v", err)
	}

	if err := writeOutputs(
		*outputIR,
		*outputPreset,
		*reportPath,
		*referencePath,
		*presetPath,
		*sampleRate,
		*note,
		result.bestVelocity,
		result.bestReleaseAfter,
		result.elapsed,
		result.evals,
		strings.ToLower(*mayflyVariant),
		defs,
		result.best,
		result.bestMetrics,
		result.bestParams,
		result.bestIRL,
		result.bestIRR,
		result.checkpoints,
		result.top,
	); err != nil {
		die("failed to write outputs: %v", err)
	}

	fmt.Printf("Done evals=%d elapsed=%.1fs best_score=%.4f best_similarity=%.2f%% variant=%s\n", result.evals, result.elapsed, result.bestMetrics.Score, result.bestMetrics.Similarity*100.0, strings.ToLower(*mayflyVariant))
}

func parseWorkersFlag(raw string) (int, error) {
	return fitcommon.ParseWorkers(raw)
}

func initCandidate(
	base *piano.Params,
	sampleRate int,
	note int,
	baseVelocity int,
	baseReleaseAfter float64,
	optimizeIRMix bool,
	optimizeJoint bool,
) ([]knobDef, candidate) {
	cfg := irsynth.DefaultConfig()
	cfg.SampleRate = sampleRate

	defs := make([]knobDef, 0, 24)
	vals := make([]float64, 0, 24)
	addKnob := func(def knobDef, val float64) {
		for _, d := range defs {
			if d.Name == def.Name {
				return
			}
		}
		defs = append(defs, def)
		vals = append(vals, val)
	}

	addKnob(knobDef{Name: "modes", Min: 32, Max: 256, IsInt: true}, float64(cfg.Modes))
	addKnob(knobDef{Name: "brightness", Min: 0.5, Max: 2.5}, cfg.Brightness)
	addKnob(knobDef{Name: "stereo_width", Min: 0.0, Max: 1.0}, cfg.StereoWidth)
	addKnob(knobDef{Name: "direct", Min: 0.1, Max: 1.2}, cfg.DirectLevel)
	addKnob(knobDef{Name: "early", Min: 0, Max: 48, IsInt: true}, float64(cfg.EarlyCount))
	addKnob(knobDef{Name: "late", Min: 0.0, Max: 0.12}, cfg.LateLevel)
	addKnob(knobDef{Name: "low_decay", Min: 0.6, Max: 5.0}, cfg.LowDecayS)
	addKnob(knobDef{Name: "high_decay", Min: 0.1, Max: 1.5}, cfg.HighDecayS)

	if optimizeIRMix || optimizeJoint {
		addKnob(knobDef{Name: "ir_wet_mix", Min: 0.2, Max: 1.6}, float64(base.IRWetMix))
		addKnob(knobDef{Name: "ir_dry_mix", Min: 0.0, Max: 0.8}, float64(base.IRDryMix))
		addKnob(knobDef{Name: "ir_gain", Min: 0.4, Max: 2.2}, float64(base.IRGain))
	}

	if optimizeJoint {
		np := base.PerNote[note]
		if np == nil {
			np = &piano.NoteParams{Loss: 0.9990, Inharmonicity: 0.12, StrikePosition: 0.18}
		}
		addKnob(knobDef{Name: "output_gain", Min: 0.4, Max: 1.8}, float64(base.OutputGain))
		addKnob(knobDef{Name: "hammer_stiffness_scale", Min: 0.6, Max: 1.8}, float64(base.HammerStiffnessScale))
		addKnob(knobDef{Name: "hammer_exponent_scale", Min: 0.8, Max: 1.2}, float64(base.HammerExponentScale))
		addKnob(knobDef{Name: "hammer_damping_scale", Min: 0.6, Max: 1.8}, float64(base.HammerDampingScale))
		addKnob(knobDef{Name: "hammer_initial_velocity_scale", Min: 0.7, Max: 1.4}, float64(base.HammerInitialVelocityScale))
		addKnob(knobDef{Name: "hammer_contact_time_scale", Min: 0.7, Max: 1.6}, float64(base.HammerContactTimeScale))
		addKnob(knobDef{Name: "unison_detune_scale", Min: 0.0, Max: 2.0}, float64(base.UnisonDetuneScale))
		addKnob(knobDef{Name: "unison_crossfeed", Min: 0.0, Max: 0.005}, float64(base.UnisonCrossfeed))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.loss", note), Min: 0.985, Max: 0.99995}, float64(np.Loss))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.inharmonicity", note), Min: 0.0, Max: 0.6}, float64(np.Inharmonicity))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.strike_position", note), Min: 0.08, Max: 0.45}, float64(np.StrikePosition))
		addKnob(knobDef{Name: "render.velocity", Min: 40, Max: 127, IsInt: true}, float64(baseVelocity))
		addKnob(knobDef{Name: "render.release_after", Min: 0.2, Max: 3.5}, baseReleaseAfter)
	}

	for i := range vals {
		vals[i] = clamp(vals[i], defs[i].Min, defs[i].Max)
		if defs[i].IsInt {
			vals[i] = math.Round(vals[i])
		}
	}
	return defs, candidate{Vals: vals}
}

func applyCandidate(
	base *piano.Params,
	sampleRate int,
	note int,
	baseVelocity int,
	baseReleaseAfter float64,
	defs []knobDef,
	c candidate,
) (irsynth.Config, *piano.Params, int, float64) {
	cfg := irsynth.DefaultConfig()
	cfg.SampleRate = sampleRate
	params := cloneParams(base)
	if params.PerNote == nil {
		params.PerNote = make(map[int]*piano.NoteParams)
	}
	np := params.PerNote[note]
	if np == nil {
		np = &piano.NoteParams{}
		params.PerNote[note] = np
	}
	velocity := baseVelocity
	releaseAfter := baseReleaseAfter

	for i, def := range defs {
		v := c.Vals[i]
		switch def.Name {
		case "modes":
			cfg.Modes = int(math.Round(v))
		case "brightness":
			cfg.Brightness = v
		case "stereo_width":
			cfg.StereoWidth = v
		case "direct":
			cfg.DirectLevel = v
		case "early":
			cfg.EarlyCount = int(math.Round(v))
		case "late":
			cfg.LateLevel = v
		case "low_decay":
			cfg.LowDecayS = v
		case "high_decay":
			cfg.HighDecayS = v
		case "ir_wet_mix":
			params.IRWetMix = float32(v)
		case "ir_dry_mix":
			params.IRDryMix = float32(v)
		case "ir_gain":
			params.IRGain = float32(v)
		case "output_gain":
			params.OutputGain = float32(v)
		case "hammer_stiffness_scale":
			params.HammerStiffnessScale = float32(v)
		case "hammer_exponent_scale":
			params.HammerExponentScale = float32(v)
		case "hammer_damping_scale":
			params.HammerDampingScale = float32(v)
		case "hammer_initial_velocity_scale":
			params.HammerInitialVelocityScale = float32(v)
		case "hammer_contact_time_scale":
			params.HammerContactTimeScale = float32(v)
		case "unison_detune_scale":
			params.UnisonDetuneScale = float32(v)
		case "unison_crossfeed":
			params.UnisonCrossfeed = float32(v)
		case fmt.Sprintf("per_note.%d.loss", note):
			np.Loss = float32(v)
		case fmt.Sprintf("per_note.%d.inharmonicity", note):
			np.Inharmonicity = float32(v)
		case fmt.Sprintf("per_note.%d.strike_position", note):
			np.StrikePosition = float32(v)
		case "render.velocity":
			velocity = int(math.Round(v))
		case "render.release_after":
			releaseAfter = v
		}
	}

	if cfg.Modes < 1 {
		cfg.Modes = 1
	}
	if cfg.EarlyCount < 0 {
		cfg.EarlyCount = 0
	}
	if velocity < 1 {
		velocity = 1
	}
	if velocity > 127 {
		velocity = 127
	}
	if releaseAfter < 0.05 {
		releaseAfter = 0.05
	}
	return cfg, params, velocity, releaseAfter
}

func updateTopCandidates(top []topCandidate, topK int, eval int, metrics analysis.Metrics, defs []knobDef, cand candidate) []topCandidate {
	entry := topCandidate{
		Eval:       eval,
		Score:      metrics.Score,
		Similarity: metrics.Similarity,
		Knobs:      make(map[string]float64, len(defs)),
	}
	for i, d := range defs {
		entry.Knobs[d.Name] = cand.Vals[i]
	}
	top = append(top, entry)
	sort.Slice(top, func(i, j int) bool {
		if top[i].Score == top[j].Score {
			return top[i].Eval < top[j].Eval
		}
		return top[i].Score < top[j].Score
	})
	if len(top) > topK {
		top = top[:topK]
	}
	return top
}

func loadCandidateFromReport(path string, defs []knobDef, fallback candidate) (candidate, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fallback, false, nil
		}
		return fallback, false, err
	}
	var rep runReport
	if err := json.Unmarshal(b, &rep); err != nil {
		return fallback, false, err
	}
	if len(rep.BestIRKnobs) == 0 {
		return fallback, false, nil
	}

	vals := make([]float64, len(fallback.Vals))
	copy(vals, fallback.Vals)
	updated := false
	for i, d := range defs {
		if v, ok := rep.BestIRKnobs[d.Name]; ok {
			vals[i] = clamp(v, d.Min, d.Max)
			if d.IsInt {
				vals[i] = math.Round(vals[i])
			}
			updated = true
		}
	}
	if !updated {
		return fallback, false, nil
	}
	return candidate{Vals: vals}, true, nil
}

func fromNormalized(pos []float64, defs []knobDef) candidate {
	vals := make([]float64, len(defs))
	for i := range defs {
		x := 0.0
		if i < len(pos) {
			x = clamp(pos[i], 0, 1)
		}
		v := defs[i].Min + x*(defs[i].Max-defs[i].Min)
		if defs[i].IsInt {
			v = math.Round(v)
		}
		vals[i] = v
	}
	return candidate{Vals: vals}
}
