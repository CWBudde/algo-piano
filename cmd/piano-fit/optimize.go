package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/mayfly"
)

type topCandidate struct {
	Eval       int                `json:"eval"`
	Score      float64            `json:"score"`
	Similarity float64            `json:"similarity"`
	Knobs      map[string]float64 `json:"knobs"`
}

type optimizationConfig struct {
	reference        []float64
	finalReference   []float64
	baseParams       *piano.Params
	defs             []knobDef
	initCandidate    candidate
	note             int
	baseVelocity     int
	baseReleaseAfter float64
	sampleRate       int
	finalSampleRate  int
	seed             int64
	timeBudget       float64
	maxEvals         int
	reportEvery      int
	checkpointEvery  int
	decayDBFS        float64
	decayHoldBlocks  int
	minDuration      float64
	maxDuration      float64
	finalMinDuration float64
	finalMaxDuration float64
	renderBlockSize  int
	refineTopK       int
	mayflyVariant    string
	mayflyPop        int
	mayflyRoundEvals int
	workers          int
	topK             int
	groups           map[string]bool
	workDir          string
	outputIR         string
	outputPreset     string
	reportPath       string
	referencePath    string
	presetPath       string
}

type evalSettings struct {
	reference       []float64
	sampleRate      int
	minDuration     float64
	maxDuration     float64
	decayDBFS       float64
	decayHoldBlocks int
	renderBlockSize int
}

type optimizationEval struct {
	metrics      analysis.Metrics
	params       *piano.Params
	bodyIR       []float32 // mono body IR
	roomIRL      []float32 // stereo room IR left
	roomIRR      []float32 // stereo room IR right
	velocity     int
	releaseAfter float64
}

type optimizationResult struct {
	best             candidate
	bestMetrics      analysis.Metrics
	bestParams       *piano.Params
	bestBodyIR       []float32
	bestRoomIRL      []float32
	bestRoomIRR      []float32
	bestVelocity     int
	bestReleaseAfter float64
	top              []topCandidate
	evals            int
	elapsed          float64
	checkpoints      int
}

type optimizationState struct {
	mu          sync.Mutex
	best        candidate
	bestEval    optimizationEval
	top         []topCandidate
	checkpoints int
}

