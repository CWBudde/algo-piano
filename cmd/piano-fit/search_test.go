package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// searchTestDefs is a small, well-conditioned box: three linear knobs, so a
// normalized position maps to knob values and back without rounding.
func searchTestDefs() []knobDef {
	return []knobDef{
		{Name: "a", Min: 0.0, Max: 1.0},
		{Name: "b", Min: -2.0, Max: 2.0},
		{Name: "c", Min: 10.0, Max: 20.0},
	}
}

// searchTestConfig builds a render-free optimization: the synthetic quadratic
// objective from polish_test.go stands in for the real evaluator, so a whole
// search costs microseconds instead of the ~70 ms per evaluation a real render
// costs. That substitution is the reason cfg.evaluator exists.
func searchTestConfig(t *testing.T, defs []knobDef, target []float64) *optimizationConfig {
	t.Helper()
	dir := t.TempDir()
	outputPreset := filepath.Join(dir, "fitted.json")
	// Pre-create it: runOptimization writes an initial preset only when the
	// output does not exist yet, and the synthetic evaluator produces no
	// piano.Params for it to write.
	if err := os.WriteFile(outputPreset, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed output preset: %v", err)
	}

	vals := make([]float64, len(defs))
	for i := range defs {
		vals[i] = defs[i].Min
	}

	return &optimizationConfig{
		targets:          []noteTarget{{note: 60, referencePath: "synthetic", weight: 1.0}},
		aggregate:        aggregateMean,
		defs:             defs,
		initCandidate:    candidate{Vals: vals},
		baseVelocity:     100,
		baseReleaseAfter: 0.2,
		sampleRate:       8000,
		finalSampleRate:  8000,
		seed:             1,
		timeBudget:       60,
		maxEvals:         200,
		mayflyVariant:    "desma",
		mayflyPop:        10,
		mayflyRoundEvals: 240,
		workers:          1,
		topK:             5,
		refineTopK:       1,
		groups:           map[string]bool{"piano": true},
		workDir:          dir,
		outputPreset:     outputPreset,
		evaluator:        quadraticEvaluator(defs, target, nil),
	}
}

// TestSearchOptionsZeroValueIsTodaysBehaviour is the invariant that protects
// every tracked report: a run that sets none of the audit flags must configure
// the optimizer exactly as it was configured before those flags existed.
func TestSearchOptionsZeroValueIsTodaysBehaviour(t *testing.T) {
	var zero searchOptions

	if got := zero.effectiveMode(); got != searchMayfly {
		t.Fatalf("effectiveMode() = %q, want %q", got, searchMayfly)
	}

	// The historical derivation: budget / (2*pop).
	if got, want := roundIterations(zero, 240, 10), 12; got != want {
		t.Fatalf("roundIterations = %d, want the historical %d", got, want)
	}

	want, err := newMayflyConfig("desma", 10, 5, 12)
	if err != nil {
		t.Fatalf("newMayflyConfig: %v", err)
	}
	got, err := newMayflyConfig("desma", 10, 5, 12)
	if err != nil {
		t.Fatalf("newMayflyConfig: %v", err)
	}
	applySearchOptions(got, zero, 10)

	if !reflect.DeepEqual(got, want) {
		t.Fatal("zero-valued searchOptions modified the mayfly config")
	}
	if opts := warmStartOptions(zero, candidate{Vals: []float64{0.5}}, searchTestDefs()[:1]); opts != nil {
		t.Fatalf("zero-valued searchOptions produced %d run options, want none", len(opts))
	}
}

func TestSearchOptionsOverrides(t *testing.T) {
	opts := searchOptions{
		iters:      40,
		ncRatio:    1.0,
		danceDamp:  0.99,
		flDamp:     0.95,
		gDamp:      0.98,
		stagnation: 7,
	}

	if got := roundIterations(opts, 240, 10); got != 40 {
		t.Fatalf("roundIterations = %d, want the override 40", got)
	}

	cfg, err := newMayflyConfig("desma", 10, 5, 12)
	if err != nil {
		t.Fatalf("newMayflyConfig: %v", err)
	}
	if cfg.NC != 20 {
		t.Fatalf("precondition: NC = %d, want the hardcoded 2*pop = 20", cfg.NC)
	}
	applySearchOptions(cfg, opts, 10)

	if cfg.NC != 10 {
		t.Fatalf("NC = %d, want round(1.0*10) = 10", cfg.NC)
	}
	if cfg.DanceDamp != 0.99 || cfg.FLDamp != 0.95 || cfg.GDamp != 0.98 {
		t.Fatalf("damping = (%v, %v, %v), want (0.99, 0.95, 0.98)", cfg.DanceDamp, cfg.FLDamp, cfg.GDamp)
	}
	if cfg.Convergence == nil || cfg.Convergence.StagnationIterations != 7 {
		t.Fatalf("Convergence = %+v, want StagnationIterations 7", cfg.Convergence)
	}
}

