package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/qmc"
)

// sweepTestDefs mirrors the five knobs `--pass sustain` leaves active at note
// 60, with the same bounds knobs.go declares.
func sweepTestDefs() []knobDef {
	return []knobDef{
		{Name: "high_freq_damping", Min: 0.0, Max: 0.6},
		{Name: "unison_detune_scale", Min: 0.0, Max: 2.0},
		{Name: "unison_crossfeed", Min: 0.0, Max: 0.005},
		{Name: "per_note.60.loss", Min: 0.985, Max: 0.99995, Note: 60, NoteField: noteFieldLoss},
		{Name: "render.release_after", Min: 0.2, Max: 3.5},
	}
}

func sweepTestBaseline(defs []knobDef) candidate {
	pos := make([]float64, len(defs))
	for i := range pos {
		pos[i] = 0.5
	}
	return fromNormalized(pos, defs)
}

// syntheticEvaluator scores a candidate from a caller-supplied function of its
// normalised position, so every optimum, span and Pareto front in these tests
// is known analytically and no audio is ever rendered. It mirrors
// quadraticEvaluator in polish_test.go, extended to the multi-profile shape the
// sweep needs.
func syntheticEvaluator(
	defs []knobDef,
	profiles []string,
	score func(profile string, pos []float64) float64,
	counter *int64,
	fail func(pos []float64) error,
) sweepEvaluator {
	return func(c candidate) (map[string]analysis.Metrics, error) {
		if counter != nil {
			atomic.AddInt64(counter, 1)
		}
		pos := toNormalized(c, defs)
		if fail != nil {
			if err := fail(pos); err != nil {
				return nil, err
			}
		}
		out := make(map[string]analysis.Metrics, len(profiles))
		for _, p := range profiles {
			s := score(p, pos)
			out[p] = analysis.Metrics{
				SampleRate:   48000,
				Score:        s,
				Similarity:   1 - s,
				ScoreProfile: p,
				TimeRMSE:     s / 10,
			}
		}
		return out, nil
	}
}

func TestGenerateOATSamples(t *testing.T) {
	defs := sweepTestDefs()
	baseline := sweepTestBaseline(defs)
	basePos := toNormalized(baseline, defs)
	const samples = 9

	got, err := generateOATSamples(defs, baseline, samples)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := len(defs) * samples; len(got) != want {
		t.Fatalf("got %d samples before dedupe, want %d", len(got), want)
	}

	for k, d := range defs {
		var sawZero, sawOne bool
		for _, s := range got {
			if s.Knob != d.Name {
				continue
			}
			if s.Stage != sweepStageOAT {
				t.Fatalf("knob %s: stage = %q, want %q", d.Name, s.Stage, sweepStageOAT)
			}
			switch s.Pos[k] {
			case 0:
				sawZero = true
			case 1:
				sawOne = true
			}
			for j := range defs {
				if j == k {
					continue
				}
				if s.Pos[j] != basePos[j] {
					t.Fatalf("knob %s: coordinate %s = %v, want baseline %v", d.Name, defs[j].Name, s.Pos[j], basePos[j])
				}
			}
		}
		if !sawZero || !sawOne {
			t.Fatalf("knob %s: endpoints missing (0=%v, 1=%v)", d.Name, sawZero, sawOne)
		}
	}
}

func TestGenerateOATSamplesRejectsTooFewSamples(t *testing.T) {
	defs := sweepTestDefs()
	for _, n := range []int{-1, 0, 1} {
		if _, err := generateOATSamples(defs, sweepTestBaseline(defs), n); err == nil {
			t.Fatalf("samples=%d: expected an error", n)
		}
	}
}

