package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/cwbudde/mayfly"
	"github.com/cwbudde/qmc"
)

// searchMode selects where candidate positions come from.
//
// The two sampler modes are not alternatives anyone should fit with — they are
// the control conditions the optimizer has to beat. A stochastic search that
// does not outperform a low-discrepancy sequence at the same evaluation budget
// is not paying for itself, and PLAN.md:1016 already records one round of
// piano-fit runs losing to the deterministic Halton sweep. Without a control
// in the same harness, on the same objective and the same budget, that
// comparison stays anecdotal.
type searchMode string

const (
	// searchMayfly is the production search and the default.
	searchMayfly searchMode = "mayfly"
	// searchRandom draws uniform points in the unit hypercube.
	searchRandom searchMode = "random"
	// searchHalton walks the same Halton sequence the sweep's joint stage
	// uses, so the control is deterministic and reproducible from the flags.
	searchHalton searchMode = "halton"
)

func parseSearchMode(raw string) (searchMode, error) {
	switch searchMode(raw) {
	case searchMayfly, searchRandom, searchHalton:
		return searchMode(raw), nil
	default:
		return "", fmt.Errorf("unknown search mode %q (valid: mayfly, random, halton)", raw)
	}
}

// searchOptions carries the search knobs the optimizer audit exposed. Every
// zero value means "behave exactly as this tool behaved before the audit", so
// a run that sets none of them reproduces every tracked report bit for bit.
// That invariant is what protects the recorded history, and
// TestSearchOptionsZeroValueIsTodaysBehaviour pins it.
type searchOptions struct {
	// mode selects the sampler. The empty string means searchMayfly.
	mode searchMode
	// iters overrides the per-round iteration count. 0 keeps the derived
	// value; see roundIterations for why the derivation is wrong.
	iters int
	// warmStart seeds the swarm with the incumbent instead of starting every
	// round from a uniformly random population.
	warmStart bool
	// ncRatio replaces the hardcoded NC = 2*pop offspring count with
	// round(ncRatio*pop). 0 keeps the hardcoded value.
	ncRatio float64
	// danceDamp, flDamp and gDamp override the library's annealing schedule.
	// 0 keeps whatever the variant constructor set.
	danceDamp float64
	flDamp    float64
	gDamp     float64
	// stagnation ends a round after this many iterations without improvement,
	// via the library's own ConvergenceConfig. 0 disables early stopping.
	stagnation int
}

func (o searchOptions) effectiveMode() searchMode {
	if o.mode == "" {
		return searchMayfly
	}

	return o.mode
}

// roundIterations converts a per-round evaluation budget into an iteration
// count.
//
// The historical derivation is `budget / (2*pop)`, which assumes a round costs
// NPop + NPopF evaluations per iteration. It does not. The library also
// evaluates every crossover offspring and every mutant, and DESMA evaluates
// its elites on top:
//
//	NPop + NPopF + NC + NM + EliteCount = 10 + 10 + 20 + 1 + 5 = 46
//
// at this tool's own defaults, against the 20 the derivation assumes. So a
// nominal 240-evaluation round really wants ~5 iterations' worth of budget but
// is told to run 12, and reserveEval truncates it mid-search by poisoning the
// objective. The round never completes and its annealing schedule never
// finishes — with DanceDamp 0.8 the nuptial dance is already at 0.8^12 ≈ 0.07
// by the end of a round that then gets thrown away.
//
// This function keeps the historical arithmetic when iters is 0 so nothing
// moves by default, and lets --mayfly-iters set the count directly so the
// question "how long should a round be" can be measured instead of derived
// from an incorrect model.
func roundIterations(opts searchOptions, budget int, pop int) int {
	if opts.iters > 0 {
		return opts.iters
	}

	return maxInt(1, budget/(2*pop))
}

// applySearchOptions folds the audit overrides into a freshly built config.
// It is separate from newMayflyConfig so the historical constructor keeps its
// signature and its test.
func applySearchOptions(cfg *mayfly.Config, opts searchOptions, pop int) {
	if opts.ncRatio > 0 {
		cfg.NC = maxInt(2, int(math.Round(opts.ncRatio*float64(pop))))
	}
	if opts.danceDamp > 0 {
		cfg.DanceDamp = opts.danceDamp
	}
	if opts.flDamp > 0 {
		cfg.FLDamp = opts.flDamp
	}
	if opts.gDamp > 0 {
		cfg.GDamp = opts.gDamp
	}
	if opts.stagnation > 0 {
		cfg.Convergence = &mayfly.ConvergenceConfig{
			StagnationIterations: opts.stagnation,
			MinIterations:        1,
		}
	}
}

