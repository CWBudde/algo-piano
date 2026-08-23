package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cwbudde/mayfly"
)

// Stage 0 of the optimizer audit: a render-free screening sweep.
//
// A real evaluation renders and analyses audio at roughly 10-22 evals/s, so a
// matrix of eighteen configurations across four dimensionalities and twenty
// seeds would cost days. The same matrix against a closed-form objective costs
// a couple of minutes, because cfg.evaluator lets the search run without ever
// touching the synth. What that buys is not an answer about the piano — a
// synthetic landscape is not the piano landscape — but a way to cut hundreds of
// candidate configurations down to the handful worth spending real renders on,
// and to settle the mechanical questions (does a round finish, does warm start
// reach the swarm, what does an iteration actually cost) with statistics
// instead of code reading.
//
// It is opt-in: OPT_SCREEN=1 go test -run TestOptimizerScreening ./cmd/piano-fit/
// Set OPT_SCREEN_OUT to also write the markdown table to a file.

// screenObjective is a synthetic landscape expressed over the same normalized
// unit box the optimizer searches.
//
// The optimum deliberately does not sit at the centre or a corner of the box.
// A centred optimum flatters every symmetric sampler — Halton's early points
// cluster near the middle, and a swarm initialised uniformly averages there on
// iteration one — so a centred target would measure the box, not the search.
type screenObjective struct {
	name string
	// span is the width of the x-domain each normalized axis maps onto.
	span float64
	// optimum is the x-space coordinate of the global minimum, the same in
	// every dimension for all three functions used here.
	optimum float64
	fn      func([]float64) float64
}

func screenObjectives() []screenObjective {
	return []screenObjective{
		// Unimodal and separable: the easy case. If a search cannot beat
		// Halton here it cannot beat it anywhere.
		{name: "sphere", span: 10.24, optimum: 0, fn: mayfly.Sphere},
		// Multimodal and separable: punishes a search that anneals to a
		// standstill before it has left its starting basin.
		{name: "rastrigin", span: 10.24, optimum: 0, fn: mayfly.Rastrigin},
		// Unimodal but ill-conditioned with a curved valley: punishes a search
		// that cannot follow a narrow ridge, which is what the piano's
		// correlated knobs look like.
		{name: "rosenbrock", span: 4.096, optimum: 1, fn: mayfly.Rosenbrock},
	}
}

// screenTargets places the optimum at an irrational-ish normalized offset per
// axis, so no axis shares its optimum with another and none lands on a value a
// low-discrepancy sequence visits early.
func screenTargets(dims int) []float64 {
	t := make([]float64, dims)
	for i := range t {
		frac := math.Mod(0.37+float64(i)*0.6180339887498949, 1.0)
		t[i] = 0.1 + 0.8*frac
	}

	return t
}

// screenDefs builds dims knobs over the plain unit interval, so a candidate's
// normalized position and its value coincide and the mapping adds no scaling
// of its own to the measurement.
func screenDefs(dims int) []knobDef {
	defs := make([]knobDef, dims)
	for i := range defs {
		defs[i] = knobDef{Name: fmt.Sprintf("x%02d", i), Min: 0, Max: 1}
	}

	return defs
}

func screenEvaluator(obj screenObjective, defs []knobDef) candidateEvaluator {
	targets := screenTargets(len(defs))
	x := make([]float64, len(defs))

	return func(c candidate, _ string, _ evalSettings) (optimizationEval, error) {
		pos := toNormalized(c, defs)
		for i := range pos {
			x[i] = obj.optimum + (pos[i]-targets[i])*obj.span
		}
		score := obj.fn(x)

		return optimizationEval{
			aggregate: score,
			notes:     []noteReport{{Note: 60, Score: score}},
		}, nil
	}
}

// screenConfig is one row of the matrix: a name plus the deviation from
// today's defaults it applies. Every axis is varied one at a time from the
// baseline, which is the same discipline Stage B applies to real audio — a
// full cross product would not tell us which change earned the difference.
type screenConfig struct {
	name string
	// apply mutates a baseline optimizationConfig. pop is passed separately
	// because the round-length overrides depend on it.
	apply func(cfg *optimizationConfig)
}