func TestDedupeSamplesDropsNoOpIntegerSteps(t *testing.T) {
	// A three-value integer knob sampled at nine positions produces repeats
	// after rounding; the ones that land back on the baseline must not cost an
	// evaluation.
	defs := []knobDef{{Name: "render.velocity", Min: 1, Max: 3, IsInt: true}}
	baseline := candidate{Vals: []float64{2}}

	samples, err := generateOATSamples(defs, baseline, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 9 {
		t.Fatalf("got %d planned samples, want 9", len(samples))
	}

	kept, dropped := dedupeSamples(samples, baseline)
	if dropped == 0 {
		t.Fatal("expected the baseline-valued samples to be deduped")
	}
	if len(kept)+dropped != len(samples) {
		t.Fatalf("kept %d + dropped %d != %d planned", len(kept), dropped, len(samples))
	}
	baseKey := candidateKey(baseline)
	for _, s := range kept {
		if candidateKey(s.Cand) == baseKey {
			t.Fatalf("kept a sample identical to the baseline: %v", s.Cand.Vals)
		}
	}
}

// TestJointStageReachesEveryKnobSelection pins the dimensionality the joint
// stage has to cover, which is the widest selection the tool offers: the
// combined piano,body-ir,room-ir,mix set at 39 knobs.
//
// The sequence itself is tested in github.com/cwbudde/qmc. What is worth
// pinning here is that nothing between the flag and the generator caps the
// dimensionality by accident. The hand-rolled generator this replaced carried
// a fixed prime table that had to be grown by hand twice, and both times the
// failure was silent until a run hit it; qmc sieves its bases on demand, so
// the wall is gone rather than merely moved.
func TestJointStageReachesEveryKnobSelection(t *testing.T) {
	for _, dims := range []int{5, 9, 20, 39, 80} {
		wide := make([]knobDef, dims)
		for i := range wide {
			wide[i] = knobDef{Name: fmt.Sprintf("k%02d", i), Min: 0, Max: 1}
		}
		got, err := generateJointSamples(wide, 8, 64, dims, false, 0)
		if err != nil {
			t.Fatalf("%d dimensions: %v", dims, err)
		}
		if len(got) != 8 {
			t.Fatalf("%d dimensions: got %d samples, want 8", dims, len(got))
		}
		for i, sample := range got {
			if len(sample.Pos) != dims {
				t.Fatalf("%d dimensions: sample %d has %d coordinates", dims, i, len(sample.Pos))
			}
			for d, v := range sample.Pos {
				if v < 0 || v >= 1 {
					t.Fatalf("%d dimensions: sample %d coord %d = %v, want [0,1)", dims, i, d, v)
				}
			}
		}
	}
}

func TestGenerateJointSamples(t *testing.T) {
	defs := sweepTestDefs()

	t.Run("zero evals yields no samples", func(t *testing.T) {
		got, err := generateJointSamples(defs, 0, 64, 8, false, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d samples, want 0", len(got))
		}
	})

	t.Run("respects the dimensionality cap", func(t *testing.T) {
		_, err := generateJointSamples(defs, 16, 64, 4, false, 0)
		if err == nil {
			t.Fatal("expected an error above the cap")
		}
		if !strings.Contains(err.Error(), "--sweep-joint-max-dims") {
			t.Fatalf("error must name the flag that raises the cap, got %q", err)
		}
	})

	t.Run("nine knobs fill the box without duplicates", func(t *testing.T) {
		// Mirrors the width of the `attack` pass allowlist, which the 8-prime
		// table used to refuse outright.
		wide := make([]knobDef, 0, 9)
		for i := 0; i < 9; i++ {
			wide = append(wide, knobDef{Name: fmt.Sprintf("k%d", i), Min: 0, Max: 1})
		}
		const evals = 128
		got, err := generateJointSamples(wide, evals, 64, 16, false, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != evals {
			t.Fatalf("got %d samples, want %d", len(got), evals)
		}
		seen := make(map[string]int, evals)
		for i, s := range got {
			if len(s.Pos) != 9 {
				t.Fatalf("sample %d has %d coordinates, want 9", i, len(s.Pos))
			}
			key := candidateKey(s.Cand)
			if prev, dup := seen[key]; dup {
				t.Fatalf("sample %d duplicates sample %d (key %q)", i, prev, key)
			}
			seen[key] = i
		}
	})

	t.Run("skip offsets the sequence", func(t *testing.T) {
		got, err := generateJointSamples(defs, 3, 64, 8, false, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d samples, want 3", len(got))
		}
		seq, err := qmc.NewHalton(len(defs), qmc.WithSkip(64))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := seq.Next()
		if !reflect.DeepEqual(got[0].Pos, want) {
			t.Fatalf("first joint point = %v, want the point after a 64-point burn-in = %v", got[0].Pos, want)
		}
		if got[0].Stage != sweepStageJoint {
			t.Fatalf("stage = %q, want %q", got[0].Stage, sweepStageJoint)
		}
	})

	t.Run("scrambling is off by default and seeded when on", func(t *testing.T) {
		plain, err := generateJointSamples(defs, 16, 64, 8, false, 7)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The seed argument must be inert while scrambling is off: every
		// recorded sweep report was produced without it, and they have to
		// keep reproducing.
		ignoresSeed, err := generateJointSamples(defs, 16, 64, 8, false, 99)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(plain, ignoresSeed) {
			t.Fatal("the seed changed the unscrambled sequence; recorded sweep reports would no longer reproduce")
		}

		scrambled, err := generateJointSamples(defs, 16, 64, 8, true, 7)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reflect.DeepEqual(plain, scrambled) {
			t.Fatal("scrambling did not change the sequence")
		}
		again, err := generateJointSamples(defs, 16, 64, 8, true, 7)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(scrambled, again) {
			t.Fatal("the same seed produced different points; a scrambled sweep would not be reproducible")
		}
	})
}

// paretoTestPoint builds a scored point without going through an evaluator.
func paretoTestPoint(index int, a, b float64) sweepPoint {
	return sweepPoint{
		Index:  index,
		Stage:  sweepStageJoint,
		Scores: map[string]float64{"decay-v1": a, "legacy-v1": b},
		Knobs:  map[string]float64{"k": float64(index)},
	}
}

func TestParetoFront(t *testing.T) {
	const primary, secondary = "decay-v1", "legacy-v1"

	t.Run("empty input", func(t *testing.T) {
		if got := paretoFront(nil, primary, secondary); len(got) != 0 {
			t.Fatalf("got %d entries, want 0", len(got))
		}
	})

	t.Run("single point", func(t *testing.T) {
		got := paretoFront([]sweepPoint{paretoTestPoint(1, 0.4, 0.5)}, primary, secondary)
		if len(got) != 1 || got[0].Index != 1 {
			t.Fatalf("got %+v, want the single point", got)
		}
	})

	t.Run("strict dominance removes, weak keeps", func(t *testing.T) {
		// 2 is strictly dominated by 1; 3 ties 1 on primary but is worse on
		// secondary, so it is dominated too (weakly better in one, worse in
		// the other is still domination when the tie is not strict).
		pts := []sweepPoint{
			paretoTestPoint(1, 0.40, 0.50),
			paretoTestPoint(2, 0.45, 0.55),
			paretoTestPoint(3, 0.40, 0.60),
			paretoTestPoint(4, 0.30, 0.70),
		}
		got := paretoFront(pts, primary, secondary)
		gotIdx := make([]int, 0, len(got))
		for _, e := range got {
			gotIdx = append(gotIdx, e.Index)
		}
		if !reflect.DeepEqual(gotIdx, []int{4, 1}) {
			t.Fatalf("front = %v, want [4 1]", gotIdx)
		}
	})

	t.Run("exact duplicates collapse", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(7, 0.40, 0.50),
			paretoTestPoint(9, 0.40, 0.50),
		}
		got := paretoFront(pts, primary, secondary)
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1", len(got))
		}
		if got[0].Index != 7 {
			t.Fatalf("kept index %d, want the lowest-index representative 7", got[0].Index)
		}
	})

	// The lowest-index guarantee must not depend on the caller handing the
	// points over in index order.
	t.Run("exact duplicates collapse regardless of input order", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(9, 0.40, 0.50),
			paretoTestPoint(2, 0.40, 0.50),
			paretoTestPoint(7, 0.40, 0.50),
		}
		got := paretoFront(pts, primary, secondary)
		if len(got) != 1 {
			t.Fatalf("got %d entries, want 1", len(got))
		}
		if got[0].Index != 2 {
			t.Fatalf("kept index %d, want the lowest-index representative 2", got[0].Index)
		}
	})

	t.Run("hand-computed six-point set", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(1, 0.50, 0.50), // dominated by 4
			paretoTestPoint(2, 0.20, 0.90), // front
			paretoTestPoint(3, 0.60, 0.30), // front
			paretoTestPoint(4, 0.40, 0.45), // front
			paretoTestPoint(5, 0.70, 0.80), // dominated by 4
			paretoTestPoint(6, 0.45, 0.95), // dominated by 4
		}
		got := paretoFront(pts, primary, secondary)
		gotIdx := make([]int, 0, len(got))
		for _, e := range got {
			gotIdx = append(gotIdx, e.Index)
		}
		// Sorted by primary ascending: 0.20, 0.40, 0.60.
		if !reflect.DeepEqual(gotIdx, []int{2, 4, 3}) {
			t.Fatalf("front = %v, want [2 4 3]", gotIdx)
		}
	})
}