// warmStartOptions seeds one male and one female at the incumbent's position.
//
// Every round currently starts from a uniformly random population, so nothing
// the run has already learned enters the search: --preset chaining, --resume
// and the previous rounds' progress all survive only in the external best
// tracker and in the polish stage. The library supports seeding through
// WithInitialPopulation, but only via OptimizeContext — plain Optimize takes
// no run options, which is why this was never wired up.
//
// One of each and no more, deliberately. Filling the population with copies of
// the incumbent would collapse the swarm's diversity in the first iteration
// and turn a global search into an expensive local one.
func warmStartOptions(opts searchOptions, best candidate, defs []knobDef) []mayfly.RunOption {
	if !opts.warmStart {
		return nil
	}
	pos := toNormalized(best, defs)
	if len(pos) == 0 {
		return nil
	}
	seed := [][]float64{pos}

	return []mayfly.RunOption{mayfly.WithInitialPopulation(seed, seed)}
}

// runMayflyRound runs one round and reports what it actually cost.
//
// The historical call discarded the result entirely, which is why the
// evaluation-accounting error above went unnoticed for so long: the library
// reports FuncEvalCount, IterationCount and TerminationReason, and nothing
// read them.
func runMayflyRound(cfg *mayfly.Config, opts ...mayfly.RunOption) (_ *mayfly.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mayfly panic: %v", r)
		}
	}()

	// context.Background, not a deadline-bound context: OptimizeContext
	// discards its partial result on cancellation, so cancelling a round would
	// throw away everything it found. The deadline is enforced inside the
	// objective instead, which is what the pre-audit code already did.
	return mayfly.OptimizeContext(context.Background(), cfg, opts...)
}

// roundStats accumulates what the mayfly rounds reported, so a run can state
// its real per-round cost instead of the assumed one.
type roundStats struct {
	rounds     atomic.Int64
	funcEvals  atomic.Int64
	iterations atomic.Int64
}

func (s *roundStats) observe(res *mayfly.Result) {
	if res == nil {
		return
	}
	s.rounds.Add(1)
	s.funcEvals.Add(int64(res.FuncEvalCount))
	s.iterations.Add(int64(res.IterationCount))
}

// evalsPerIteration is the measured cost this run actually paid, or 0 when no
// round reported anything. It is the number the historical derivation gets
// wrong.
func (s *roundStats) evalsPerIteration() float64 {
	iterations := s.iterations.Load()
	if iterations == 0 {
		return 0
	}

	return float64(s.funcEvals.Load()) / float64(iterations)
}

// samplerRun drives a control-condition search until the budget or the
// deadline runs out. It feeds the same objective the mayfly path feeds, so
// budget accounting, best-tracking, constraint penalties and tracing are
// identical and the search strategy is the only thing that differs.
type samplerRun struct {
	mode      searchMode
	dims      int
	seed      int64
	workerID  int
	deadline  time.Time
	evals     *int64
	maxEvals  int
	index     *atomic.Int64
	objective func([]float64) float64
	// halton is shared across workers. qmc's AtInto is stateless, so the
	// workers index one sequence concurrently instead of each rebuilding the
	// permutation tables. Nil for non-Halton modes.
	halton *qmc.Halton
}

// samplerHaltonBurnIn discards the opening points of the Halton sequence, the
// same burn-in --sweep-joint-skip defaults to. Point 1 is (1/2, 1/3, 1/5, ...),
// which is a corner of the box in every coordinate with a large base; starting
// there spends the first evaluations on a region the sequence would have
// covered anyway.
const samplerHaltonBurnIn = 64

// newSamplerHalton builds the generator for the Halton control.
//
// Scrambling is on, and that is a deliberate change of character: the control
// becomes a randomized quasi-Monte Carlo sequence and no longer returns
// identical points for every seed. Unscrambled Halton does not fill a
// high-dimensional box at the budgets this tool runs at — measured over 39
// knobs and 600 points, adjacent coordinates correlate at 0.81, because the
// last dimensions have not left their first period and are still walking a
// linear ramp. A control that is not filling the box makes the optimizer look
// better than it is, which is the opposite of what a control is for.
func newSamplerHalton(dims int, seed int64) (*qmc.Halton, error) {
	return qmc.NewHalton(
		dims,
		qmc.WithSkip(samplerHaltonBurnIn),
		qmc.WithScrambling(uint64(seed)),
	)
}

func (r samplerRun) run() error {
	rng := rand.New(rand.NewSource(r.seed + int64(r.workerID)*7919))
	pos := make([]float64, r.dims)

	for {
		if time.Now().After(r.deadline) {
			return nil
		}
		if atomic.LoadInt64(r.evals) >= int64(r.maxEvals) {
			return nil
		}

		// The Halton index is shared across workers so parallel workers walk
		// disjoint parts of one sequence rather than each replaying it.
		idx := r.index.Add(1)
		switch r.mode {
		case searchHalton:
			// idx counts from 1; the sequence counts from 0.
			r.halton.AtInto(int(idx)-1, pos)
		case searchRandom:
			for d := range pos {
				pos[d] = rng.Float64()
			}
		case searchMayfly:
			return fmt.Errorf("samplerRun: mode %q is not a sampler", r.mode)
		}

		r.objective(pos)
	}
}
