package main

import (
	"fmt"
	"sort"
	"strings"
)

// defaultPolishKnobs is the default --polish-knobs list. It deliberately
// excludes output_gain: that knob is score-invariant (see matchOutputGain) and
// polishing it would waste the entire eval budget on a flat dimension.
//
// The invariance has two halves, and both are proved by tests rather than
// asserted here. analysis.Compare RMS-normalises both signals
// (TestOutputGainIsScoreInvariant), and the render auto-stop is taken relative
// to the render's own running peak so the render length does not depend on the
// absolute level either (TestOutputGainDoesNotMoveTheScoreThroughRenderLength
// in gain_invariance_test.go). The second half only became true on 2026-08-22;
// before that the auto-stop was an absolute -90 dBFS threshold and a louder
// render was scored over a longer window. --decay-relative=false brings that
// behaviour, and the level-dependence with it, back.
const defaultPolishKnobs = "render.velocity,render.release_after,hammer_initial_velocity_scale"

// polishConfig parameterises the deterministic coordinate-descent polish stage.
type polishConfig struct {
	knobIndices []int
	maxEvals    int
	rounds      int
	initialStep float64
	shrink      float64
	minStep     float64
	settings    evalSettings
	scratchPath string
	// onImprove is called after every accepted improvement so the caller can
	// checkpoint. It is called synchronously from polishCandidate.
	onImprove func(cand candidate, ev optimizationEval, evalNum int)
}

// polishSummary reports what the polish stage did.
type polishSummary struct {
	Knobs        []string `json:"knobs"`
	Evals        int      `json:"evals"`
	Improvements int      `json:"improvements"`
	ScoreBefore  float64  `json:"score_before"`
	ScoreAfter   float64  `json:"score_after"`
	FinalStep    float64  `json:"final_step"`
	Converged    bool     `json:"converged"`
}

// parsePolishKnobs resolves a comma-separated knob-name list into indices into
// defs. The result is deduplicated and sorted into defs order, which is the
// order the coordinate sweep visits.
func parsePolishKnobs(raw string, defs []knobDef) ([]int, error) {
	index := make(map[string]int, len(defs))
	for i, d := range defs {
		index[d.Name] = i
	}

	seen := make(map[int]bool)
	indices := make([]int, 0, len(defs))
	var unknown []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		i, ok := index[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if seen[i] {
			continue
		}
		seen[i] = true
		indices = append(indices, i)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown polish knob(s) %s (not in the active --optimize groups)", strings.Join(unknown, ", "))
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("no polish knobs selected")
	}
	sort.Ints(indices)
	return indices, nil
}

// intersectKnobNames drops names that are not present in defs.
//
// It exists so the DEFAULT --polish-knobs list degrades gracefully: a pass or
// an --optimize selection without the piano group filters some of those knobs
// out of defs, and a default that the user never typed should not turn into a
// hard error. An explicitly supplied list still goes to parsePolishKnobs
// untouched, so typos there are still reported.
func intersectKnobNames(raw string, defs []knobDef) string {
	present := make(map[string]bool, len(defs))
	for _, d := range defs {
		present[d.Name] = true
	}
	kept := make([]string, 0, len(defs))
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" && present[name] {
			kept = append(kept, name)
		}
	}
	return strings.Join(kept, ",")
}

// polishCandidate runs a deterministic coordinate descent over the selected
// knobs, entirely in normalised [0,1] space so bounds, LogScale and IsInt
// handling all come from the existing toNormalized/fromNormalized pair.
//
// Each round sweeps the selected knobs in defs order, trying +step then -step
// and moving on to the next knob at the first STRICTLY better score. A sweep
// that improves nothing shrinks the step by pcfg.shrink. The descent stops when
// the step falls below pcfg.minStep, the round budget is exhausted, or the hard
// eval budget is hit.
//
// Because only strictly better candidates are accepted, the stage cannot
// regress: the returned score is always <= startEval.aggregate. Probes that
// candidateKey shows to be no-ops after IsInt rounding are skipped without
// spending an evaluation — for render.velocity on [40,127] the smallest
// meaningful normalised step is 1/87 ~= 0.0115, so with a min step of 0.004 the
// late rounds would otherwise be pure waste.
func polishCandidate(
	defs []knobDef,
	eval candidateEvaluator,
	pcfg polishConfig,
	start candidate,
	startEval optimizationEval,
) (candidate, optimizationEval, polishSummary) {
	names := make([]string, 0, len(pcfg.knobIndices))
	for _, i := range pcfg.knobIndices {
		if i >= 0 && i < len(defs) {
			names = append(names, defs[i].Name)
		}
	}

	best := cloneCandidate(start)
	bestEval := cloneOptimizationEval(startEval)
	step := pcfg.initialStep
	summary := polishSummary{
		Knobs:       names,
		ScoreBefore: startEval.aggregate,
		ScoreAfter:  startEval.aggregate,
		FinalStep:   step,
	}

	if len(pcfg.knobIndices) == 0 || pcfg.maxEvals < 1 || pcfg.rounds < 1 || step <= 0 {
		return best, bestEval, summary
	}
	shrink := pcfg.shrink
	if shrink <= 0 || shrink >= 1 {
		shrink = 0.5
	}

	pos := toNormalized(best, defs)
	evals := 0

	for round := 0; round < pcfg.rounds && step >= pcfg.minStep && evals < pcfg.maxEvals; round++ {
		improvedRound := false
		bestKey := candidateKey(best)

		for _, ki := range pcfg.knobIndices {
			if evals >= pcfg.maxEvals {
				break
			}
			if ki < 0 || ki >= len(defs) {
				continue
			}
			for _, delta := range [2]float64{step, -step} {
				if evals >= pcfg.maxEvals {
					break
				}
				trial := append([]float64(nil), pos...)
				trial[ki] = clamp(pos[ki]+delta, 0, 1)
				cand := fromNormalized(trial, defs)
				key := candidateKey(cand)
				if key == bestKey {
					// No-op after clamping and IsInt rounding: do not burn an
					// evaluation on a candidate we have already scored.
					continue
				}

				evals++
				ev, err := eval(cand, pcfg.scratchPath, pcfg.settings)
				if err != nil {
					continue
				}
				if ev.aggregate >= bestEval.aggregate {
					continue
				}

				best = cloneCandidate(cand)
				bestEval = cloneOptimizationEval(ev)
				pos = toNormalized(best, defs)
				bestKey = candidateKey(best)
				improvedRound = true
				summary.Improvements++
				if pcfg.onImprove != nil {
					pcfg.onImprove(cloneCandidate(best), cloneOptimizationEval(bestEval), evals)
				}
				break
			}
		}

		if !improvedRound {
			step *= shrink
		}
	}

	summary.Evals = evals
	summary.ScoreAfter = bestEval.aggregate
	summary.FinalStep = step
	summary.Converged = step < pcfg.minStep
	return best, bestEval, summary
}