func TestConstrainedBest(t *testing.T) {
	const primary, secondary = "decay-v1", "legacy-v1"
	// A generous primary cap so the pre-existing cases keep exercising only
	// the secondary constraint; the primary requirement gets its own cases.
	const anyPrimary = 1.0

	t.Run("nil when nothing qualifies", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(1, 0.20, 0.60),
			paretoTestPoint(2, 0.10, 0.70),
		}
		best, count := constrainedBest(pts, primary, secondary, anyPrimary, 0.50)
		if best != nil {
			t.Fatalf("got %+v, want nil", best)
		}
		if count != 0 {
			t.Fatalf("count = %d, want 0", count)
		}
	})

	t.Run("lowest primary among qualifiers", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(1, 0.40, 0.49),
			paretoTestPoint(2, 0.30, 0.50),
			paretoTestPoint(3, 0.10, 0.51),
		}
		best, count := constrainedBest(pts, primary, secondary, anyPrimary, 0.50)
		if count != 2 {
			t.Fatalf("count = %d, want 2", count)
		}
		if best == nil || best.Index != 2 {
			t.Fatalf("got %+v, want index 2", best)
		}
	})

	t.Run("ties break by index", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(9, 0.30, 0.40),
			paretoTestPoint(4, 0.30, 0.45),
		}
		best, count := constrainedBest(pts, primary, secondary, anyPrimary, 0.50)
		if count != 2 {
			t.Fatalf("count = %d, want 2", count)
		}
		if best == nil || best.Index != 4 {
			t.Fatalf("got %+v, want the lowest index 4", best)
		}
	})

	// The regression this guards: with only the secondary cap, a run where
	// every non-regressing sample is WORSE on the primary objective still
	// returned the least-bad one, and both the report and the console read a
	// non-nil constrained_best as "a non-regressing improvement region
	// exists".
	t.Run("nil when the secondary cap holds but the primary never improves", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(1, 0.45, 0.48),
			paretoTestPoint(2, 0.60, 0.40),
		}
		best, count := constrainedBest(pts, primary, secondary, 0.40, 0.50)
		if best != nil {
			t.Fatalf("got %+v, want nil: no sample improves the primary", best)
		}
		// The count keeps its secondary-only meaning.
		if count != 2 {
			t.Fatalf("count = %d, want 2", count)
		}
	})

	t.Run("primary improvement must be strict", func(t *testing.T) {
		pts := []sweepPoint{paretoTestPoint(1, 0.40, 0.45)}
		best, count := constrainedBest(pts, primary, secondary, 0.40, 0.50)
		if best != nil {
			t.Fatalf("got %+v, want nil: equalling the baseline is not an improvement", best)
		}
		if count != 1 {
			t.Fatalf("count = %d, want 1", count)
		}
	})

	t.Run("skips a better-primary point that breaches the secondary cap", func(t *testing.T) {
		pts := []sweepPoint{
			paretoTestPoint(1, 0.10, 0.90), // best primary, but regresses secondary
			paretoTestPoint(2, 0.35, 0.45), // the honest answer
		}
		best, count := constrainedBest(pts, primary, secondary, 0.40, 0.50)
		if count != 1 {
			t.Fatalf("count = %d, want 1", count)
		}
		if best == nil || best.Index != 2 {
			t.Fatalf("got %+v, want index 2", best)
		}
	})
}