func runOptimization(cfg *optimizationConfig) (*optimizationResult, error) {
	if err := os.MkdirAll(cfg.workDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create work-dir: %w", err)
	}

	start := time.Now()
	deadline := start.Add(time.Duration(cfg.timeBudget * float64(time.Second)))
	variant := strings.ToLower(cfg.mayflyVariant)
	optEvalSettings := evalSettings{
		reference:       cfg.reference,
		sampleRate:      cfg.sampleRate,
		minDuration:     cfg.minDuration,
		maxDuration:     cfg.maxDuration,
		decayDBFS:       cfg.decayDBFS,
		decayHoldBlocks: cfg.decayHoldBlocks,
		renderBlockSize: cfg.renderBlockSize,
	}
	finalEvalSettings := evalSettings{
		reference:       cfg.finalReference,
		sampleRate:      cfg.finalSampleRate,
		minDuration:     cfg.finalMinDuration,
		maxDuration:     cfg.finalMaxDuration,
		decayDBFS:       cfg.decayDBFS,
		decayHoldBlocks: cfg.decayHoldBlocks,
		renderBlockSize: cfg.renderBlockSize,
	}

	initialScratch := filepath.Join(cfg.workDir, "candidate_ir_init.wav")
	best := cloneCandidate(cfg.initCandidate)
	initialEval, err := evaluateCandidate(cfg, best, initialScratch, optEvalSettings)
	if err != nil {
		return nil, fmt.Errorf("initial evaluation failed: %w", err)
	}
	fmt.Printf("Start score=%.4f similarity=%.2f%%\n", initialEval.metrics.Score, initialEval.metrics.Similarity*100.0)

	state := &optimizationState{
		best:     best,
		bestEval: cloneOptimizationEval(initialEval),
		top:      updateTopCandidates(nil, cfg.topK, 1, initialEval.metrics, cfg.defs, best),
	}

	if _, err := os.Stat(cfg.outputPreset); err != nil && errors.Is(err, os.ErrNotExist) {
		if err := writeOutputs(
			cfg.outputIR,
			cfg.outputPreset,
			cfg.reportPath,
			cfg.referencePath,
			cfg.presetPath,
			optEvalSettings.sampleRate,
			cfg.note,
			initialEval.velocity,
			initialEval.releaseAfter,
			time.Since(start).Seconds(),
			1,
			variant,
			cfg.defs,
			best,
			initialEval.metrics,
			initialEval.params,
			initialEval.bodyIR,
			initialEval.roomIRL,
			initialEval.roomIRR,
			0,
			state.top,
		); err != nil {
			fmt.Fprintf(os.Stderr, "initial write failed: %v\n", err)
		}
	}

	var evals int64 = 1
	var rounds int64
	var improves int64
	var outputMu sync.Mutex
	var latestPersistedImprove int64

	workers := cfg.workers
	if workers == 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerScratch := filepath.Join(cfg.workDir, fmt.Sprintf("candidate_ir_worker_%d.wav", workerID))
			for {
				if time.Now().After(deadline) {
					return
				}
				if atomic.LoadInt64(&evals) >= int64(cfg.maxEvals) {
					return
				}

				round := int(atomic.AddInt64(&rounds, 1))
				remaining := cfg.maxEvals - int(atomic.LoadInt64(&evals))
				if remaining <= 0 {
					return
				}
				budget := minInt(cfg.mayflyRoundEvals, remaining)
				iters := maxInt(1, budget/(2*cfg.mayflyPop))

				mayflyConfig, err := newMayflyConfig(variant, cfg.mayflyPop, len(cfg.defs), iters)
				if err != nil {
					fmt.Fprintf(os.Stderr, "mayfly round %d setup failed: %v\n", round, err)
					return
				}
				mayflyConfig.Rand = rand.New(rand.NewSource(cfg.seed + int64(round)*7919))
				mayflyConfig.ObjectiveFunc = func(pos []float64) float64 {
					if time.Now().After(deadline) {
						return currentBestScore(state) + 1.0
					}
					evalNum, ok := reserveEval(&evals, cfg.maxEvals)
					if !ok {
						return currentBestScore(state) + 1.0
					}

					cand := fromNormalized(pos, cfg.defs)
					evalRes, err := evaluateCandidate(cfg, cand, workerScratch, optEvalSettings)
					if err != nil {
						return currentBestScore(state) + 0.8
					}

					improved := false
					var improveNum int64
					checkpointDue := false
					var bestSnapshot candidate
					var bestEvalSnapshot optimizationEval
					var topSnapshot []topCandidate
					bestScore := 0.0

					state.mu.Lock()
					state.top = updateTopCandidates(state.top, cfg.topK, int(evalNum), evalRes.metrics, cfg.defs, cand)
					if evalRes.metrics.Score < state.bestEval.metrics.Score {
						state.best = cloneCandidate(cand)
						state.bestEval = cloneOptimizationEval(evalRes)
						improved = true
						improveNum = atomic.AddInt64(&improves, 1)
						if cfg.checkpointEvery > 0 && improveNum%int64(cfg.checkpointEvery) == 0 {
							checkpointDue = true
						}
						bestSnapshot = cloneCandidate(state.best)
						bestEvalSnapshot = cloneOptimizationEval(state.bestEval)
						topSnapshot = cloneTopCandidates(state.top)
					}
					bestScore = state.bestEval.metrics.Score
					state.mu.Unlock()

					if improved {
						fmt.Printf("Improved #%d eval=%d score=%.4f sim=%.2f%%\n", improveNum, evalNum, bestEvalSnapshot.metrics.Score, bestEvalSnapshot.metrics.Similarity*100.0)
						outputMu.Lock()
						if improveNum > latestPersistedImprove {
							latestPersistedImprove = improveNum
							if checkpointDue {
								state.mu.Lock()
								checkpointNum := state.checkpoints + 1
								state.mu.Unlock()
								if err := writeOutputs(
									cfg.outputIR,
									cfg.outputPreset,
									cfg.reportPath,
									cfg.referencePath,
									cfg.presetPath,
									optEvalSettings.sampleRate,
									cfg.note,
									bestEvalSnapshot.velocity,
									bestEvalSnapshot.releaseAfter,
									time.Since(start).Seconds(),
									int(atomic.LoadInt64(&evals)),
									variant,
									cfg.defs,
									bestSnapshot,
									bestEvalSnapshot.metrics,
									bestEvalSnapshot.params,
									bestEvalSnapshot.bodyIR,
									bestEvalSnapshot.roomIRL,
									bestEvalSnapshot.roomIRR,
									checkpointNum,
									topSnapshot,
								); err != nil {
									fmt.Fprintf(os.Stderr, "checkpoint write failed: %v\n", err)
								} else {
									state.mu.Lock()
									if checkpointNum > state.checkpoints {
										state.checkpoints = checkpointNum
									}
									state.mu.Unlock()
								}
							}
						}
						outputMu.Unlock()
					}

					if cfg.reportEvery > 0 && evalNum%int64(cfg.reportEvery) == 0 {
						fmt.Printf("Progress eval=%d/%d elapsed=%.1fs best=%.4f\n", evalNum, cfg.maxEvals, time.Since(start).Seconds(), bestScore)
					}
					return evalRes.metrics.Score
				}

				if _, err := runMayfly(mayflyConfig); err != nil {
					fmt.Fprintf(os.Stderr, "mayfly round %d failed: %v\n", round, err)
				}
			}
		}(i + 1)
	}
	wg.Wait()

	state.mu.Lock()
	finalBest := cloneCandidate(state.best)
	finalEval := cloneOptimizationEval(state.bestEval)
	finalTop := cloneTopCandidates(state.top)
	finalCheckpoints := state.checkpoints
	state.mu.Unlock()

	refineTopK := cfg.refineTopK
	if refineTopK < 1 {
		refineTopK = 1
	}
	seen := make(map[string]struct{}, refineTopK)
	candidates := make([]candidate, 0, refineTopK)
	addCandidate := func(c candidate) {
		if len(candidates) >= refineTopK {
			return
		}
		key := candidateKey(c)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, c)
	}
	addCandidate(finalBest)
	for _, entry := range finalTop {
		if len(candidates) >= refineTopK {
			break
		}
		addCandidate(candidateFromTop(entry, cfg.defs, finalBest))
	}

	refinedTop := make([]topCandidate, 0, cfg.topK)
	var refinedBest candidate
	var refinedEval optimizationEval
	hasRefinedBest := false
	for i, cand := range candidates {
		scratchPath := filepath.Join(cfg.workDir, fmt.Sprintf("candidate_ir_refine_%d.wav", i+1))
		evalRes, err := evaluateCandidate(cfg, cand, scratchPath, finalEvalSettings)
		if err != nil {
			fmt.Fprintf(os.Stderr, "refine eval %d failed: %v\n", i+1, err)
			continue
		}
		refinedTop = updateTopCandidates(refinedTop, cfg.topK, i+1, evalRes.metrics, cfg.defs, cand)
		if !hasRefinedBest || evalRes.metrics.Score < refinedEval.metrics.Score {
			refinedBest = cloneCandidate(cand)
			refinedEval = cloneOptimizationEval(evalRes)
			hasRefinedBest = true
		}
	}
	if hasRefinedBest {
		finalBest = refinedBest
		finalEval = refinedEval
		if len(refinedTop) > 0 {
			finalTop = refinedTop
		}
	}

	return &optimizationResult{
		best:             finalBest,
		bestMetrics:      finalEval.metrics,
		bestParams:       finalEval.params,
		bestBodyIR:       finalEval.bodyIR,
		bestRoomIRL:      finalEval.roomIRL,
		bestRoomIRR:      finalEval.roomIRR,
		bestVelocity:     finalEval.velocity,
		bestReleaseAfter: finalEval.releaseAfter,
		top:              finalTop,
		evals:            int(atomic.LoadInt64(&evals)),
		elapsed:          time.Since(start).Seconds(),
		checkpoints:      finalCheckpoints,
	}, nil
}

