package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// polishTestDefs mirrors the knobs the default --polish-knobs list names, plus
// a knob that is never selected, so the test can assert it is left alone.
func polishTestDefs() []knobDef {
	return []knobDef{
		{Name: "output_gain", Min: 0.01, Max: 5.0},
		{Name: "hammer_initial_velocity_scale", Min: 0.7, Max: 1.4},
		{Name: "render.velocity", Min: 40, Max: 127, IsInt: true},
		{Name: "render.release_after", Min: 0.2, Max: 3.5},
	}
}

// quadraticEvaluator scores a candidate by its normalised distance to a target
// position, so the optimum and the descent path are both known analytically.
func quadraticEvaluator(defs []knobDef, target []float64, counter *int) candidateEvaluator {
	return func(c candidate, _ string, _ evalSettings) (optimizationEval, error) {
		if counter != nil {
			*counter++
		}
		pos := toNormalized(c, defs)
		sum := 0.0
		for i := range pos {
			d := pos[i] - target[i]
			sum += d * d
		}
		score := sum / float64(len(pos))
		return optimizationEval{
			aggregate: score,
			notes:     []noteReport{{Note: 60, Score: score}},
		}, nil
	}
}

func TestParsePolishKnobs(t *testing.T) {
	defs := polishTestDefs()

	t.Run("defaults resolve in defs order", func(t *testing.T) {
		got, err := parsePolishKnobs(defaultPolishKnobs, defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// defaultPolishKnobs lists velocity, release_after, hammer... but the
		// result must come back sorted into defs order.
		want := []int{1, 2, 3}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("default excludes output_gain", func(t *testing.T) {
		if strings.Contains(defaultPolishKnobs, "output_gain") {
			t.Fatal("output_gain is score-invariant and must not be a default polish knob")
		}
	})

	t.Run("dedupes and trims", func(t *testing.T) {
		got, err := parsePolishKnobs(" render.velocity , render.velocity ,, output_gain ", defs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, []int{0, 2}) {
			t.Fatalf("got %v, want [0 2]", got)
		}
	})

	t.Run("unknown knob errors", func(t *testing.T) {
		_, err := parsePolishKnobs("render.velocity,body_modes", defs)
		if err == nil {
			t.Fatal("expected error for a knob outside the active groups")
		}
		if !strings.Contains(err.Error(), "body_modes") {
			t.Fatalf("error %q should name the offending knob", err)
		}
	})

	t.Run("empty errors", func(t *testing.T) {
		if _, err := parsePolishKnobs("  ,  ", defs); err == nil {
			t.Fatal("expected error for an empty selection")
		}
	})
}

func TestPolishCandidateCannotRegress(t *testing.T) {
	defs := polishTestDefs()
	// A target the polish stage can never reach, because the only selected
	// knob is index 1 and the target moves every dimension.
	target := []float64{0.0, 0.9, 0.9, 0.9}
	eval := quadraticEvaluator(defs, target, nil)

	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)
	startEval, err := eval(start, "", evalSettings{})
	if err != nil {
		t.Fatalf("start eval: %v", err)
	}

	_, gotEval, summary := polishCandidate(defs, eval, polishConfig{
		knobIndices: []int{1},
		maxEvals:    100,
		rounds:      6,
		initialStep: 0.08,
		shrink:      0.5,
		minStep:     0.004,
	}, start, startEval)

	if gotEval.aggregate > startEval.aggregate {
		t.Fatalf("polish regressed: %v -> %v", startEval.aggregate, gotEval.aggregate)
	}
	if summary.ScoreAfter > summary.ScoreBefore {
		t.Fatalf("summary regressed: %v -> %v", summary.ScoreBefore, summary.ScoreAfter)
	}
	if summary.Improvements == 0 {
		t.Fatal("expected the descent to find at least one improvement")
	}
}

func TestPolishCandidateDescendsTowardsOptimum(t *testing.T) {
	defs := polishTestDefs()
	target := []float64{0.5, 0.9, 0.5, 0.5}
	eval := quadraticEvaluator(defs, target, nil)

	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)
	startEval, _ := eval(start, "", evalSettings{})

	best, bestEval, summary := polishCandidate(defs, eval, polishConfig{
		knobIndices: []int{1},
		maxEvals:    200,
		rounds:      12,
		initialStep: 0.08,
		shrink:      0.5,
		minStep:     0.004,
	}, start, startEval)

	pos := toNormalized(best, defs)
	if math.Abs(pos[1]-0.9) > 0.01 {
		t.Fatalf("knob 1 settled at %v, want ~0.9", pos[1])
	}
	if bestEval.aggregate >= startEval.aggregate {
		t.Fatalf("no improvement: %v -> %v", startEval.aggregate, bestEval.aggregate)
	}
	if !summary.Converged {
		t.Fatalf("expected convergence, summary=%+v", summary)
	}
	if summary.FinalStep >= 0.004 {
		t.Fatalf("final step %v should have shrunk below minStep", summary.FinalStep)
	}
	// Untouched knobs must keep their starting values.
	if best.Vals[0] != start.Vals[0] || best.Vals[2] != start.Vals[2] || best.Vals[3] != start.Vals[3] {
		t.Fatalf("polish moved unselected knobs: %v vs %v", best.Vals, start.Vals)
	}
}

// TestPolishShrinksStepOnFailedSweep is the first of the three things this
// stage must do better than cmd/piano-modal-fit's refineLocally: that one
// never shrinks its step.
func TestPolishShrinksStepOnFailedSweep(t *testing.T) {
	defs := polishTestDefs()
	// The start is already the optimum, so every sweep fails.
	target := []float64{0.5, 0.5, 0.5, 0.5}
	eval := quadraticEvaluator(defs, target, nil)

	start := fromNormalized(target, defs)
	startEval, _ := eval(start, "", evalSettings{})

	_, _, summary := polishCandidate(defs, eval, polishConfig{
		knobIndices: []int{1},
		maxEvals:    1000,
		rounds:      20,
		initialStep: 0.08,
		shrink:      0.5,
		minStep:     0.004,
	}, start, startEval)

	if summary.Improvements != 0 {
		t.Fatalf("expected no improvement at the optimum, got %d", summary.Improvements)
	}
	// 0.08 * 0.5^5 = 0.0025 < 0.004, so it must stop after five shrinks.
	if summary.FinalStep >= 0.004 {
		t.Fatalf("step never shrank below minStep: %v", summary.FinalStep)
	}
	if !summary.Converged {
		t.Fatal("expected Converged=true after the step shrank below minStep")
	}
	if summary.Evals > 12 {
		t.Fatalf("failed sweeps cost %d evals; the shrink should have ended it quickly", summary.Evals)
	}
}

// TestPolishSkipsIntegerNoOpProbes is the second improvement: probes that
// candidateKey shows are no-ops after IsInt rounding must not spend an
// evaluation. For render.velocity on [40,127] the smallest meaningful
// normalised step is 1/87 ~= 0.0115, so a step of 0.004 rounds to the same
// velocity in both directions.
func TestPolishSkipsIntegerNoOpProbes(t *testing.T) {
	defs := polishTestDefs()
	target := []float64{0.5, 0.5, 1.0, 0.5}
	evals := 0
	eval := quadraticEvaluator(defs, target, &evals)

	// Start exactly on an integer velocity so +/-0.004 rounds back to it.
	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)
	startEval, _ := eval(start, "", evalSettings{})
	evals = 0

	_, _, summary := polishCandidate(defs, eval, polishConfig{
		knobIndices: []int{2}, // render.velocity only
		maxEvals:    100,
		rounds:      1,
		initialStep: 0.004,
		shrink:      0.5,
		minStep:     0.001,
	}, start, startEval)

	if summary.Evals != 0 || evals != 0 {
		t.Fatalf("no-op integer probes cost %d evals (evaluator calls %d), want 0", summary.Evals, evals)
	}
}