// sensitivitySlice builds an ordered OAT line for one knob.
func sensitivitySlice(knob string, values []float64) []sweepPoint {
	pts := make([]sweepPoint, 0, len(values))
	for i, v := range values {
		pts = append(pts, sweepPoint{
			Index:  i,
			Stage:  sweepStageOAT,
			Knob:   knob,
			Scores: map[string]float64{"decay-v1": v},
			Knobs:  map[string]float64{knob: float64(i)},
		})
	}
	return pts
}

func TestComputeSensitivity(t *testing.T) {
	const primary = "decay-v1"

	t.Run("monotone slice", func(t *testing.T) {
		got := computeSensitivity("k", sensitivitySlice("k", []float64{0.5, 0.4, 0.3, 0.2, 0.1}), []string{primary}, primary)
		span := got.Profiles[primary]
		if math.Abs(span.Min-0.1) > 1e-12 || math.Abs(span.Max-0.5) > 1e-12 {
			t.Fatalf("span = %+v, want min 0.1 max 0.5", span)
		}
		if math.Abs(span.Span-0.4) > 1e-12 {
			t.Fatalf("span.Span = %v, want 0.4", span.Span)
		}
		if span.ArgminValue != 4 {
			t.Fatalf("argmin = %v, want the last knob value 4", span.ArgminValue)
		}
		if !got.Monotonic {
			t.Fatal("a strictly decreasing slice must be flagged monotonic")
		}
	})

	t.Run("V-shaped slice", func(t *testing.T) {
		got := computeSensitivity("k", sensitivitySlice("k", []float64{0.5, 0.3, 0.1, 0.3, 0.5}), []string{primary}, primary)
		span := got.Profiles[primary]
		if span.ArgminValue != 2 {
			t.Fatalf("argmin = %v, want the interior minimum at 2", span.ArgminValue)
		}
		if math.Abs(span.Span-0.4) > 1e-12 {
			t.Fatalf("span = %v, want 0.4", span.Span)
		}
		if got.Monotonic {
			t.Fatal("a V-shaped slice must not be flagged monotonic")
		}
	})
}

