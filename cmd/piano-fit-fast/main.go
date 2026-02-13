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

	evaluate := func(c candidate) (analysis.Metrics, error) {
		p, velocity, releaseAfter := applyCandidate(baseParams, *note, defs, c)
		mono, _, err := renderCandidateFromParams(
			p,
			*note,
			velocity,
			*sampleRate,
			*decayDBFS,
			*decayHoldBlocks,
			*minDuration,
			*maxDuration,
			releaseAfter,
		)
		if err != nil {
			return analysis.Metrics{}, err
		}
		return analysis.Compare(ref, mono, *sampleRate), nil
	}

	start := time.Now()
	deadline := start.Add(time.Duration(*timeBudget * float64(time.Second)))
	evals := 0
	bestImproves := 0
	checkpoints := 0

	best := initCand
	bestM, err := evaluate(best)
	if err != nil {
		die("initial evaluation failed: %v", err)
	}
	evals++
	fmt.Printf("Start score=%.4f similarity=%.2f%%\n", bestM.Score, bestM.Similarity*100.0)

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
			m, err := evaluate(cand)
			evals++
			if err != nil {
				return bestM.Score + 0.8
			}
			if m.Score < bestM.Score {
				best = cand
				bestM = m
				bestImproves++
				fmt.Printf("Improved #%d eval=%d score=%.4f sim=%.2f%%\n", bestImproves, evals, bestM.Score, bestM.Similarity*100.0)
				if *writeBestCandidate != "" {
					if err := writeBestCandidateSnapshot(
						*writeBestCandidate,
						baseParams,
						*note,
						defs,
						best,
						*sampleRate,
						*decayDBFS,
						*decayHoldBlocks,
						*minDuration,
						*maxDuration,
					); err != nil {
						fmt.Fprintf(os.Stderr, "failed to update best candidate wav: %v\n", err)
					}
				}
				if bestImproves%*checkpointEvery == 0 {
					if err := writeOutputs(*outputPreset, *reportPath, *referencePath, *presetPath, *sampleRate, *note, time.Since(start).Seconds(), evals, strings.ToLower(*mayflyVariant), defs, best, bestM, baseParams, checkpoints+1); err != nil {
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
	if err := writeOutputs(*outputPreset, *reportPath, *referencePath, *presetPath, *sampleRate, *note, elapsed, evals, strings.ToLower(*mayflyVariant), defs, best, bestM, baseParams, checkpoints); err != nil {
		die("failed to write outputs: %v", err)
	}

	if *writeBestCandidate != "" {
		if err := writeBestCandidateSnapshot(
			*writeBestCandidate,
			baseParams,
			*note,
			defs,
			best,
			*sampleRate,
			*decayDBFS,
			*decayHoldBlocks,
			*minDuration,
			*maxDuration,
		); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write best candidate wav: %v\n", err)
		}
	}

	fmt.Printf("Done evals=%d elapsed=%.1fs best_score=%.4f best_similarity=%.2f%% variant=%s\n", evals, elapsed, bestM.Score, bestM.Similarity*100.0, strings.ToLower(*mayflyVariant))
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
	// Mayfly's implementation assumes NC/2 parent pairs are available from both
	// male and female populations.
	cfg.NC = 2 * pop
	// Keep at least one mutation to avoid stalling on small populations.
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

func writeOutputs(
	outputPreset string,
	reportPath string,
	referencePath string,
	presetPath string,
	sampleRate int,
	note int,
	elapsed float64,
	evals int,
	variant string,
	defs []knobDef,
	best candidate,
	bestM analysis.Metrics,
	base *piano.Params,
	checkpoints int,
) error {
	p, _, _ := applyCandidate(base, note, defs, best)
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
		SampleRate:      sampleRate,
		Note:            note,
		DurationSec:     elapsed,
		Evaluations:     evals,
		MayflyVariant:   variant,
		BestScore:       bestM.Score,
		BestSimilarity:  bestM.Similarity,
		BestMetrics:     bestM,
		BestKnobs:       knobs,
		CheckpointCount: checkpoints,
	}

	if reportPath == "" {
		reportPath = outputPreset + ".report.json"
	}
	if err := writeJSON(reportPath, rep); err != nil {
		return err
	}
	return nil
}

func writeBestCandidateSnapshot(
	path string,
	base *piano.Params,
	note int,
	defs []knobDef,
	best candidate,
	sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	minDuration float64,
	maxDuration float64,
) error {
	p, velocity, releaseAfter := applyCandidate(base, note, defs, best)
	_, stereo, err := renderCandidateFromParams(
		p,
		note,
		velocity,
		sampleRate,
		decayDBFS,
		decayHoldBlocks,
		minDuration,
		maxDuration,
		releaseAfter,
	)
	if err != nil {
		return err
	}
	return writeStereoWAV(path, stereo, sampleRate)
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

func writeStereoWAV(path string, samples []float32, sampleRate int) error {
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

	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 2,
		},
		Data:           samples,
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

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

func maxf64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
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
