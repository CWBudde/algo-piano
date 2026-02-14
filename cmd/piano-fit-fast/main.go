package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
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

type runReport struct {
	ReferencePath   string             `json:"reference_path"`
	PresetPath      string             `json:"preset_path"`
	OutputPreset    string             `json:"output_preset"`
	SampleRate      int                `json:"sample_rate"`
	Note            int                `json:"note"`
	DurationSec     float64            `json:"elapsed_seconds"`
	Evaluations     int                `json:"evaluations"`
	MayflyVariant   string             `json:"mayfly_variant"`
	BestScore       float64            `json:"best_score"`
	BestSimilarity  float64            `json:"best_similarity"`
	BestMetrics     analysis.Metrics   `json:"best_metrics"`
	BestKnobs       map[string]float64 `json:"best_knobs"`
	CheckpointCount int                `json:"checkpoint_count"`
}

func main() {
	referencePath := flag.String("reference", "reference/c4.wav", "Reference WAV path")
	presetPath := flag.String("preset", "assets/presets/default.json", "Base preset JSON path")
	outputPreset := flag.String("output-preset", "assets/presets/fitted-c4.json", "Path to write best fitted preset JSON")
	reportPath := flag.String("report", "", "Optional report JSON path (default: <output-preset>.report.json)")
	note := flag.Int("note", 60, "MIDI note to fit")
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
	writeBestCandidate := flag.String("write-best-candidate", "", "Optional WAV path to write best candidate render")
	resume := flag.Bool("resume", true, "Resume from previous best_knobs report when available")
	resumeReport := flag.String("resume-report", "", "Optional report JSON path to resume from (default: current report path)")

	mayflyVariant := flag.String("mayfly-variant", "desma", "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	mayflyPop := flag.Int("mayfly-pop", 10, "Male and female population size per Mayfly run")
	mayflyRoundEvals := flag.Int("mayfly-round-evals", 240, "Target eval budget per Mayfly round")
	workers := flag.String("workers", "1", "Parallel optimization workers running independent Mayfly rounds (number or 'auto')")
	flag.Parse()

	if *maxEvals < 1 {
		die("max-evals must be >= 1")
	}
	if *timeBudget <= 0 {
		die("time-budget must be > 0")
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

	defs, initCand := initCandidate(baseParams, *note)
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
		reference:          ref,
		baseParams:         baseParams,
		defs:               defs,
		initCandidate:      initCand,
		note:               *note,
		sampleRate:         *sampleRate,
		seed:               *seed,
		timeBudget:         *timeBudget,
		maxEvals:           *maxEvals,
		reportEvery:        *reportEvery,
		checkpointEvery:    *checkpointEvery,
		decayDBFS:          *decayDBFS,
		decayHoldBlocks:    *decayHoldBlocks,
		minDuration:        *minDuration,
		maxDuration:        *maxDuration,
		mayflyVariant:      *mayflyVariant,
		mayflyPop:          *mayflyPop,
		mayflyRoundEvals:   *mayflyRoundEvals,
		workers:            parsedWorkers,
		outputPreset:       *outputPreset,
		reportPath:         *reportPath,
		referencePath:      *referencePath,
		presetPath:         *presetPath,
		writeBestCandidate: *writeBestCandidate,
	}

	result, err := runOptimization(cfg)
	if err != nil {
		die("optimization failed: %v", err)
	}

	if err := writeOutputs(*outputPreset, *reportPath, *referencePath, *presetPath, *sampleRate, *note, result.elapsed, result.evals, strings.ToLower(*mayflyVariant), defs, result.best, result.bestMetrics, baseParams, result.checkpoints); err != nil {
		die("failed to write outputs: %v", err)
	}

	if *writeBestCandidate != "" {
		if err := writeBestCandidateSnapshot(
			*writeBestCandidate,
			baseParams,
			*note,
			defs,
			result.best,
			*sampleRate,
			*decayDBFS,
			*decayHoldBlocks,
			*minDuration,
			*maxDuration,
		); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write best candidate wav: %v\n", err)
		}
	}

	fmt.Printf("Done evals=%d elapsed=%.1fs best_score=%.4f best_similarity=%.2f%% variant=%s\n", result.evals, result.elapsed, result.bestMetrics.Score, result.bestMetrics.Similarity*100.0, strings.ToLower(*mayflyVariant))
}