func TestParseSearchMode(t *testing.T) {
	for _, raw := range []string{"mayfly", "random", "halton"} {
		if _, err := parseSearchMode(raw); err != nil {
			t.Fatalf("parseSearchMode(%q): %v", raw, err)
		}
	}
	if _, err := parseSearchMode("annealing"); err == nil {
		t.Fatal("parseSearchMode accepted an unknown mode")
	}
}

// TestWarmStartEvaluatesTheIncumbent pins the mechanism, not a statistic: the
// seeded position has to appear in the very first round's evaluations. Without
// this the flag could be silently inert — which is exactly what the production
// path was, because mayfly.Optimize takes no run options at all.
func TestWarmStartEvaluatesTheIncumbent(t *testing.T) {
	defs := searchTestDefs()
	incumbent := candidate{Vals: []float64{0.25, 1.0, 17.5}}
	want := toNormalized(incumbent, defs)

	cfg, err := newMayflyConfig("ma", 4, len(defs), 1)
	if err != nil {
		t.Fatalf("newMayflyConfig: %v", err)
	}

	var mu sync.Mutex
	var seen [][]float64
	cfg.ObjectiveFunc = func(pos []float64) float64 {
		mu.Lock()
		seen = append(seen, append([]float64(nil), pos...))
		mu.Unlock()

		return 1.0
	}

	opts := warmStartOptions(searchOptions{warmStart: true}, incumbent, defs)
	if len(opts) == 0 {
		t.Fatal("warm start produced no run options")
	}
	if _, err := runMayflyRound(cfg, opts...); err != nil {
		t.Fatalf("runMayflyRound: %v", err)
	}

	for _, pos := range seen {
		if len(pos) == len(want) && positionsEqual(pos, want) {
			return
		}
	}
	t.Fatalf("the incumbent %v was never evaluated across %d positions", want, len(seen))
}

func positionsEqual(a, b []float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-12 {
			return false
		}
	}

	return true
}

// TestSamplerModesRespectTheEvalBudget checks the control conditions consume
// exactly the budget they were given. A control that spends a different number
// of evaluations than the optimizer it is compared against measures nothing.
func TestSamplerModesRespectTheEvalBudget(t *testing.T) {
	defs := searchTestDefs()
	target := []float64{0.3, 0.7, 0.5}

	for _, mode := range []searchMode{searchRandom, searchHalton} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := searchTestConfig(t, defs, target)
			cfg.search = searchOptions{mode: mode}

			res, err := runOptimization(cfg)
			if err != nil {
				t.Fatalf("runOptimization: %v", err)
			}
			if res.evals != cfg.maxEvals {
				t.Fatalf("evals = %d, want exactly the budget %d", res.evals, cfg.maxEvals)
			}
			if res.bestScore > cfg.evaluatorScoreAt(t, cfg.initCandidate) {
				t.Fatal("the sampler returned a result worse than the seed it started from")
			}
		})
	}
}

// evaluatorScoreAt scores one candidate with the configured evaluator, so a
// test can state "no worse than the seed" without duplicating the objective.
func (cfg *optimizationConfig) evaluatorScoreAt(t *testing.T, c candidate) float64 {
	t.Helper()
	ev, err := cfg.evaluator(c, "", evalSettings{})
	if err != nil {
		t.Fatalf("evaluator: %v", err)
	}

	return ev.aggregate
}

// TestHaltonSearchIsDeterministic pins the property that makes the Halton
// control usable as a baseline: it carries no RNG, so the same flags produce
// the same answer and a comparison against it needs no seed averaging.
func TestHaltonSearchIsDeterministic(t *testing.T) {
	defs := searchTestDefs()
	target := []float64{0.3, 0.7, 0.5}

	run := func(seed int64) float64 {
		cfg := searchTestConfig(t, defs, target)
		cfg.search = searchOptions{mode: searchHalton}
		cfg.seed = seed
		res, err := runOptimization(cfg)
		if err != nil {
			t.Fatalf("runOptimization: %v", err)
		}

		return res.bestScore
	}

	first, second := run(1), run(1)
	if first != second {
		t.Fatalf("halton is not reproducible: %v then %v", first, second)
	}
	// The seed must not matter either — a Halton run that moved with --seed
	// would be a random search wearing a deterministic name.
	if other := run(7); other != first {
		t.Fatalf("halton moved with the seed: %v at seed 1, %v at seed 7", first, other)
	}
}

