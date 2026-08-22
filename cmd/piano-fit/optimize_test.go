package main

import (
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

func TestNewMayflyConfig(t *testing.T) {
	tests := []struct {
		variant string
		wantErr bool
	}{
		{variant: "ma"},
		{variant: "desma"},
		{variant: "olce"},
		{variant: "eobbma"},
		{variant: "gsasma"},
		{variant: "mpma"},
		{variant: "aoblmoa"},
		{variant: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.variant, func(t *testing.T) {
			cfg, err := newMayflyConfig(tt.variant, 10, 5, 20)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("newMayflyConfig(%q) expected error", tt.variant)
				}
				return
			}
			if err != nil {
				t.Fatalf("newMayflyConfig(%q) unexpected error: %v", tt.variant, err)
			}
			if cfg.ProblemSize != 5 {
				t.Fatalf("ProblemSize = %d, want 5", cfg.ProblemSize)
			}
			if cfg.NPop != 10 {
				t.Fatalf("NPop = %d, want 10", cfg.NPop)
			}
			if cfg.MaxIterations != 20 {
				t.Fatalf("MaxIterations = %d, want 20", cfg.MaxIterations)
			}
		})
	}
}

func TestReserveEvalCapsAtMax(t *testing.T) {
	const (
		maxEvals = 47
		workers  = 8
	)

	var evals int64
	var granted int64
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, ok := reserveEval(&evals, maxEvals); !ok {
					return
				}
				atomic.AddInt64(&granted, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&granted); got != maxEvals {
		t.Fatalf("granted evaluations = %d, want %d", got, maxEvals)
	}
	if got := atomic.LoadInt64(&evals); got != maxEvals {
		t.Fatalf("eval counter = %d, want %d", got, maxEvals)
	}
}

// constrainedRunConfig builds a fast, fully deterministic constrained run:
// --polish-only with no polish budget, so the seed candidate is scored once at
// final settings and handed straight to the gain match. It renders synthetic
// audio only and needs no reference WAV on disk.
//
// decayRelative is a parameter because it decides whether the gain match can
// move a score at all: with the relative auto-stop the render length no longer
// depends on the absolute level, and with the absolute one it does.
func constrainedRunConfig(t *testing.T, max float64, matchGain, decayRelative bool) *optimizationConfig {
	t.Helper()
	cfg := testConfig(t, []int{60})
	s := testEvalSettings()
	cfg.minDuration, cfg.maxDuration = s.minDuration, s.maxDuration
	cfg.finalMinDuration, cfg.finalMaxDuration = s.minDuration, s.maxDuration
	cfg.decayDBFS, cfg.decayHoldBlocks = s.decayDBFS, s.decayHoldBlocks
	cfg.decayRelative = decayRelative
	cfg.renderBlockSize = s.renderBlockSize

	dir := t.TempDir()
	cfg.workDir = dir
	cfg.outputPreset = filepath.Join(dir, "fitted.json")
	cfg.mayflyVariant = "mpma"
	cfg.maxEvals = 1
	cfg.timeBudget = 30
	cfg.polishOnly = true
	cfg.matchOutputGain = matchGain
	cfg.scoreConstraints = legacyConstraint(t, max)
	cfg.constraintRejects = new(atomic.Int64)
	return cfg
}

func finalSettingsFor(cfg *optimizationConfig) evalSettings {
	return evalSettings{
		final:           true,
		sampleRate:      cfg.finalSampleRate,
		minDuration:     cfg.finalMinDuration,
		maxDuration:     cfg.finalMaxDuration,
		decayDBFS:       cfg.decayDBFS,
		decayHoldBlocks: cfg.decayHoldBlocks,
		decayRelative:   cfg.decayRelative,
		renderBlockSize: cfg.renderBlockSize,
	}
}

// runConstrained runs one constrained optimization and returns its result.
func runConstrained(t *testing.T, cfg *optimizationConfig) *optimizationResult {
	t.Helper()
	res, err := runOptimization(cfg)
	if err != nil {
		t.Fatalf("runOptimization: %v", err)
	}
	return res
}

// remeasureConstraint scores the params a run actually wrote, so a test can ask
// whether the reported constrained score describes the written preset.
func remeasureConstraint(t *testing.T, cfg *optimizationConfig, res *optimizationResult) float64 {
	t.Helper()
	// The measurement must not count as a rejection or penalise anything, so it
	// runs against a copy of the config with the shared counter detached.
	probe := *cfg
	probe.constraintRejects = new(atomic.Int64)
	ev, err := scoreParams(
		&probe, res.bestParams, res.bestBodyIR, res.bestRoomIRL, res.bestRoomIRR,
		res.bestVelocity, res.bestReleaseAfter, finalSettingsFor(cfg),
	)
	if err != nil {
		t.Fatalf("scoreParams: %v", err)
	}
	return ev.constraintScores[analysis.ProfileLegacyV1]
}