func evaluateCandidate(cfg *optimizationConfig, cand candidate, scratchPath string, settings evalSettings) (optimizationEval, error) {
	irCfgs, params, evalVelocity, evalReleaseAfter := applyCandidate(
		cfg.baseParams,
		settings.sampleRate,
		cfg.note,
		cfg.baseVelocity,
		cfg.baseReleaseAfter,
		cfg.defs,
		cand,
	)

	if needsIRSynthesis(cfg.groups) {
		// IR synthesis mode: generate body/room IR, render with dual IR buffers.
		var bodyIR []float32
		var roomL, roomR []float32
		if cfg.groups["body-ir"] {
			ir, err := irsynth.GenerateBody(irCfgs.body)
			if err != nil {
				return optimizationEval{}, fmt.Errorf("body IR: %w", err)
			}
			bodyIR = ir
		}
		if cfg.groups["room-ir"] {
			l, r, err := irsynth.GenerateRoom(irCfgs.room)
			if err != nil {
				return optimizationEval{}, fmt.Errorf("room IR: %w", err)
			}
			roomL, roomR = l, r
		}
		// Clear IR paths so NewPiano won't load from disk; we set buffers directly.
		params.IRWavPath = ""
		params.BodyIRWavPath = ""
		params.RoomIRWavPath = ""
		mono, _, err := renderCandidateWithDualIR(
			params,
			bodyIR, roomL, roomR,
			cfg.note,
			evalVelocity,
			settings.sampleRate,
			settings.decayDBFS,
			settings.decayHoldBlocks,
			settings.minDuration,
			settings.maxDuration,
			settings.renderBlockSize,
			evalReleaseAfter,
		)
		if err != nil {
			return optimizationEval{}, err
		}
		return optimizationEval{
			metrics:      analysis.Compare(settings.reference, mono, settings.sampleRate),
			params:       params,
			bodyIR:       bodyIR,
			roomIRL:      roomL,
			roomIRR:      roomR,
			velocity:     evalVelocity,
			releaseAfter: evalReleaseAfter,
		}, nil
	}

	// Non-IR mode: load IR from disk via renderCandidateFromParams.
	mono, _, err := renderCandidateFromParams(
		params,
		cfg.note,
		evalVelocity,
		settings.sampleRate,
		settings.decayDBFS,
		settings.decayHoldBlocks,
		settings.minDuration,
		settings.maxDuration,
		settings.renderBlockSize,
		evalReleaseAfter,
	)
	if err != nil {
		return optimizationEval{}, err
	}
	return optimizationEval{
		metrics:      analysis.Compare(settings.reference, mono, settings.sampleRate),
		params:       params,
		velocity:     evalVelocity,
		releaseAfter: evalReleaseAfter,
	}, nil
}

