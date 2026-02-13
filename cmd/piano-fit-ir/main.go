package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
	"github.com/cwbudde/mayfly"
	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
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

	if err := os.MkdirAll(*workDir, 0o755); err != nil {
		die("failed to create work-dir: %v", err)
	}
	scratchIRPath := filepath.Join(*workDir, "candidate_ir.wav")

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

	evaluate := func(c candidate) (analysis.Metrics, *piano.Params, []float32, []float32, int, float64, error) {
		cfg, params, evalVelocity, evalReleaseAfter := applyCandidate(
			baseParams,
			*sampleRate,
			*note,
			*velocity,
			*releaseAfter,
			defs,
			c,
		)
		left, right, err := irsynth.GenerateStereo(cfg)
		if err != nil {
			return analysis.Metrics{}, nil, nil, nil, 0, 0, err
		}
		if err := writeStereoWAV(scratchIRPath, left, right, cfg.SampleRate); err != nil {
			return analysis.Metrics{}, nil, nil, nil, 0, 0, err
		}
		params.IRWavPath = scratchIRPath
		mono, _, err := renderCandidateFromParams(
			params,
			*note,
			evalVelocity,
			*sampleRate,
			*decayDBFS,
			*decayHoldBlocks,
			*minDuration,
			*maxDuration,
			evalReleaseAfter,
		)
		if err != nil {
			return analysis.Metrics{}, nil, nil, nil, 0, 0, err
		}
		return analysis.Compare(ref, mono, *sampleRate), params, left, right, evalVelocity, evalReleaseAfter, nil
	}

	start := time.Now()
	deadline := start.Add(time.Duration(*timeBudget * float64(time.Second)))
	evals := 0
	bestImproves := 0
	checkpoints := 0
	top := make([]topCandidate, 0, *topK)

	best := initCand
	bestM, bestParams, bestIRL, bestIRR, bestVelocity, bestReleaseAfter, err := evaluate(best)
	if err != nil {
		die("initial evaluation failed: %v", err)
	}
	evals++
	top = updateTopCandidates(top, *topK, evals, bestM, defs, best)
	fmt.Printf("Start score=%.4f similarity=%.2f%%\n", bestM.Score, bestM.Similarity*100.0)

	if _, err := os.Stat(*outputIR); err != nil && errors.Is(err, os.ErrNotExist) {
		if err := writeOutputs(
			*outputIR,
			*outputPreset,
			*reportPath,
			*referencePath,
			*presetPath,
			*sampleRate,
			*note,
			bestVelocity,
			bestReleaseAfter,
			time.Since(start).Seconds(),
			evals,
			strings.ToLower(*mayflyVariant),
			defs,
			best,
			bestM,
			bestParams,
			bestIRL,
			bestIRR,
			checkpoints,
			top,
		); err != nil {
			fmt.Fprintf(os.Stderr, "initial write failed: %v\n", err)
		}
	}

	round := 0
	for evals < *maxEvals && time.Now().Before(deadline) {
		round++
		remaining := *maxEvals - evals
		budget := minInt(*mayflyRoundEvals, remaining)
		iters := maxInt(1, budget/(2*(*mayflyPop)))

		cfg, err := newMayflyConfig(strings.ToLower(*mayflyVariant), *mayflyPop, len(defs), iters)
		if err != nil {
			die("invalid mayfly variant: %v", err)
		}
		cfg.Rand = rand.New(rand.NewSource(*seed + int64(round)*7919))

		cfg.ObjectiveFunc = func(pos []float64) float64 {
			if evals >= *maxEvals || time.Now().After(deadline) {
				return bestM.Score + 1.0
			}
			cand := fromNormalized(pos, defs)
			m, params, left, right, evalVelocity, evalReleaseAfter, err := evaluate(cand)
			evals++
			if err != nil {
				return bestM.Score + 0.8
			}

			top = updateTopCandidates(top, *topK, evals, m, defs, cand)

			if m.Score < bestM.Score {
				best = cand
				bestM = m
				bestParams = params
				bestVelocity = evalVelocity
				bestReleaseAfter = evalReleaseAfter
				bestIRL = append(bestIRL[:0], left...)
				bestIRR = append(bestIRR[:0], right...)
				bestImproves++
				fmt.Printf("Improved #%d eval=%d score=%.4f sim=%.2f%%\n", bestImproves, evals, bestM.Score, bestM.Similarity*100.0)
				if bestImproves%*checkpointEvery == 0 {
					if err := writeOutputs(
						*outputIR,
						*outputPreset,
						*reportPath,
						*referencePath,
						*presetPath,
						*sampleRate,
						*note,
						bestVelocity,
						bestReleaseAfter,
						time.Since(start).Seconds(),
						evals,
						strings.ToLower(*mayflyVariant),
						defs,
						best,
						bestM,
						bestParams,
						bestIRL,
						bestIRR,
						checkpoints+1,
						top,
					); err != nil {
						fmt.Fprintf(os.Stderr, "checkpoint write failed: %v\n", err)
					} else {
						checkpoints++
					}
				}
			}

			if evals%*reportEvery == 0 {
				fmt.Printf("Progress round=%d eval=%d elapsed=%.1fs best=%.4f\n", round, evals, time.Since(start).Seconds(), bestM.Score)
			}
			return m.Score
		}

		if _, err := runMayfly(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "mayfly round %d failed: %v\n", round, err)
			continue
		}
	}

	elapsed := time.Since(start).Seconds()
	if err := writeOutputs(
		*outputIR,
		*outputPreset,
		*reportPath,
		*referencePath,
		*presetPath,
		*sampleRate,
		*note,
		bestVelocity,
		bestReleaseAfter,
		elapsed,
		evals,
		strings.ToLower(*mayflyVariant),
		defs,
		best,
		bestM,
		bestParams,
		bestIRL,
		bestIRR,
		checkpoints,
		top,
	); err != nil {
		die("failed to write outputs: %v", err)
	}

	fmt.Printf("Done evals=%d elapsed=%.1fs best_score=%.4f best_similarity=%.2f%% variant=%s\n", evals, elapsed, bestM.Score, bestM.Similarity*100.0, strings.ToLower(*mayflyVariant))
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
	bestIRL []float32,
	bestIRR []float32,
	checkpoints int,
	top []topCandidate,
) error {
	if err := writeStereoWAV(outputIR, bestIRL, bestIRR, sampleRate); err != nil {
		return err
	}

	p := cloneParams(bestParams)
	p.IRWavPath = presetIRPath(outputPreset, outputIR)
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
		BestIRKnobs:     knobs,
		CheckpointCount: checkpoints,
		TopCandidates:   top,
	}

	if reportPath == "" {
		reportPath = outputPreset + ".report.json"
	}
	return writeJSON(reportPath, rep)
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