// TestPolishHonoursEvalBudget is the third improvement: a hard eval budget.
func TestPolishHonoursEvalBudget(t *testing.T) {
	defs := polishTestDefs()
	target := []float64{0.9, 0.9, 0.9, 0.9}
	evals := 0
	eval := quadraticEvaluator(defs, target, &evals)

	start := fromNormalized([]float64{0.1, 0.1, 0.1, 0.1}, defs)
	startEval, _ := eval(start, "", evalSettings{})
	evals = 0

	const budget = 5
	_, _, summary := polishCandidate(defs, eval, polishConfig{
		knobIndices: []int{0, 1, 2, 3},
		maxEvals:    budget,
		rounds:      50,
		initialStep: 0.02,
		shrink:      0.5,
		minStep:     0.0001,
	}, start, startEval)

	if summary.Evals > budget || evals > budget {
		t.Fatalf("budget %d exceeded: summary %d, evaluator calls %d", budget, summary.Evals, evals)
	}
}

func TestPolishOnImproveIsCalledPerImprovement(t *testing.T) {
	defs := polishTestDefs()
	target := []float64{0.5, 0.95, 0.5, 0.5}
	eval := quadraticEvaluator(defs, target, nil)

	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)
	startEval, _ := eval(start, "", evalSettings{})

	var seen []float64
	_, _, summary := polishCandidate(defs, eval, polishConfig{
		knobIndices: []int{1},
		maxEvals:    100,
		rounds:      10,
		initialStep: 0.08,
		shrink:      0.5,
		minStep:     0.004,
		onImprove: func(_ candidate, ev optimizationEval, _ int) {
			seen = append(seen, ev.aggregate)
		},
	}, start, startEval)

	if len(seen) != summary.Improvements {
		t.Fatalf("onImprove called %d times, summary reports %d improvements", len(seen), summary.Improvements)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] >= seen[i-1] {
			t.Fatalf("onImprove scores are not strictly decreasing: %v", seen)
		}
	}
}

func TestPolishNoKnobsIsANoOp(t *testing.T) {
	defs := polishTestDefs()
	evals := 0
	eval := quadraticEvaluator(defs, []float64{0.9, 0.9, 0.9, 0.9}, &evals)
	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)
	startEval, _ := eval(start, "", evalSettings{})
	evals = 0

	best, bestEval, summary := polishCandidate(defs, eval, polishConfig{
		maxEvals: 100, rounds: 5, initialStep: 0.08, shrink: 0.5, minStep: 0.004,
	}, start, startEval)

	if evals != 0 || summary.Evals != 0 {
		t.Fatalf("expected no evaluations, got %d/%d", evals, summary.Evals)
	}
	if !reflect.DeepEqual(best.Vals, start.Vals) || bestEval.aggregate != startEval.aggregate {
		t.Fatal("empty knob selection must leave the candidate untouched")
	}
}

func TestIntersectKnobNames(t *testing.T) {
	defs := polishTestDefs()
	if got := intersectKnobNames(defaultPolishKnobs, defs); got != defaultPolishKnobs {
		t.Fatalf("all knobs present: got %q", got)
	}

	// A sustain pass keeps render.release_after but drops the other two.
	sustainDefs := []knobDef{{Name: "high_freq_damping"}, {Name: "render.release_after"}}
	if got := intersectKnobNames(defaultPolishKnobs, sustainDefs); got != "render.release_after" {
		t.Fatalf("got %q, want \"render.release_after\"", got)
	}

	if got := intersectKnobNames(defaultPolishKnobs, []knobDef{{Name: "body_modes"}}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