// TestConstraintScoresDescribeTheWrittenPreset is the run-level half of the
// post-gain-match re-check: whatever best_constraint_scores reports has to be a
// measurement of the params that end up in the preset, not of the candidate as
// the search last saw it. It runs with the ABSOLUTE auto-stop, the mode in
// which the gain match can still move a score.
func TestConstraintScoresDescribeTheWrittenPreset(t *testing.T) {
	cfg := constrainedRunConfig(t, 1.0, true, false)
	res := runConstrained(t, cfg)

	if res.outputGainRatio == 0 {
		t.Fatal("the gain match did not run, so this test proves nothing")
	}
	got := res.bestConstraintScores[analysis.ProfileLegacyV1]
	if want := remeasureConstraint(t, cfg, res); got != want {
		t.Fatalf("reported legacy-v1 %v, but the written preset measures %v", got, want)
	}
}

// TestPostGainMatchRecheckDecidesFeasibility pins that the feasibility verdict
// follows the POST-match measurement. Under --decay-relative=false a louder
// render crosses the fixed stop threshold later and is scored over a longer
// window, so a ceiling between the two measurements decides the run either way.
func TestPostGainMatchRecheckDecidesFeasibility(t *testing.T) {
	preMatch := runConstrained(t, constrainedRunConfig(t, 1.0, false, false)).
		bestConstraintScores[analysis.ProfileLegacyV1]
	postMatch := runConstrained(t, constrainedRunConfig(t, 1.0, true, false)).
		bestConstraintScores[analysis.ProfileLegacyV1]

	if preMatch == postMatch {
		t.Skip("the gain match does not move the constrained score at these test settings")
	}
	ceiling := (preMatch + postMatch) / 2
	// postMatch > preMatch: passes before the match, breaches after it, and the
	// run must come back infeasible. The mirror case must come back feasible.
	wantInfeasible := postMatch > preMatch

	res := runConstrained(t, constrainedRunConfig(t, ceiling, true, false))
	if res.constraintInfeasible != wantInfeasible {
		t.Fatalf("constraintInfeasible = %v, want %v (pre-match %v, post-match %v, ceiling %v)",
			res.constraintInfeasible, wantInfeasible, preMatch, postMatch, ceiling)
	}
	if got := res.bestConstraintScores[analysis.ProfileLegacyV1]; got != postMatch {
		t.Fatalf("reported legacy-v1 %v, want the post-match %v", got, postMatch)
	}
	if res.bestScore >= constraintPenaltyBase {
		t.Fatalf("best_score = %v: the report must state a readable primary score", res.bestScore)
	}
}

// TestGainMatchIsScoreInvariantUnderRelativeDecay is the other half: with the
// default relative auto-stop the gain match cannot move the constrained score,
// so the post-match measurement simply confirms the pre-match one.
func TestGainMatchIsScoreInvariantUnderRelativeDecay(t *testing.T) {
	preMatch := runConstrained(t, constrainedRunConfig(t, 1.0, false, true)).
		bestConstraintScores[analysis.ProfileLegacyV1]
	res := runConstrained(t, constrainedRunConfig(t, 1.0, true, true))
	if res.outputGainRatio == 0 {
		t.Fatal("the gain match did not run, so this test proves nothing")
	}
	// Equal up to the float32 rounding of the folded gain: the render length is
	// identical, only the samples are scaled.
	if got := res.bestConstraintScores[analysis.ProfileLegacyV1]; math.Abs(got-preMatch) > 1e-6 {
		t.Fatalf("relative decay: post-match legacy-v1 %v moved from the pre-match %v", got, preMatch)
	}
}

// TestNoFeasibleCandidateIsInfeasible covers the --max-evals 1 / unattainable
// ceiling case: the seed breaches, nothing feasible is found, and the result
// must be flagged so main can exit non-zero instead of publishing it.
func TestNoFeasibleCandidateIsInfeasible(t *testing.T) {
	res := runConstrained(t, constrainedRunConfig(t, 0.0, false, true))
	if !res.constraintInfeasible {
		t.Fatal("a run that never found a feasible candidate must be flagged infeasible")
	}
	if len(res.bestConstraintScores) == 0 {
		t.Fatal("the failed run must still report what it measured, or it cannot be diagnosed")
	}

	t.Run("feasible run is not flagged", func(t *testing.T) {
		ok := runConstrained(t, constrainedRunConfig(t, 1.0, false, true))
		if ok.constraintInfeasible {
			t.Fatal("a feasible run must not be flagged")
		}
	})

	t.Run("unconstrained run is never flagged", func(t *testing.T) {
		cfg := constrainedRunConfig(t, 0.0, false, true)
		cfg.scoreConstraints = nil
		plain := runConstrained(t, cfg)
		if plain.constraintInfeasible {
			t.Fatal("an unconstrained run must never be flagged infeasible")
		}
		if plain.bestConstraintScores != nil {
			t.Fatalf("unconstrained run reported constraint scores: %v", plain.bestConstraintScores)
		}
	})
}

func TestCloneCandidateCopiesSlice(t *testing.T) {
	orig := candidate{Vals: []float64{1.0, 2.0, 3.0}}
	cloned := cloneCandidate(orig)
	cloned.Vals[0] = 99.0

	if orig.Vals[0] != 1.0 {
		t.Fatalf("clone mutated original: got %.1f want 1.0", orig.Vals[0])
	}
}
