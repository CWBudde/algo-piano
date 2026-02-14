package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/mayfly"
)

type optimizationConfig struct {
	reference          []float64
	baseParams         *piano.Params
	defs               []knobDef
	initCandidate      candidate
	note               int
	sampleRate         int
	seed               int64
	timeBudget         float64
	maxEvals           int
	reportEvery        int
	checkpointEvery    int
	decayDBFS          float64
	decayHoldBlocks    int
	minDuration        float64
	maxDuration        float64
	mayflyVariant      string
	mayflyPop          int
	mayflyRoundEvals   int
	workers            int
	outputPreset       string
	reportPath         string
	referencePath      string
	presetPath         string
	writeBestCandidate string
}

type optimizationResult struct {
	best        candidate
	bestMetrics analysis.Metrics
	evals       int
	elapsed     float64
	checkpoints int
}

type optimizationState struct {
	mu          sync.Mutex
	best        candidate
	bestMetrics analysis.Metrics
	checkpoints int
}

func runOptimization(cfg *optimizationConfig) (*optimizationResult, error) {
	evaluate := func(c candidate) (analysis.Metrics, error) {
		p, velocity, releaseAfter := applyCandidate(cfg.baseParams, cfg.note, cfg.defs, c)
		mono, _, err := renderCandidateFromParams(
			p,
			cfg.note,
			velocity,
			cfg.sampleRate,
			cfg.decayDBFS,
			cfg.decayHoldBlocks,
			cfg.minDuration,
			cfg.maxDuration,
			releaseAfter,
		)
		if err != nil {
			return analysis.Metrics{}, err
		}
		return analysis.Compare(cfg.reference, mono, cfg.sampleRate), nil
	}

	start := time.Now()
	deadline := start.Add(time.Duration(cfg.timeBudget * float64(time.Second)))
	variant := strings.ToLower(cfg.mayflyVariant)

	best := cloneCandidate(cfg.initCandidate)
	bestM, err := evaluate(best)
	if err != nil {
		return nil, fmt.Errorf("initial evaluation failed: %w", err)
	}
	fmt.Printf("Start score=%.4f similarity=%.2f%%\n", bestM.Score, bestM.Similarity*100.0)

	state := &optimizationState{
		best:        best,
		bestMetrics: bestM,
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
		go func() {
			defer wg.Done()
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
					m, err := evaluate(cand)
					if err != nil {
						return currentBestScore(state) + 0.8
					}

					improved := false
					var improveNum int64
					checkpointDue := false
					var bestSnapshot candidate
					var bestMetrics analysis.Metrics

					state.mu.Lock()
					if m.Score < state.bestMetrics.Score {
						state.best = cloneCandidate(cand)
						state.bestMetrics = m
						improved = true
						improveNum = atomic.AddInt64(&improves, 1)
						if cfg.checkpointEvery > 0 && improveNum%int64(cfg.checkpointEvery) == 0 {
							checkpointDue = true
						}
					}
					bestSnapshot = cloneCandidate(state.best)
					bestMetrics = state.bestMetrics
					state.mu.Unlock()

					if improved {
						fmt.Printf("Improved #%d eval=%d score=%.4f sim=%.2f%%\n", improveNum, evalNum, bestMetrics.Score, bestMetrics.Similarity*100.0)
						outputMu.Lock()
						if improveNum > latestPersistedImprove {
							latestPersistedImprove = improveNum
							if cfg.writeBestCandidate != "" {
								if err := writeBestCandidateSnapshot(
									cfg.writeBestCandidate,
									cfg.baseParams,
									cfg.note,
									cfg.defs,
									bestSnapshot,
									cfg.sampleRate,
									cfg.decayDBFS,
									cfg.decayHoldBlocks,
									cfg.minDuration,
									cfg.maxDuration,
								); err != nil {
									fmt.Fprintf(os.Stderr, "failed to update best candidate wav: %v\n", err)
								}
							}
							if checkpointDue {
								state.mu.Lock()
								checkpointNum := state.checkpoints + 1
								state.mu.Unlock()
								if err := writeOutputs(cfg.outputPreset, cfg.reportPath, cfg.referencePath, cfg.presetPath, cfg.sampleRate, cfg.note, time.Since(start).Seconds(), int(atomic.LoadInt64(&evals)), variant, cfg.defs, bestSnapshot, bestMetrics, cfg.baseParams, checkpointNum); err != nil {
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
						fmt.Printf("Progress round=%d eval=%d elapsed=%.1fs best=%.4f\n", round, evalNum, time.Since(start).Seconds(), bestMetrics.Score)
					}
					return m.Score
				}

				if _, err := runMayfly(mayflyConfig); err != nil {
					fmt.Fprintf(os.Stderr, "mayfly round %d failed: %v\n", round, err)
				}
			}
		}()
	}
	wg.Wait()

	state.mu.Lock()
	finalBest := cloneCandidate(state.best)
	finalMetrics := state.bestMetrics
	finalCheckpoints := state.checkpoints
	state.mu.Unlock()

	return &optimizationResult{
		best:        finalBest,
		bestMetrics: finalMetrics,
		evals:       int(atomic.LoadInt64(&evals)),
		elapsed:     time.Since(start).Seconds(),
		checkpoints: finalCheckpoints,
	}, nil
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
	score := state.bestMetrics.Score
	state.mu.Unlock()
	return score
}

func cloneCandidate(c candidate) candidate {
	vals := make([]float64, len(c.Vals))
	copy(vals, c.Vals)
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