func screenConfigs(maxEvals int) []screenConfig {
	base := func(*optimizationConfig) {}
	mode := func(m searchMode) func(*optimizationConfig) {
		return func(cfg *optimizationConfig) { cfg.search.mode = m }
	}
	iters := func(n int) func(*optimizationConfig) {
		return func(cfg *optimizationConfig) { cfg.search.iters = n }
	}

	configs := []screenConfig{
		{"baseline", base},
		{"random", mode(searchRandom)},
		{"halton", mode(searchHalton)},
		{"warm-start", func(cfg *optimizationConfig) { cfg.search.warmStart = true }},
		{"iters-40", iters(40)},
		{"iters-120", iters(120)},
		// One round for the whole budget: an iteration costs at least one
		// evaluation, so an iteration count equal to the budget can never be
		// reached and the round is only ever ended by the budget running out.
		// That asks what happens when the search is never restarted at all.
		// Not spelled 1<<20, because the library sizes its convergence curve
		// from MaxIterations and an absurd count buys an absurd allocation.
		{"iters-single-round", iters(maxEvals)},
		{"stagnation-15", func(cfg *optimizationConfig) { cfg.search.stagnation = 15 }},
		{"dance-damp-0.95", func(cfg *optimizationConfig) { cfg.search.danceDamp = 0.95 }},
		{"dance-damp-0.99", func(cfg *optimizationConfig) { cfg.search.danceDamp = 0.99 }},
		{"nc-ratio-1", func(cfg *optimizationConfig) { cfg.search.ncRatio = 1 }},
		{"pop-20", func(cfg *optimizationConfig) { cfg.mayflyPop = 20 }},
		{"pop-40", func(cfg *optimizationConfig) { cfg.mayflyPop = 40 }},
		// Warm start plus a round long enough to use it: the two findings are
		// coupled, because seeding a swarm that is discarded twelve iterations
		// later cannot compound.
		{"warm+iters-120", func(cfg *optimizationConfig) {
			cfg.search.warmStart = true
			cfg.search.iters = 120
		}},
	}

	for _, v := range []string{"ma", "desma", "olce", "eobbma", "gsasma", "mpma", "aoblmoa"} {
		configs = append(configs, screenConfig{
			name:  "variant-" + v,
			apply: func(cfg *optimizationConfig) { cfg.mayflyVariant = v },
		})
	}

	return configs
}

func screenBaseConfig(t *testing.T, dims int, obj screenObjective, seed int64, maxEvals int) *optimizationConfig {
	t.Helper()
	dir := t.TempDir()
	defs := screenDefs(dims)
	outputPreset := filepath.Join(dir, "fitted.json")
	if err := os.WriteFile(outputPreset, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed output preset: %v", err)
	}

	// The seed candidate sits at the box corner for every axis, so no
	// configuration starts closer to the optimum than another.
	vals := make([]float64, dims)

	return &optimizationConfig{
		targets:          []noteTarget{{note: 60, referencePath: "synthetic", weight: 1.0}},
		aggregate:        aggregateMean,
		defs:             defs,
		initCandidate:    candidate{Vals: vals},
		baseVelocity:     100,
		baseReleaseAfter: 0.2,
		sampleRate:       8000,
		finalSampleRate:  8000,
		seed:             seed,
		timeBudget:       3600,
		maxEvals:         maxEvals,
		mayflyVariant:    "desma",
		mayflyPop:        10,
		mayflyRoundEvals: 240,
		workers:          1,
		topK:             5,
		refineTopK:       1,
		groups:           map[string]bool{"piano": true},
		workDir:          dir,
		outputPreset:     outputPreset,
		evaluator:        screenEvaluator(obj, defs),
	}
}

type screenResult struct {
	objective string
	dims      int
	config    string
	seed      int64
	best      float64
	evalsIter float64
}

func TestOptimizerScreening(t *testing.T) {
	if os.Getenv("OPT_SCREEN") == "" {
		t.Skip("set OPT_SCREEN=1 to run the render-free optimizer screening sweep")
	}

	// The budget is overridable because the round-length question is
	// budget-dependent by construction: at a small budget "longer rounds"
	// and "never restart" are the same configuration, and only a budget
	// several rounds long can separate them.
	maxEvals := screenEnvInt("OPT_SCREEN_EVALS", 1500)
	seeds := screenEnvInt("OPT_SCREEN_SEEDS", 20)
	dimsList := []int{5, 9, 20, 30}
	objectives := screenObjectives()
	configs := screenConfigs(maxEvals)

	// runOptimization narrates every improvement on stdout. Across a few
	// thousand runs that output is both useless and enormous, and it would
	// dominate the wall time of a search that otherwise costs microseconds.
	restore := silenceStdout(t)
	defer restore()

	var mu sync.Mutex
	results := make([]screenResult, 0, len(objectives)*len(dimsList)*len(configs)*seeds)

	var wg sync.WaitGroup
	sem := make(chan struct{}, screenParallelism())
	for _, obj := range objectives {
		for _, dims := range dimsList {
			for _, cf := range configs {
				for seed := int64(1); seed <= int64(seeds); seed++ {
					wg.Add(1)
					sem <- struct{}{}
					go func(obj screenObjective, dims int, cf screenConfig, seed int64) {
						defer wg.Done()
						defer func() { <-sem }()

						cfg := screenBaseConfig(t, dims, obj, seed, maxEvals)
						cf.apply(cfg)
						res, err := runOptimization(cfg)
						if err != nil {
							t.Errorf("%s/%dd/%s/seed%d: %v", obj.name, dims, cf.name, seed, err)
							return
						}
						mu.Lock()
						results = append(results, screenResult{
							objective: obj.name,
							dims:      dims,
							config:    cf.name,
							seed:      seed,
							best:      res.bestScore,
							evalsIter: res.evalsPerIteration,
						})
						mu.Unlock()
					}(obj, dims, cf, seed)
				}
			}
		}
	}
	wg.Wait()

	report := renderScreenReport(results, configs, objectives, dimsList, maxEvals, seeds)
	restore()
	fmt.Print(report)
	if path := os.Getenv("OPT_SCREEN_OUT"); path != "" {
		if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
			t.Fatalf("write screening report: %v", err)
		}
	}
}