func newMayflyConfig(variant string, pop int, dims int, iters int) (*mayfly.Config, error) {
	var cfg *mayfly.Config
	switch variant {
	case "ma":
		cfg = mayfly.NewDefaultConfig()
	case "desma":
		cfg = mayfly.NewDESMAConfig()
	case "olce":
		cfg = mayfly.NewOLCEConfig()
	case "eobbma":
		cfg = mayfly.NewEOBBMAConfig()
	case "gsasma":
		cfg = mayfly.NewGSASMAConfig()
	case "mpma":
		cfg = mayfly.NewMPMAConfig()
	case "aoblmoa":
		cfg = mayfly.NewAOBLMOAConfig()
	default:
		return nil, fmt.Errorf("unsupported variant %q", variant)
	}
	cfg.ProblemSize = dims
	cfg.LowerBound = 0.0
	cfg.UpperBound = 1.0
	cfg.MaxIterations = iters
	cfg.NPop = pop
	cfg.NPopF = pop
	cfg.NC = 2 * pop
	cfg.NM = maxInt(1, int(math.Round(0.05*float64(pop))))
	return cfg, nil
}

func runMayfly(cfg *mayfly.Config) (_ *mayfly.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mayfly panic: %v", r)
		}
	}()
	return mayfly.Optimize(cfg)
}

