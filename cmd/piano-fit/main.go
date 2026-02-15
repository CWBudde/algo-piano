package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	fitcommon "github.com/cwbudde/algo-piano/internal/fitcommon"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

func main() {
	referencePath := flag.String("reference", "reference/c4.wav", "Reference WAV path")
	presetPath := flag.String("preset", "assets/presets/default.json", "Base preset JSON path")
	outputIR := flag.String("output-ir", "", "Path to write best synthesized IR WAV (required when body-ir or room-ir groups active)")
	outputPreset := flag.String("output-preset", "assets/presets/fitted-c4.json", "Path to write best fitted preset JSON")
	reportPath := flag.String("report", "", "Optional report JSON path (default: <output-preset>.report.json)")
	workDir := flag.String("work-dir", "out/fit", "Directory for temporary candidates")
	optimize := flag.String("optimize", "piano,mix", "Comma-separated knob groups to optimize: piano, body-ir, room-ir, mix")
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
	optSampleRate := flag.Int("opt-sample-rate", 0, "Optimization-loop sample rate (0 uses --sample-rate)")
	optMinDuration := flag.Float64("opt-min-duration", -1, "Optimization-loop min render duration seconds (<0 uses --min-duration)")
	optMaxDuration := flag.Float64("opt-max-duration", -1, "Optimization-loop max render duration seconds (<0 uses --max-duration)")
	renderBlockSize := flag.Int("render-block-size", 128, "Audio render block size for candidate evaluation")
	refineTopK := flag.Int("refine-top-k", 3, "After optimization, re-evaluate best N candidates at full settings")
	topK := flag.Int("top-k", 5, "How many top candidates to keep in report")
	resume := flag.Bool("resume", true, "Resume from previous best_knobs report when available")
	resumeReport := flag.String("resume-report", "", "Optional report JSON path to resume from (default: current report path)")
	workers := flag.String("workers", "1", "Parallel optimization workers running independent Mayfly rounds (number or 'auto')")

	mayflyVariant := flag.String("mayfly-variant", "desma", "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	mayflyPop := flag.Int("mayfly-pop", 10, "Male and female population size per Mayfly run")
	mayflyRoundEvals := flag.Int("mayfly-round-evals", 240, "Target eval budget per Mayfly round")
	flag.Parse()

	groups, err := parseOptimizeGroups(*optimize)
	if err != nil {
		die("invalid --optimize: %v", err)
	}

	if needsIRSynthesis(groups) && *outputIR == "" {
		die("--output-ir is required when body-ir or room-ir groups are active")
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
	if *optSampleRate <= 0 {
		*optSampleRate = *sampleRate
	}
	if *optMinDuration < 0 {
		*optMinDuration = *minDuration
	}
	if *optMaxDuration < 0 {
		*optMaxDuration = *maxDuration
	}
	if *optMaxDuration < *optMinDuration {
		*optMaxDuration = *optMinDuration
	}
	if *renderBlockSize < 16 {
		*renderBlockSize = 16
	}
	if *refineTopK < 1 {
		*refineTopK = 1
	}
	if *refineTopK > *topK {
		*refineTopK = *topK
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

	refRaw, refSR, err := readWAVMono(*referencePath)
	if err != nil {
		die("failed to read reference: %v", err)
	}
	refOpt, err := resampleIfNeeded(refRaw, refSR, *optSampleRate)
	if err != nil {
		die("failed to resample optimization reference: %v", err)
	}
	refFull, err := resampleIfNeeded(refRaw, refSR, *sampleRate)
	if err != nil {
		die("failed to resample full reference: %v", err)
	}

	defs, initCand := initCandidate(
		baseParams,
		*optSampleRate,
		*note,
		*velocity,
		*releaseAfter,
		groups,
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
		reference:        refOpt,
		finalReference:   refFull,
		baseParams:       baseParams,
		defs:             defs,
		initCandidate:    initCand,
		note:             *note,
		baseVelocity:     *velocity,
		baseReleaseAfter: *releaseAfter,
		sampleRate:       *optSampleRate,
		finalSampleRate:  *sampleRate,
		seed:             *seed,
		timeBudget:       *timeBudget,
		maxEvals:         *maxEvals,
		reportEvery:      *reportEvery,
		checkpointEvery:  *checkpointEvery,
		decayDBFS:        *decayDBFS,
		decayHoldBlocks:  *decayHoldBlocks,
		minDuration:      *optMinDuration,
		maxDuration:      *optMaxDuration,
		finalMinDuration: *minDuration,
		finalMaxDuration: *maxDuration,
		renderBlockSize:  *renderBlockSize,
		refineTopK:       *refineTopK,
		mayflyVariant:    *mayflyVariant,
		mayflyPop:        *mayflyPop,
		mayflyRoundEvals: *mayflyRoundEvals,
		workers:          parsedWorkers,
		topK:             *topK,
		groups:           groups,
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
		result.bestBodyIR,
		result.bestRoomIRL,
		result.bestRoomIRR,
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

func loadCandidateFromReport(path string, defs []knobDef, fallback candidate) (candidate, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fallback, false, nil
		}
		return fallback, false, err
	}

	// Use a flexible struct to check both unified best_knobs and legacy best_ir_knobs.
	var rep struct {
		BestKnobs   map[string]float64 `json:"best_knobs"`
		BestIRKnobs map[string]float64 `json:"best_ir_knobs"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return fallback, false, err
	}

	knobs := rep.BestKnobs
	if len(knobs) == 0 {
		knobs = rep.BestIRKnobs // backwards compat with piano-fit-ir reports
	}
	if len(knobs) == 0 {
		return fallback, false, nil
	}

	vals := make([]float64, len(fallback.Vals))
	copy(vals, fallback.Vals)
	updated := false
	for i, d := range defs {
		if v, ok := knobs[d.Name]; ok {
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