// TestMeasuredEvalsPerIterationExceedsTheDerivation is the audit's headline
// finding as an assertion. The round-length derivation assumes a round costs
// NPop+NPopF = 2*pop evaluations per iteration; the library also evaluates
// crossover offspring, mutants and DESMA elites. If this test ever starts
// failing, the derivation has become correct and roundIterations' comment
// needs to say so.
func TestMeasuredEvalsPerIterationExceedsTheDerivation(t *testing.T) {
	defs := searchTestDefs()
	cfg := searchTestConfig(t, defs, []float64{0.3, 0.7, 0.5})
	cfg.maxEvals = 2000

	res, err := runOptimization(cfg)
	if err != nil {
		t.Fatalf("runOptimization: %v", err)
	}

	assumed := float64(2 * cfg.mayflyPop)
	if res.evalsPerIteration <= assumed {
		t.Fatalf("measured %.1f evals/iteration, expected more than the assumed %.0f",
			res.evalsPerIteration, assumed)
	}
	t.Logf("measured %.1f evals/iteration against an assumed %.0f (%.2fx)",
		res.evalsPerIteration, assumed, res.evalsPerIteration/assumed)
}

// TestTraceRecordsEveryEvaluation keeps the trace honest: a convergence curve
// drawn from a trace that quietly dropped records would understate how many
// evaluations the run spent reaching a score.
func TestTraceRecordsEveryEvaluation(t *testing.T) {
	defs := searchTestDefs()
	cfg := searchTestConfig(t, defs, []float64{0.3, 0.7, 0.5})
	cfg.search = searchOptions{mode: searchRandom}
	cfg.tracePath = filepath.Join(t.TempDir(), "trace.jsonl")

	res, err := runOptimization(cfg)
	if err != nil {
		t.Fatalf("runOptimization: %v", err)
	}

	file, err := os.Open(cfg.tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = file.Close() }()

	count := 0
	var last traceRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &last); err != nil {
			t.Fatalf("trace line %d is not valid JSON: %v", count+1, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan trace: %v", err)
	}

	// The seed evaluation is scored before the workers start and is not part
	// of the search, so the trace carries one record fewer than res.evals.
	if want := res.evals - 1; count != want {
		t.Fatalf("trace has %d records, want %d", count, want)
	}
	if last.Best > last.Aggregate && last.Best != res.bestScore {
		t.Fatalf("final trace record disagrees with the result: best %v, result %v", last.Best, res.bestScore)
	}
}

// TestEvaluatorInjectionIsUsed pins that a configured evaluator really
// replaces the render path. The whole synthetic benchmark rests on it.
func TestEvaluatorInjectionIsUsed(t *testing.T) {
	defs := searchTestDefs()
	cfg := searchTestConfig(t, defs, []float64{0.3, 0.7, 0.5})
	cfg.search = searchOptions{mode: searchRandom}
	cfg.maxEvals = 25

	var mu sync.Mutex
	calls := 0
	inner := cfg.evaluator
	cfg.evaluator = func(c candidate, scratch string, settings evalSettings) (optimizationEval, error) {
		mu.Lock()
		calls++
		mu.Unlock()

		return inner(c, scratch, settings)
	}

	// The targets carry no reference audio at all, so a run that reached the
	// real evaluator would fail rather than return — reaching the end is
	// already most of the proof. The count check adds the rest: at least the
	// whole eval budget went through the injection. It is ">=" because the
	// refine-top-k pass re-scores the winner at final settings after the
	// search, outside the search budget.
	if _, err := runOptimization(cfg); err != nil {
		t.Fatalf("runOptimization: %v", err)
	}
	if calls < cfg.maxEvals {
		t.Fatalf("injected evaluator called %d times, want at least the budget %d", calls, cfg.maxEvals)
	}
}

// TestSamplerFailureFailsTheRun pins that a search which cannot run at all
// fails the invocation instead of reporting the seed candidate as a result.
//
// This was not hypothetical. --search halton on the joint
// piano,body-ir,room-ir,mix selection exceeded the Halton base table, printed a
// warning, and exited 0 with a report holding one evaluation — which then
// entered a benchmark table as if it were a finished 600-evaluation run.
func TestSamplerFailureFailsTheRun(t *testing.T) {
	dims := len(haltonPrimes) + 1
	defs := make([]knobDef, dims)
	for i := range defs {
		defs[i] = knobDef{Name: fmt.Sprintf("x%02d", i), Min: 0, Max: 1}
	}
	target := make([]float64, dims)

	cfg := searchTestConfig(t, defs, target)
	cfg.search.mode = searchHalton

	if _, err := runOptimization(cfg); err == nil {
		t.Fatal("a sampler that cannot produce points must fail the run, not report the seed candidate")
	}
}