func renderCandidateFromParams(
	params *piano.Params,
	note int,
	velocity int,
	sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	minDuration float64,
	maxDuration float64,
	releaseAfter float64,
) ([]float64, []float32, error) {
	if params == nil {
		return nil, nil, errors.New("nil params")
	}
	p := piano.NewPiano(sampleRate, 16, params)
	p.NoteOn(note, velocity)

	if decayHoldBlocks < 1 {
		decayHoldBlocks = 1
	}
	if minDuration < 0 {
		minDuration = 0
	}
	if maxDuration < minDuration {
		maxDuration = minDuration
	}

	minFrames := int(float64(sampleRate) * minDuration)
	maxFrames := int(float64(sampleRate) * maxDuration)
	releaseAtFrame := int(float64(sampleRate) * releaseAfter)
	if releaseAtFrame < 0 {
		releaseAtFrame = 0
	}
	if maxFrames < 1 {
		return nil, nil, errors.New("max duration too small")
	}

	threshold := math.Pow(10.0, decayDBFS/20.0)
	blockSize := 128
	framesRendered := 0
	belowCount := 0
	noteReleased := false
	stereo := make([]float32, 0, maxFrames*2)

	for framesRendered < maxFrames {
		framesToRender := blockSize
		if framesRendered+framesToRender > maxFrames {
			framesToRender = maxFrames - framesRendered
		}
		if !noteReleased && framesRendered >= releaseAtFrame {
			p.NoteOff(note)
			noteReleased = true
		}
		block := p.Process(framesToRender)
		stereo = append(stereo, block...)
		framesRendered += framesToRender

		if framesRendered >= minFrames {
			if stereoRMS(block) < threshold {
				belowCount++
				if belowCount >= decayHoldBlocks {
					break
				}
			} else {
				belowCount = 0
			}
		}
	}

	return stereoToMono64(stereo), stereo, nil
}

func cloneParams(src *piano.Params) *piano.Params {
	if src == nil {
		return piano.NewDefaultParams()
	}
	d := *src
	d.PerNote = make(map[int]*piano.NoteParams, len(src.PerNote))
	for k, v := range src.PerNote {
		if v == nil {
			d.PerNote[k] = nil
			continue
		}
		nv := *v
		d.PerNote[k] = &nv
	}
	return &d
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
		IRWavPath:                  p.IRWavPath,
		IRWetMix:                   p.IRWetMix,
		IRDryMix:                   p.IRDryMix,
		IRGain:                     p.IRGain,
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

func readWAVMono(path string) ([]float64, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid wav file: %s", path)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}
	if buf == nil || buf.Format == nil || buf.Format.NumChannels < 1 {
		return nil, 0, fmt.Errorf("invalid wav buffer: %s", path)
	}
	ch := buf.Format.NumChannels
	frames := len(buf.Data) / ch
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < ch; c++ {
			sum += float64(buf.Data[i*ch+c])
		}
		out[i] = sum / float64(ch)
	}
	return out, buf.Format.SampleRate, nil
}

func resampleIfNeeded(in []float64, fromRate int, toRate int) ([]float64, error) {
	if fromRate == toRate {
		return in, nil
	}
	r, err := dspresample.NewForRates(
		float64(fromRate),
		float64(toRate),
		dspresample.WithQuality(dspresample.QualityBest),
	)
	if err != nil {
		return nil, err
	}
	return r.Process(in), nil
}

func writeStereoWAV(path string, left []float32, right []float32, sampleRate int) error {
	if len(left) != len(right) {
		return fmt.Errorf("left/right length mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := wav.NewEncoder(f, sampleRate, 16, 2, 1)
	defer enc.Close()

	data := make([]float32, len(left)*2)
	for i := 0; i < len(left); i++ {
		data[i*2] = left[i]
		data[i*2+1] = right[i]
	}
	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 2,
		},
		Data:           data,
		SourceBitDepth: 16,
	}
	return enc.Write(buf)
}

func stereoToMono64(st []float32) []float64 {
	if len(st) < 2 {
		return nil
	}
	n := len(st) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = 0.5 * (float64(st[i*2]) + float64(st[i*2+1]))
	}
	return out
}

func stereoRMS(interleaved []float32) float64 {
	if len(interleaved) == 0 {
		return 0
	}
	var sum float64
	for _, s := range interleaved {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(interleaved)))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