func renderCandidateWithDualIR(
	params *piano.Params,
	bodyIR []float32,
	roomIRL []float32,
	roomIRR []float32,
	note int,
	velocity int,
	sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	minDuration float64,
	maxDuration float64,
	blockSize int,
	releaseAfter float64,
) ([]float64, []float32, error) {
	if params == nil {
		return nil, nil, errors.New("nil params")
	}
	p := piano.NewPiano(sampleRate, 16, params)
	if len(bodyIR) > 0 {
		p.SetBodyIR(bodyIR)
	}
	if len(roomIRL) > 0 && len(roomIRR) > 0 {
		p.SetRoomIR(roomIRL, roomIRR)
	}
	return renderPiano(p, note, velocity, sampleRate, decayDBFS, decayHoldBlocks, minDuration, maxDuration, blockSize, releaseAfter)
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
	blockSize int,
	releaseAfter float64,
) ([]float64, []float32, error) {
	if params == nil {
		return nil, nil, errors.New("nil params")
	}
	p := piano.NewPiano(sampleRate, 16, params)
	return renderPiano(p, note, velocity, sampleRate, decayDBFS, decayHoldBlocks, minDuration, maxDuration, blockSize, releaseAfter)
}

func renderPiano(
	p *piano.Piano,
	note int,
	velocity int,
	sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	minDuration float64,
	maxDuration float64,
	blockSize int,
	releaseAfter float64,
) ([]float64, []float32, error) {
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
	if blockSize < 16 {
		blockSize = 16
	}
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

func cloneCandidate(c candidate) candidate {
	vals := make([]float64, len(c.Vals))
	copy(vals, c.Vals)
	return candidate{Vals: vals}
}

func cloneOptimizationEval(in optimizationEval) optimizationEval {
	out := optimizationEval{
		metrics:      in.metrics,
		params:       cloneParams(in.params),
		velocity:     in.velocity,
		releaseAfter: in.releaseAfter,
	}
	if len(in.bodyIR) > 0 {
		out.bodyIR = append([]float32(nil), in.bodyIR...)
	}
	if len(in.roomIRL) > 0 {
		out.roomIRL = append([]float32(nil), in.roomIRL...)
	}
	if len(in.roomIRR) > 0 {
		out.roomIRR = append([]float32(nil), in.roomIRR...)
	}
	return out
}

func cloneTopCandidates(in []topCandidate) []topCandidate {
	out := make([]topCandidate, len(in))
	for i := range in {
		entry := topCandidate{
			Eval:       in[i].Eval,
			Score:      in[i].Score,
			Similarity: in[i].Similarity,
			Knobs:      make(map[string]float64, len(in[i].Knobs)),
		}
		for k, v := range in[i].Knobs {
			entry.Knobs[k] = v
		}
		out[i] = entry
	}
	return out
}

func candidateFromTop(entry topCandidate, defs []knobDef, fallback candidate) candidate {
	vals := make([]float64, len(fallback.Vals))
	copy(vals, fallback.Vals)
	for i, d := range defs {
		if v, ok := entry.Knobs[d.Name]; ok {
			vals[i] = clamp(v, d.Min, d.Max)
			if d.IsInt {
				vals[i] = math.Round(vals[i])
			}
		}
	}
	return candidate{Vals: vals}
}

func candidateKey(c candidate) string {
	var b strings.Builder
	for i, v := range c.Vals {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.6g", v)
	}
	return b.String()
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

func reserveEval(evals *int64, maxEvals int) (int64, bool) {
	for {
		cur := atomic.LoadInt64(evals)
		if cur >= int64(maxEvals) {
			return 0, false
		}
		if atomic.CompareAndSwapInt64(evals, cur, cur+1) {
			return cur + 1, true
		}
	}
}

func currentBestScore(state *optimizationState) float64 {
	state.mu.Lock()
	score := state.bestEval.metrics.Score
	state.mu.Unlock()
	return score
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