func parseWorkersFlag(raw string) (int, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return 0, fmt.Errorf("empty value (use integer >= 1 or 'auto')")
	}
	if v == "auto" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%q (use integer >= 1 or 'auto')", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("%d (must be >= 1 or 'auto')", n)
	}
	return n, nil
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
	if len(rep.BestKnobs) == 0 {
		return fallback, false, nil
	}

	vals := make([]float64, len(fallback.Vals))
	copy(vals, fallback.Vals)
	updated := false
	for i, d := range defs {
		if v, ok := rep.BestKnobs[d.Name]; ok {
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

func initCandidate(base *piano.Params, note int) ([]knobDef, candidate) {
	np := base.PerNote[note]
	if np == nil {
		np = &piano.NoteParams{Loss: 0.9990, Inharmonicity: 0.12, StrikePosition: 0.18}
	}

	defs := []knobDef{
		{Name: "output_gain", Min: 0.4, Max: 1.8},
		{Name: "ir_wet_mix", Min: 0.2, Max: 1.6},
		{Name: "ir_dry_mix", Min: 0.0, Max: 0.8},
		{Name: "ir_gain", Min: 0.4, Max: 2.2},
		{Name: "hammer_stiffness_scale", Min: 0.6, Max: 1.8},
		{Name: "hammer_exponent_scale", Min: 0.8, Max: 1.2},
		{Name: "hammer_damping_scale", Min: 0.6, Max: 1.8},
		{Name: "hammer_initial_velocity_scale", Min: 0.7, Max: 1.4},
		{Name: "hammer_contact_time_scale", Min: 0.7, Max: 1.6},
		{Name: "unison_detune_scale", Min: 0.0, Max: 2.0},
		{Name: "unison_crossfeed", Min: 0.0, Max: 0.005},
		{Name: fmt.Sprintf("per_note.%d.loss", note), Min: 0.985, Max: 0.99995},
		{Name: fmt.Sprintf("per_note.%d.inharmonicity", note), Min: 0.0, Max: 0.6},
		{Name: fmt.Sprintf("per_note.%d.strike_position", note), Min: 0.08, Max: 0.45},
		{Name: "render.release_after", Min: 0.2, Max: 3.5},
		{Name: "render.velocity", Min: 40, Max: 127, IsInt: true},
	}

	vals := []float64{
		float64(base.OutputGain),
		float64(base.IRWetMix),
		float64(base.IRDryMix),
		float64(base.IRGain),
		float64(base.HammerStiffnessScale),
		float64(base.HammerExponentScale),
		float64(base.HammerDampingScale),
		float64(base.HammerInitialVelocityScale),
		float64(base.HammerContactTimeScale),
		float64(base.UnisonDetuneScale),
		float64(base.UnisonCrossfeed),
		float64(np.Loss),
		float64(np.Inharmonicity),
		float64(np.StrikePosition),
		2.0,
		100,
	}
	for i := range vals {
		vals[i] = clamp(vals[i], defs[i].Min, defs[i].Max)
	}
	return defs, candidate{Vals: vals}
}

func applyCandidate(base *piano.Params, note int, defs []knobDef, c candidate) (*piano.Params, int, float64) {
	p := cloneParams(base)
	if p.PerNote == nil {
		p.PerNote = make(map[int]*piano.NoteParams)
	}
	np := p.PerNote[note]
	if np == nil {
		np = &piano.NoteParams{}
		p.PerNote[note] = np
	}

	velocity := 100
	releaseAfter := 2.0

	for i, def := range defs {
		v := c.Vals[i]
		switch def.Name {
		case "output_gain":
			p.OutputGain = float32(v)
		case "ir_wet_mix":
			p.IRWetMix = float32(v)
		case "ir_dry_mix":
			p.IRDryMix = float32(v)
		case "ir_gain":
			p.IRGain = float32(v)
		case "hammer_stiffness_scale":
			p.HammerStiffnessScale = float32(v)
		case "hammer_exponent_scale":
			p.HammerExponentScale = float32(v)
		case "hammer_damping_scale":
			p.HammerDampingScale = float32(v)
		case "hammer_initial_velocity_scale":
			p.HammerInitialVelocityScale = float32(v)
		case "hammer_contact_time_scale":
			p.HammerContactTimeScale = float32(v)
		case "unison_detune_scale":
			p.UnisonDetuneScale = float32(v)
		case "unison_crossfeed":
			p.UnisonCrossfeed = float32(v)
		case fmt.Sprintf("per_note.%d.loss", note):
			np.Loss = float32(v)
		case fmt.Sprintf("per_note.%d.inharmonicity", note):
			np.Inharmonicity = float32(v)
		case fmt.Sprintf("per_note.%d.strike_position", note):
			np.StrikePosition = float32(v)
		case "render.release_after":
			releaseAfter = v
		case "render.velocity":
			velocity = int(math.Round(v))
		}
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
	return p, velocity, releaseAfter
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