func TestParseSweepProfiles(t *testing.T) {
	t.Run("empty falls back to pass profile then legacy", func(t *testing.T) {
		got, err := parseSweepProfiles("", analysis.ProfileDecayV1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{analysis.ProfileDecayV1, analysis.ProfileLegacyV1}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("empty with a legacy pass collapses to one profile", func(t *testing.T) {
		got, err := parseSweepProfiles("", analysis.ProfileLegacyV1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, []string{analysis.ProfileLegacyV1}) {
			t.Fatalf("got %v, want one legacy-v1 entry", got)
		}
	})

	t.Run("explicit list preserved and deduped", func(t *testing.T) {
		got, err := parseSweepProfiles("legacy-v1, decay-v1 ,legacy-v1", analysis.ProfileDecayV1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{analysis.ProfileLegacyV1, analysis.ProfileDecayV1}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("unknown profile names the offender", func(t *testing.T) {
		_, err := parseSweepProfiles("legacy-v1,bogus-v9", analysis.ProfileDecayV1)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "bogus-v9") {
			t.Fatalf("error must name the offender, got %q", err)
		}
	})
}

// sweepRunFixture builds a runnable sweep over the sustain knobs with a
// synthetic objective: a linear trade-off so decay-v1 and legacy-v1 pull in
// opposite directions along the first coordinate.
func sweepRunFixture(t *testing.T, workers int, counter *int64, fail func(pos []float64) error) sweepRunConfig {
	t.Helper()
	defs := sweepTestDefs()
	profiles := []string{analysis.ProfileDecayV1, analysis.ProfileLegacyV1}
	score := func(profile string, pos []float64) float64 {
		sum := 0.0
		for _, v := range pos {
			sum += v
		}
		mean := sum / float64(len(pos))
		if profile == analysis.ProfileLegacyV1 {
			return 1 - mean
		}
		return mean
	}
	return sweepRunConfig{
		defs:          defs,
		baseline:      sweepTestBaseline(defs),
		eval:          syntheticEvaluator(defs, profiles, score, counter, fail),
		profiles:      profiles,
		primary:       analysis.ProfileDecayV1,
		samples:       5,
		jointEvals:    32,
		jointSkip:     64,
		jointMaxDims:  8,
		workers:       workers,
		referencePath: "reference/c4.wav",
		presetPath:    "out/passes/attack.json",
		note:          60,
		velocity:      118,
		releaseAfter:  3.5,
		sampleRate:    48000,
		pass:          passSustain,
	}
}

func TestRunSweepIsWorkerCountIndependent(t *testing.T) {
	var oneCount, manyCount int64

	one, err := runSweep(sweepRunFixture(t, 1, &oneCount, nil))
	if err != nil {
		t.Fatalf("workers=1: %v", err)
	}
	many, err := runSweep(sweepRunFixture(t, 8, &manyCount, nil))
	if err != nil {
		t.Fatalf("workers=8: %v", err)
	}

	// Wall-clock is the one field that legitimately differs.
	one.ElapsedSeconds = 0
	many.ElapsedSeconds = 0

	a, err := json.Marshal(one)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := json.Marshal(many)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("sweep report depends on --workers; it must not")
	}

	wantEvals := 1 + len(one.OAT) + len(one.Joint)
	if one.Evals != wantEvals {
		t.Fatalf("evals = %d, want %d", one.Evals, wantEvals)
	}
	if oneCount != int64(wantEvals) || manyCount != int64(wantEvals) {
		t.Fatalf("evaluator calls = %d/%d, want %d", oneCount, manyCount, wantEvals)
	}
	if one.Errors != 0 {
		t.Fatalf("errors = %d, want 0", one.Errors)
	}
}

func TestRunSweepRecordsEvaluatorErrors(t *testing.T) {
	var failed int64
	fail := func(pos []float64) error {
		// Fail exactly one interior point, deterministically.
		if math.Abs(pos[0]-0.25) < 1e-9 && math.Abs(pos[1]-0.5) < 1e-9 {
			if atomic.AddInt64(&failed, 1) == 1 {
				return errors.New("synthetic render failure")
			}
		}
		return nil
	}
	report, err := runSweep(sweepRunFixture(t, 4, nil, fail))
	if err != nil {
		t.Fatalf("the run must not abort on a single evaluator error: %v", err)
	}
	if report.Errors != 1 {
		t.Fatalf("errors = %d, want 1", report.Errors)
	}
	found := false
	for _, p := range report.OAT {
		if p.Err != "" {
			found = true
			if p.Knobs == nil || len(p.Pos) == 0 {
				t.Fatal("a failed point must still carry its position and knobs")
			}
		}
	}
	if !found {
		t.Fatal("the failed point is missing from the report")
	}
}

func TestSweepReportShape(t *testing.T) {
	report, err := runSweep(sweepRunFixture(t, 2, nil, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	documented := []string{
		"schema", "reference_path", "preset_path", "note", "velocity",
		"release_after_seconds", "sample_rate", "pass", "profiles",
		"primary_profile", "score_norms", "knobs", "samples", "joint_evals",
		"joint_sequence", "joint_skip", "deduped", "baseline", "oat", "joint",
		"sensitivity", "pareto", "constrained_count", "evals", "errors",
		"elapsed_seconds",
	}
	for _, key := range documented {
		if _, ok := got[key]; !ok {
			t.Fatalf("report is missing the documented key %q", key)
		}
	}
	if got["schema"] != sweepSchema {
		t.Fatalf("schema = %v, want %q", got["schema"], sweepSchema)
	}
	if got["joint_sequence"] != sweepJointSequence {
		t.Fatalf("joint_sequence = %v, want %q", got["joint_sequence"], sweepJointSequence)
	}

	// pass_window is omitempty: unset here, present once set.
	if _, ok := got["pass_window"]; ok {
		t.Fatal("pass_window must be omitted when no window is configured")
	}
	windowed := sweepRunFixture(t, 1, nil, nil)
	windowed.passWindow = &windowSpec{StartSec: 0.2, EndSec: 2.0}
	wrep, err := runSweep(windowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wb, err := json.Marshal(wrep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wgot map[string]any
	if err := json.Unmarshal(wb, &wgot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := wgot["pass_window"]; !ok {
		t.Fatal("pass_window must be present once a window is configured")
	}

	// constrained_best is omitempty too.
	report.ConstrainedBest = nil
	nb, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var ngot map[string]any
	if err := json.Unmarshal(nb, &ngot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := ngot["constrained_best"]; ok {
		t.Fatal("constrained_best must be omitted when nothing qualifies")
	}
}

func TestSweepPointMarshalsNonFiniteMetrics(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	m := analysis.Metrics{
		SampleRate:             48000,
		TimeRMSE:               nan,
		EnvelopeRMSEDB:         inf,
		SpectralRMSEDB:         nan,
		DecayDiffDBPerS:        nan,
		PartialLevelRMSEDB:     nan,
		PartialFreqRMSECents:   nan,
		TristimulusDistance:    nan,
		DecaySegmentRMSEDBPerS: nan,
		Score:                  nan,
		Similarity:             nan,
		ScoreProfile:           analysis.ProfileDecayV1,
	}
	weights, err := analysis.WeightsForProfile(analysis.ProfileDecayV1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The raw Metrics is exactly what encoding/json refuses; the sweep path
	// must sanitize it and mirror the components into null-safe rows.
	if _, err := json.Marshal(m); err == nil {
		t.Fatal("expected raw NaN metrics to be unmarshalable; the regression this guards is gone")
	}

	point := sweepPoint{
		Index:      1,
		Stage:      sweepStageJoint,
		Pos:        []float64{0.5},
		Knobs:      map[string]float64{"k": 1},
		Scores:     map[string]float64{analysis.ProfileDecayV1: 1.0},
		Metrics:    m.Sanitized(),
		Components: map[string][]sweepComponent{analysis.ProfileDecayV1: toSweepComponents(analysis.Components(m, weights))},
	}
	if _, err := json.Marshal(point); err != nil {
		t.Fatalf("a sanitized sweep point must marshal: %v", err)
	}
}

func TestSweepSustainPassSelectsTheExpectedKnobs(t *testing.T) {
	base := piano.NewDefaultParams()
	groups, err := parseOptimizeGroups("piano,mix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defs, cand := initCandidate(base, 48000, []int{60}, 118, 3.5, groups, true)

	spec, err := parsePass(passSustain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, gotCand := filterKnobsForPass(defs, cand, spec)

	names := make([]string, 0, len(got))
	for _, d := range got {
		names = append(names, d.Name)
	}
	want := []string{
		"high_freq_damping",
		"unison_detune_scale",
		"unison_crossfeed",
		"per_note.60.loss",
		"render.release_after",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("--sweep --pass sustain knobs = %v, want %v", names, want)
	}
	if len(gotCand.Vals) != len(want) {
		t.Fatalf("candidate has %d values, want %d", len(gotCand.Vals), len(want))
	}
	if _, err := generateJointSamples(got, 8, 64, 8, false, 0); err != nil {
		t.Fatalf("the sustain knob set must fit the joint stage: %v", err)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stdout = saved
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	out := <-done
	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return out
}

// TestPrintSweepReportSingleProfile pins the one-profile rendering: Pareto
// extraction is skipped with a clear message rather than panicking, and the
// sensitivity table must not fall back to an empty secondary profile name —
// that printed a blank column header over a column of 0.0000 spans read out of
// a missing map entry.
func TestPrintSweepReportSingleProfile(t *testing.T) {
	cfg := sweepRunFixture(t, 1, nil, nil)
	profiles := []string{analysis.ProfileDecayV1}
	cfg.profiles = profiles
	cfg.primary = analysis.ProfileDecayV1
	cfg.eval = syntheticEvaluator(cfg.defs, profiles, func(profile string, pos []float64) float64 {
		sum := 0.0
		for _, v := range pos {
			sum += v
		}
		return sum / float64(len(pos))
	}, nil, nil)

	report, err := runSweep(cfg)
	if err != nil {
		t.Fatalf("a single-profile sweep must still run: %v", err)
	}
	if len(report.Pareto) != 0 {
		t.Fatalf("pareto = %d entries, want 0 with one profile", len(report.Pareto))
	}
	if report.ConstrainedBest != nil {
		t.Fatalf("constrained_best = %+v, want nil with one profile", report.ConstrainedBest)
	}

	out := captureStdout(t, func() { printSweepReport(report) })

	if !strings.Contains(out, "Pareto: skipped (needs two profiles") {
		t.Fatalf("the skipped-Pareto message is missing:\n%s", out)
	}

	header := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "knob") && strings.Contains(line, "monotonic") {
			header = line
			break
		}
	}
	if header == "" {
		t.Fatalf("sensitivity header not found:\n%s", out)
	}
	// With one profile both score columns name that profile; neither may be
	// blank.
	if strings.Count(header, analysis.ProfileDecayV1) != 2 {
		t.Fatalf("header must name %q in both score columns, got %q", analysis.ProfileDecayV1, header)
	}

	// The spans must be real numbers from the profile, not zeroes read out of
	// Profiles[""].
	sawNonZeroSpan := false
	for _, s := range report.Sensitivity {
		if s.Profiles[analysis.ProfileDecayV1].Span != 0 {
			sawNonZeroSpan = true
		}
		if _, ok := s.Profiles[""]; ok {
			t.Fatal("sensitivity must not carry an empty profile key")
		}
	}
	if !sawNonZeroSpan {
		t.Fatal("the fixture must move the primary score; the test proves nothing otherwise")
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") || !strings.Contains(line, "per_note.60.loss") {
			continue
		}
		if strings.Contains(line, "0.0000       0.0000") {
			t.Fatalf("both span columns printed as 0.0000, the blank-secondary bug is back: %q", line)
		}
	}
}