// screenEnvInt reads a positive integer override, falling back to def.
func screenEnvInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}

	return n
}

// screenParallelism keeps two cores free, matching the convention the real
// bench driver uses, so a screening run does not starve the machine.
func screenParallelism() int {
	n := maxInt(1, runtime.GOMAXPROCS(0)-2)

	return n
}

// silenceStdout redirects os.Stdout for the duration of the sweep. It returns
// an idempotent restore so the caller can print the report to the real stdout.
func silenceStdout(t *testing.T) func() {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	saved := os.Stdout
	os.Stdout = devnull
	var once sync.Once

	return func() {
		once.Do(func() {
			os.Stdout = saved
			_ = devnull.Close()
		})
	}
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}

	return 0.5 * (s[mid-1] + s[mid])
}

// renderScreenReport formats the sweep as markdown: one table per objective,
// configurations down the side and dimensionality across, cells holding the
// median best score over seeds. Medians, not means: a single seed that stalls
// in a Rastrigin basin would otherwise move a whole row.
func renderScreenReport(
	results []screenResult,
	configs []screenConfig,
	objectives []screenObjective,
	dimsList []int,
	maxEvals, seeds int,
) string {
	byKey := map[string][]float64{}
	evalsIter := map[string][]float64{}
	key := func(objName string, dims int, config string) string {
		return fmt.Sprintf("%s|%d|%s", objName, dims, config)
	}
	for _, r := range results {
		k := key(r.objective, r.dims, r.config)
		byKey[k] = append(byKey[k], r.best)
		if r.evalsIter > 0 {
			evalsIter[r.config] = append(evalsIter[r.config], r.evalsIter)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Stage 0 — render-free optimizer screening\n\n")
	fmt.Fprintf(&b, "Budget %d evaluations, %d seeds per cell, one worker per run, "+
		"lower is better. Cells are medians over seeds.\n\n", maxEvals, seeds)
	// The path is absolute in the recipe because `go test` runs with the
	// package directory as its working directory, so a repo-relative
	// OPT_SCREEN_OUT would land under cmd/piano-fit.
	b.WriteString("Regenerate with `just opt-screen` (or `OPT_SCREEN=1 " +
		"OPT_SCREEN_OUT=$PWD/docs/optimizer-screening.md go test -run " +
		"TestOptimizerScreening ./cmd/piano-fit/`).\n\n")

	for _, obj := range objectives {
		fmt.Fprintf(&b, "## %s\n\n| config |", obj.name)
		for _, d := range dimsList {
			fmt.Fprintf(&b, " %dd |", d)
		}
		b.WriteString("\n|---|")
		for range dimsList {
			b.WriteString("---:|")
		}
		b.WriteString("\n")

		for _, cf := range configs {
			fmt.Fprintf(&b, "| %s |", cf.name)
			for _, d := range dimsList {
				fmt.Fprintf(&b, " %.4g |", median(byKey[key(obj.name, d, cf.name)]))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Measured evaluations per mayfly iteration\n\n")
	b.WriteString("The round-length derivation assumes 20. Anything above that means rounds are " +
		"truncated mid-search rather than completing.\n\n| config | median evals/iteration |\n|---|---:|\n")
	for _, cf := range configs {
		if vals := evalsIter[cf.name]; len(vals) > 0 {
			fmt.Fprintf(&b, "| %s | %.1f |\n", cf.name, median(vals))
		}
	}

	return b.String()
}
