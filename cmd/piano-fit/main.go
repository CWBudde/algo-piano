package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
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
	notesFlag := flag.String("notes", "", "Comma-separated MIDI notes to fit jointly, e.g. \"48,60\" (empty uses --note). "+
		"CAVEAT: piano.defaultUnisonForNote buckets notes as <40 (1 string), <70 (2 strings, -/+1.8 cents) and >=70 "+
		"(3 strings, -/+3.0 cents), so a shared unison_detune_scale across notes from different buckets settles on a "+
		"compromise optimal for neither. Start with notes from one bucket, e.g. --notes 48,60.")
	referenceMap := flag.String("reference-map", "", "Per-note reference WAVs, e.g. \"48=reference/c3.wav,60=reference/c4.wav\" (wins over --reference)")
	aggregateFlag := flag.String("aggregate", "mean", "Multi-note objective aggregation: mean|max|mean-max")
	noteWeights := flag.String("note-weights", "", "Per-note aggregate weights, e.g. \"48=1,60=2\" (empty means uniform)")
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

	noResonance := flag.Bool("no-resonance", false, "Disable sympathetic resonance during optimization (faster evals)")
	cpuProfile := flag.String("cpuprofile", "", "Write CPU profile to file")
	mayflyVariant := flag.String("mayfly-variant", "desma", "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	mayflyPop := flag.Int("mayfly-pop", 10, "Male and female population size per Mayfly run")
	mayflyRoundEvals := flag.Int("mayfly-round-evals", 240, "Target eval budget per Mayfly round")

	polish := flag.Bool("polish", false, "Run a deterministic coordinate-descent polish stage after optimization")
	polishOnly := flag.Bool("polish-only", false, "Skip the Mayfly search entirely and only run the polish stage (implies --polish); pair with --resume as a finishing move")
	polishKnobs := flag.String("polish-knobs", defaultPolishKnobs, "Comma-separated knob names the polish stage may move")
	polishEvals := flag.Int("polish-evals", 200, "Hard evaluation budget for the polish stage")
	polishRounds := flag.Int("polish-rounds", 6, "Maximum coordinate-descent rounds")
	polishStep := flag.Float64("polish-step", 0.08, "Initial polish step in normalised knob space")
	polishShrink := flag.Float64("polish-shrink", 0.5, "Step shrink factor applied after a sweep that improves nothing")
	polishMinStep := flag.Float64("polish-min-step", 0.004, "Polish stops once the step falls below this")

	// --sweep-* block. Every flag here is INERT unless --sweep is set: they
	// configure the deterministic sensitivity/Pareto sweep in sweep.go, which
	// short-circuits the optimizer entirely.
	sweep := flag.Bool("sweep", false, "Run a deterministic sensitivity + Pareto sweep over the active knobs instead of the optimizer. "+
		"Renders, measures, writes one JSON report and exits: no preset, no run report, no RNG")
	sweepOut := flag.String("sweep-out", "", "Sweep report JSON path (default: out/sweep/<pass>-note<N>.json)")
	sweepSamples := flag.Int("sweep-samples", 9, "One-at-a-time samples per knob, endpoints inclusive")
	sweepJointEvals := flag.Int("sweep-joint-evals", 2048, "Joint-stage sample count (0 disables the joint stage)")
	sweepJointSkip := flag.Int("sweep-joint-skip", 64, "Halton index offset (burn-in) for the joint stage")
	sweepJointMaxDims := flag.Int("sweep-joint-max-dims", 8, "Refuse the joint stage above this dimensionality")
	sweepProfiles := flag.String("sweep-profiles", "", "Comma-separated scoring profiles to record per sample "+
		"(empty = the pass profile first, then legacy-v1). The first profile is the primary Pareto objective")

	matchOutputGainFlag := flag.Bool("match-output-gain", true, "Solve output_gain analytically after the search instead of searching it "+
		"(analysis.Compare RMS-normalises both signals, so output_gain cannot move the score at all)")

	passFlag := flag.String("pass", "none", "Per-aspect fitting pass: none|attack|sustain|inharmonicity. Restricts which knobs may move and "+
		"which part of the signal is compared; orthogonal to --optimize")
	profileFlag := flag.String("profile", "", "Weighting profile used to score candidates (empty = the pass default: "+
		"legacy-v1 for --pass none, attack-v1/decay-v1/inharmonicity-v1 for the three passes). "+
		"NOTE: scores are only comparable within one profile")
	passWindowFlag := flag.String("pass-window", "", "Compare window \"start:end\" in seconds (empty = whole signal). "+
		"NOTE: the window is applied BEFORE analysis.Compare, so trimLeadingSilence, normalizeRMS and lag estimation all "+
		"re-run inside the window; windowed scores are NOT comparable to full-signal scores")

	flag.Parse()

	if *cpuProfile != "" {
		file, err := os.Create(*cpuProfile)
		if err != nil {
			die("cpuprofile: %v", err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			die("cpuprofile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	groups, err := parseOptimizeGroups(*optimize)
	if err != nil {
		die("invalid --optimize: %v", err)
	}

	if *polishOnly {
		*polish = true
	}

	notes, err := parseNotesFlag(*notesFlag, *note)
	if err != nil {
		die("invalid --notes: %v", err)
	}
	refMap, err := parseNoteReferenceMap(*referenceMap)
	if err != nil {
		die("invalid --reference-map: %v", err)
	}
	weightMap, err := parseNoteWeights(*noteWeights)
	if err != nil {
		die("invalid --note-weights: %v", err)
	}
	aggregate, err := parseAggregate(*aggregateFlag)
	if err != nil {
		die("invalid --aggregate: %v", err)
	}
	passSpecification, err := parsePass(*passFlag)
	if err != nil {
		die("invalid --pass: %v", err)
	}
	passSpecification.Window, err = parseWindow(*passWindowFlag)
	if err != nil {
		die("invalid --pass-window: %v", err)
	}
	passSpecification, err = passSpecification.withProfile(*profileFlag)
	if err != nil {
		die("invalid --profile: %v", err)
	}
	passScore, err := passScorer(passSpecification)
	if err != nil {
		die("invalid scoring profile: %v", err)
	}
	referencePaths, err := resolveReferences(notes, refMap, *referencePath)
	if err != nil {
		die("%v", err)
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
	// Fall back to the shipped IR as the *room* stage of the radiation path, so
	// fitting never silently selects the legacy single-IR path as its default.
	piano.ApplyDefaultRoomIR(baseParams)
	// --no-resonance is an optimization-speed knob, not a model change: it
	// silences the sympathetic resonance while candidates are scored but must
	// not leak into the written preset. Remember the preset's own setting and
	// restore it before the outputs are written, otherwise a staged pipeline
	// that disables resonance for the cheap early stages would hand every
	// later stage a preset with resonance permanently switched off.
	presetResonance := baseParams.ResonanceEnabled
	if *noResonance {
		baseParams.ResonanceEnabled = false
	}

	targets, err := loadNoteTargets(notes, referencePaths, resolveNoteWeights(notes, weightMap), *optSampleRate, *sampleRate)
	if err != nil {
		die("%v", err)
	}

	defs, initCand := initCandidate(
		baseParams,
		*optSampleRate,
		notes,
		*velocity,
		*releaseAfter,
		groups,
		*matchOutputGainFlag,
	)
	// --resume is deliberately ignored in sweep mode: a stale checkpoint would
	// silently move the sweep's baseline off --preset, and the baseline is the
	// point every sensitivity span and the constrained set are measured
	// against. See the matching --time-budget notice below.
	if *resume && *sweep {
		fmt.Println("sweep: --resume ignored (the baseline must be exactly --preset)")
	}
	if *resume && !*sweep {
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

	if !passSpecification.isNone() {
		// render.velocity and render.release_after are not preset fields: they
		// come from --velocity/--release-after. Seed them from the (possibly
		// resumed) candidate BEFORE filtering, so a pass that freezes them
		// keeps the fitted values instead of silently reverting to the CLI
		// defaults.
		*velocity, *releaseAfter = seedRenderControls(defs, initCand, *velocity, *releaseAfter)
		defs, initCand = filterKnobsForPass(defs, initCand, passSpecification)
		if len(defs) == 0 {
			die("--pass %s leaves no knobs to optimize with --optimize %s", passSpecification.Name, *optimize)
		}
		fmt.Printf("Pass %s: %d of the active knobs may move, scored with profile %s\n",
			passSpecification.Name, len(defs), passSpecification.profileName())
	} else if passSpecification.profileName() != analysis.ProfileLegacyV1 {
		fmt.Printf("Scoring with profile %s (scores are not comparable to legacy-v1 runs)\n", passSpecification.profileName())
	}

	polishIndices := []int(nil)
	if *polish {
		knobsRaw := *polishKnobs
		if knobsRaw == defaultPolishKnobs {
			// A default the user never typed must not turn a --pass or a
			// narrow --optimize selection into a hard error.
			knobsRaw = intersectKnobNames(knobsRaw, defs)
		}
		if knobsRaw == "" {
			fmt.Fprintf(os.Stderr, "polish disabled: none of the default polish knobs (%s) are active\n", defaultPolishKnobs)
			*polish = false
			*polishOnly = false
		} else if polishIndices, err = parsePolishKnobs(knobsRaw, defs); err != nil {
			die("invalid --polish-knobs: %v", err)
		}
	}

	cfg := &optimizationConfig{
		targets:          targets,
		aggregate:        aggregate,
		baseParams:       baseParams,
		defs:             defs,
		initCandidate:    initCand,
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
		referencePath:    referencePaths[0],
		presetPath:       *presetPath,

		scorer: passScore,
		pass:   passSpecification.Name,

		polish:            *polish,
		polishOnly:        *polishOnly,
		polishKnobIndices: polishIndices,
		polishEvals:       *polishEvals,
		polishRounds:      *polishRounds,
		polishStep:        *polishStep,
		polishShrink:      *polishShrink,
		polishMinStep:     *polishMinStep,

		matchOutputGain: *matchOutputGainFlag,
	}

	var passWindow *windowSpec
	if !passSpecification.Window.isZero() {
		w := passSpecification.Window
		passWindow = &w
	}

	if *sweep {
		runSweepMode(cfg, sweepModeArgs{
			outPath:      *sweepOut,
			samples:      *sweepSamples,
			jointEvals:   *sweepJointEvals,
			jointSkip:    *sweepJointSkip,
			jointMaxDims: *sweepJointMaxDims,
			profilesRaw:  *sweepProfiles,
			passProfile:  passSpecification.profileName(),
			passName:     passSpecification.Name,
			passWindow:   passWindow,
			note:         notes[0],
			workers:      parsedWorkers,
			timeBudget:   *timeBudget,
		})
		return
	}

	result, err := runOptimization(cfg)
	if err != nil {
		die("optimization failed: %v", err)
	}

	if *noResonance && result.bestParams != nil {
		result.bestParams.ResonanceEnabled = presetResonance
	}

	if err := writeOutputs(outputRequest{
		outputIR:      *outputIR,
		outputPreset:  *outputPreset,
		reportPath:    *reportPath,
		referencePath: referencePaths[0],
		presetPath:    *presetPath,
		sampleRate:    *sampleRate,
		note:          notes[0],
		velocity:      result.bestVelocity,
		releaseAfter:  result.bestReleaseAfter,
		elapsed:       result.elapsed,
		evals:         result.evals,
		variant:       strings.ToLower(*mayflyVariant),
		defs:          defs,
		best:          result.best,
		bestScore:     result.bestScore,
		bestMetrics:   result.bestMetrics,
		bestParams:    result.bestParams,
		bestBodyIR:    result.bestBodyIR,
		bestRoomIRL:   result.bestRoomIRL,
		bestRoomIRR:   result.bestRoomIRR,
		checkpoints:   result.checkpoints,
		top:           result.top,

		notes:             notes,
		perNote:           result.bestNotes,
		aggregate:         aggregate,
		pass:              passSpecification.Name,
		passWindow:        passWindow,
		rendersPerEval:    len(targets),
		polish:            result.polish,
		outputGainMatched: result.outputGainRatio,
	}); err != nil {
		die("failed to write outputs: %v", err)
	}

	fmt.Printf("Done evals=%d elapsed=%.1fs best_score=%.4f best_similarity=%.2f%% notes=%v variant=%s\n",
		result.evals, result.elapsed, result.bestScore, result.bestMetrics.Similarity*100.0, sortedNotes(notes), strings.ToLower(*mayflyVariant))
	if len(result.bestNotes) > 1 {
		for _, nr := range result.bestNotes {
			fmt.Printf("  note %d score=%.4f similarity=%.2f%% (%s)\n", nr.Note, nr.Score, nr.Similarity*100.0, nr.ReferencePath)
		}
	}
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
