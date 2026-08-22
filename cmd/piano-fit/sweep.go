package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbudde/algo-piano/analysis"
)

// sweepSchema versions the report artifact. Bump it when a documented key
// changes meaning; adding a key is backwards compatible.
const sweepSchema = "algo-piano.sweep/v1"

// sweepStageOAT and sweepStageJoint tag which stage produced a sample.
const (
	sweepStageBaseline = "baseline"
	sweepStageOAT      = "oat"
	sweepStageJoint    = "joint"
)

// sweepJointSequence names the low-discrepancy sequence used by the joint
// stage. It is recorded in the report so a reader never has to guess how the
// points were placed.
const sweepJointSequence = "halton"

// haltonPrimes are the bases of the Halton sequence, one per dimension. The
// table length is the hard dimensionality ceiling of the joint stage: high
// Halton bases correlate badly in low sample counts, so there is no point
// extending it far.
var haltonPrimes = []int{2, 3, 5, 7, 11, 13, 17, 19}

// sweepEvaluator renders one candidate and scores it under every requested
// profile. It is a distinct type from candidateEvaluator on purpose: the sweep
// needs one render scored under several profiles, while candidateEvaluator
// bakes a single scorer into the render. It exists in this shape so the whole
// sweep can be unit-tested against a synthetic objective without rendering
// audio (see sweep_test.go).
type sweepEvaluator func(cand candidate) (map[string]analysis.Metrics, error)

// sweepKnob describes one swept dimension in the report.
type sweepKnob struct {
	Name          string  `json:"name"`
	Min           float64 `json:"min"`
	Max           float64 `json:"max"`
	LogScale      bool    `json:"log_scale"`
	IsInt         bool    `json:"is_int"`
	BaselineValue float64 `json:"baseline_value"`
}

// sweepComponent mirrors analysis.Component with JSON tags and null-safe
// floats.
//
// It is a local mirror rather than a change to analysis.Component because
// analysis/ is deliberately left untouched by this tool, and because a
// component's Raw and Norm are legitimately NaN when the component could not
// be measured — encoding/json refuses NaN, so those fields become null.
type sweepComponent struct {
	Name         string   `json:"name"`
	Raw          *float64 `json:"raw"`
	Norm         *float64 `json:"norm"`
	Weight       float64  `json:"weight"`
	Contribution *float64 `json:"contribution"`
	Saturated    bool     `json:"saturated"`
	Available    bool     `json:"available"`
}

// finiteOrNil returns a pointer to v, or nil when v is not finite so it
// marshals as JSON null.
func finiteOrNil(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	out := v
	return &out
}

// toSweepComponents converts analysis rows into their JSON-safe mirror.
func toSweepComponents(in []analysis.Component) []sweepComponent {
	out := make([]sweepComponent, 0, len(in))
	for _, c := range in {
		out = append(out, sweepComponent{
			Name:         c.Name,
			Raw:          finiteOrNil(c.Raw),
			Norm:         finiteOrNil(c.Norm),
			Weight:       c.Weight,
			Contribution: finiteOrNil(c.Contribution),
			Saturated:    c.Saturated,
			Available:    c.Available,
		})
	}
	return out
}

// sweepPoint is one evaluated position in the knob box.
//
// Metrics carries the PRIMARY profile's measurement. Only the score-shaped
// fields (Score, ScoreProfile, ScoreNorms and the *Norm components) differ
// between profiles; every raw metric is identical, so recording one profile's
// Metrics loses nothing. The per-profile scores live in Scores and the
// per-profile weighting breakdown in Components.
type sweepPoint struct {
	Index      int                         `json:"index"`
	Stage      string                      `json:"stage"`
	Knob       string                      `json:"knob,omitempty"`
	Pos        []float64                   `json:"pos"`
	Knobs      map[string]float64          `json:"knobs"`
	Scores     map[string]float64          `json:"scores"`
	Metrics    analysis.Metrics            `json:"metrics"`
	Components map[string][]sweepComponent `json:"components"`
	// Err records an evaluation failure. A failed point keeps its position so
	// the report stays a faithful log of what was attempted.
	Err string `json:"error,omitempty"`
}

// ok reports whether the point carries usable scores.
func (p sweepPoint) ok() bool { return p.Err == "" && len(p.Scores) > 0 }

// sweepSample is a planned evaluation: everything known before the render.
type sweepSample struct {
	Stage string
	Knob  string
	Pos   []float64
	Cand  candidate
}

// sweepSpan summarises one knob's score range under one profile.
type sweepSpan struct {
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
	Span        float64 `json:"span"`
	ArgminValue float64 `json:"argmin_value"`
}

// sweepSensitivity is the one-at-a-time result for a single knob.
type sweepSensitivity struct {
	Knob     string               `json:"knob"`
	Profiles map[string]sweepSpan `json:"profiles"`
	// Monotonic reports whether the PRIMARY profile's score varies
	// monotonically along the knob's line. A monotone slice means the optimum
	// sits on a bound; a non-monotone one means the interior matters.
	Monotonic bool `json:"monotonic"`
}

// sweepParetoEntry is one member of the non-dominated set.
type sweepParetoEntry struct {
	Index  int                `json:"index"`
	Scores map[string]float64 `json:"scores"`
	Knobs  map[string]float64 `json:"knobs"`
}

// sweepReport is the artifact written by --sweep.
type sweepReport struct {
	Schema string `json:"schema"`

	ReferencePath       string  `json:"reference_path"`
	PresetPath          string  `json:"preset_path"`
	Note                int     `json:"note"`
	Velocity            int     `json:"velocity"`
	ReleaseAfterSeconds float64 `json:"release_after_seconds"`
	SampleRate          int     `json:"sample_rate"`

	Pass       string      `json:"pass"`
	PassWindow *windowSpec `json:"pass_window,omitempty"`

	Profiles       []string          `json:"profiles"`
	PrimaryProfile string            `json:"primary_profile"`
	ScoreNorms     map[string]string `json:"score_norms"`

	Knobs []sweepKnob `json:"knobs"`

	Samples       int    `json:"samples"`
	JointEvals    int    `json:"joint_evals"`
	JointSequence string `json:"joint_sequence"`
	JointSkip     int    `json:"joint_skip"`
	Deduped       int    `json:"deduped"`

	Baseline sweepPoint   `json:"baseline"`
	OAT      []sweepPoint `json:"oat"`
	Joint    []sweepPoint `json:"joint"`

	Sensitivity []sweepSensitivity `json:"sensitivity"`
	Pareto      []sweepParetoEntry `json:"pareto"`

	ConstrainedBest  *sweepParetoEntry `json:"constrained_best,omitempty"`
	ConstrainedCount int               `json:"constrained_count"`

	Evals          int     `json:"evals"`
	Errors         int     `json:"errors"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
}

// sweepRunConfig parameterises runSweep. Everything that touches audio is
// behind eval, so the runner itself is pure.
type sweepRunConfig struct {
	defs     []knobDef
	baseline candidate
	eval     sweepEvaluator

	profiles []string
	primary  string

	samples      int
	jointEvals   int
	jointSkip    int
	jointMaxDims int
	workers      int

	// Report metadata, copied through verbatim.
	referencePath string
	presetPath    string
	note          int
	velocity      int
	releaseAfter  float64
	sampleRate    int
	pass          string
	passWindow    *windowSpec
}

// radicalInverse returns the base-b radical inverse of index, i.e. the digits
// of index in base b mirrored around the radix point. It is the building block
// of the Halton sequence and is exactly reproducible: no state, no seed.
func radicalInverse(index int, base int) float64 {
	if base < 2 || index < 0 {
		return 0
	}
	result := 0.0
	f := 1.0 / float64(base)
	for i := index; i > 0; i /= base {
		result += float64(i%base) * f
		f /= float64(base)
	}
	return result
}

// haltonPoint returns the index-th point of the dims-dimensional Halton
// sequence. Index 1 is the first point (dim 0 = 1/2, dim 1 = 1/3); index 0 is
// the degenerate origin and is normally skipped.
func haltonPoint(index int, dims int) ([]float64, error) {
	if dims < 1 {
		return nil, fmt.Errorf("halton: dims must be >= 1, got %d", dims)
	}
	if dims > len(haltonPrimes) {
		return nil, fmt.Errorf("halton: %d dimensions exceed the %d-prime base table", dims, len(haltonPrimes))
	}
	out := make([]float64, dims)
	for d := 0; d < dims; d++ {
		out[d] = radicalInverse(index, haltonPrimes[d])
	}
	return out, nil
}

// generateOATSamples builds the one-at-a-time scan: for every knob, `samples`
// evenly spaced positions across [0,1] with endpoints included, every other
// coordinate pinned at the baseline.
//
// It deliberately does NOT deduplicate: the caller counts what dedupeSamples
// removes, and a caller that wants the raw plan (a test, say) can have it.
func generateOATSamples(defs []knobDef, baseline candidate, samples int) ([]sweepSample, error) {
	if samples < 2 {
		return nil, fmt.Errorf("--sweep-samples must be >= 2 (endpoints are inclusive), got %d", samples)
	}
	if len(defs) == 0 {
		return nil, fmt.Errorf("no knobs to sweep")
	}
	basePos := toNormalized(baseline, defs)
	out := make([]sweepSample, 0, len(defs)*samples)
	for k := range defs {
		for s := 0; s < samples; s++ {
			pos := append([]float64(nil), basePos...)
			pos[k] = float64(s) / float64(samples-1)
			out = append(out, sweepSample{
				Stage: sweepStageOAT,
				Knob:  defs[k].Name,
				Pos:   pos,
				Cand:  fromNormalized(pos, defs),
			})
		}
	}
	return out, nil
}

// generateJointSamples builds the low-discrepancy joint stage.
//
// A star-shaped OAT scan probes one line per knob through a single point and
// says nothing at all about interactions between knobs, so it cannot answer
// "is the trade-off a property of the box?" on its own. Halton fills the box
// with no RNG whatsoever, which makes the whole report reproducible by
// construction rather than by pinning a seed.
func generateJointSamples(defs []knobDef, evals int, skip int, maxDims int) ([]sweepSample, error) {
	if evals <= 0 {
		return nil, nil
	}
	if len(defs) > maxDims {
		return nil, fmt.Errorf(
			"joint stage refuses %d dimensions above the %d-dimension cap; raise --sweep-joint-max-dims to continue",
			len(defs), maxDims,
		)
	}
	if skip < 0 {
		skip = 0
	}
	out := make([]sweepSample, 0, evals)
	for i := 0; i < evals; i++ {
		pos, err := haltonPoint(skip+i+1, len(defs))
		if err != nil {
			return nil, err
		}
		out = append(out, sweepSample{
			Stage: sweepStageJoint,
			Pos:   pos,
			Cand:  fromNormalized(pos, defs),
		})
	}
	return out, nil
}

// dedupeSamples drops samples that collapse onto the baseline after bounds
// clamping and IsInt rounding, and reports how many it dropped.
//
// This is the same trick the polish stage uses: an integer knob has a smallest
// meaningful normalised step, so a fine grid over it produces repeats that
// would each cost a full render for a number already known.
func dedupeSamples(samples []sweepSample, baseline candidate) ([]sweepSample, int) {
	baseKey := candidateKey(baseline)
	out := make([]sweepSample, 0, len(samples))
	dropped := 0
	for _, s := range samples {
		if candidateKey(s.Cand) == baseKey {
			dropped++
			continue
		}
		out = append(out, s)
	}
	return out, dropped
}

// parseSweepProfiles resolves --sweep-profiles. An empty list means "the pass
// profile first, then legacy-v1", which is exactly the pair the sustain
// question is about: what the pass optimises versus what the docs track.
func parseSweepProfiles(raw string, passProfile string) ([]string, error) {
	fields := strings.Split(raw, ",")
	if strings.TrimSpace(raw) == "" {
		fields = []string{passProfile, analysis.ProfileLegacyV1}
	}
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		name := strings.ToLower(strings.TrimSpace(f))
		if name == "" {
			continue
		}
		if _, err := analysis.WeightsForProfile(name); err != nil {
			return nil, fmt.Errorf("unknown sweep profile %q (known: %v)", name, analysis.Profiles())
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no sweep profiles selected")
	}
	return out, nil
}

// newSweepEvaluator renders a candidate once and compares it against the
// reference under every profile.
//
// The second (and any further) Compare is deliberately a plain extra call
// rather than a new analysis.Rescore helper: the render dominates the per-eval
// cost by orders of magnitude, so the extra Compare is nearly free, and doing
// it this way keeps any bit-identity risk out of analysis/ entirely.
func newSweepEvaluator(cfg *optimizationConfig, profiles []string, window windowSpec, settings evalSettings) (sweepEvaluator, error) {
	if len(cfg.targets) != 1 {
		return nil, fmt.Errorf("sweep expects exactly one note target, got %d", len(cfg.targets))
	}
	weights := make([]analysis.Weights, 0, len(profiles))
	for _, name := range profiles {
		w, err := analysis.WeightsForProfile(name)
		if err != nil {
			return nil, err
		}
		weights = append(weights, w)
	}
	target := cfg.targets[0]

	return func(cand candidate) (map[string]analysis.Metrics, error) {
		irCfgs, params, velocity, releaseAfter := applyCandidate(
			cfg.baseParams, settings.sampleRate, cfg.baseVelocity, cfg.baseReleaseAfter, cfg.defs, cand,
		)
		var bodyIR, roomL, roomR []float32
		if needsIRSynthesis(cfg.groups) {
			var err error
			if bodyIR, roomL, roomR, err = synthesizeIRs(cfg, irCfgs); err != nil {
				return nil, err
			}
			params.IRWavPath = ""
			params.BodyIRWavPath = ""
			params.RoomIRWavPath = ""
		}
		mono, err := renderTarget(cfg, params, bodyIR, roomL, roomR, target.note, velocity, releaseAfter, settings)
		if err != nil {
			return nil, err
		}
		ref := window.slice(referenceFor(target, settings), settings.sampleRate)
		cut := window.slice(mono, settings.sampleRate)

		out := make(map[string]analysis.Metrics, len(profiles))
		for i, name := range profiles {
			out[name] = analysis.CompareWithOptions(ref, cut, settings.sampleRate, analysis.Options{
				Weights:  weights[i],
				MIDINote: target.note,
			})
		}
		return out, nil
	}, nil
}

// evaluateSweepPoint turns one planned sample into a report point.
func evaluateSweepPoint(index int, s sweepSample, defs []knobDef, eval sweepEvaluator, profiles []string, primary string) sweepPoint {
	point := sweepPoint{
		Index: index,
		Stage: s.Stage,
		Knob:  s.Knob,
		Pos:   append([]float64(nil), s.Pos...),
		Knobs: make(map[string]float64, len(defs)),
	}
	for i, d := range defs {
		if i < len(s.Cand.Vals) {
			point.Knobs[d.Name] = s.Cand.Vals[i]
		}
	}

	metrics, err := eval(s.Cand)
	if err != nil {
		point.Err = err.Error()
		return point
	}

	point.Scores = make(map[string]float64, len(profiles))
	point.Components = make(map[string][]sweepComponent, len(profiles))
	for _, name := range profiles {
		m, ok := metrics[name]
		if !ok {
			continue
		}
		point.Scores[name] = m.Score
		w, werr := analysis.WeightsForProfile(name)
		if werr == nil {
			point.Components[name] = toSweepComponents(analysis.Components(m, w))
		}
		if name == primary {
			// Sanitized() before the Metrics ever reaches encoding/json: an
			// un-fittable decay slope legitimately yields NaN and json refuses
			// it, which has crashed report writing before.
			point.Metrics = m.Sanitized()
		}
	}
	return point
}

// evaluateSweepSamples renders every planned sample, in parallel, into a
// result slice indexed by plan position.
//
// Workers pull an atomic index and write into results[i], so the output is
// byte-identical regardless of --workers and of scheduling order. That is the
// whole point: a sweep whose report depends on how many cores ran it is not a
// measurement.
func evaluateSweepSamples(samples []sweepSample, defs []knobDef, eval sweepEvaluator, profiles []string, primary string, workers int, offset int) []sweepPoint {
	results := make([]sweepPoint, len(samples))
	if len(samples) == 0 {
		return results
	}
	if workers < 1 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > len(samples) {
		workers = len(samples)
	}

	var next int64 = -1
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= len(samples) {
					return
				}
				results[i] = evaluateSweepPoint(offset+i, samples[i], defs, eval, profiles, primary)
			}
		}()
	}
	wg.Wait()
	return results
}

// computeSensitivity summarises one knob's slice. points must already be
// sorted by the knob's normalised coordinate and should include the baseline,
// which lies on every knob's line.
func computeSensitivity(knob string, points []sweepPoint, profiles []string, primary string) sweepSensitivity {
	out := sweepSensitivity{Knob: knob, Profiles: make(map[string]sweepSpan, len(profiles)), Monotonic: true}
	for _, profile := range profiles {
		span := sweepSpan{Min: math.Inf(1), Max: math.Inf(-1)}
		found := false
		for _, p := range points {
			v, ok := p.Scores[profile]
			if !ok || !isFiniteFloat(v) {
				continue
			}
			found = true
			if v < span.Min {
				span.Min = v
				span.ArgminValue = p.Knobs[knob]
			}
			if v > span.Max {
				span.Max = v
			}
		}
		if !found {
			continue
		}
		span.Span = span.Max - span.Min
		out.Profiles[profile] = span
	}

	// Monotonicity of the primary profile along the line.
	const eps = 1e-12
	series := make([]float64, 0, len(points))
	for _, p := range points {
		if v, ok := p.Scores[primary]; ok && isFiniteFloat(v) {
			series = append(series, v)
		}
	}
	nonDecreasing, nonIncreasing := true, true
	for i := 1; i < len(series); i++ {
		d := series[i] - series[i-1]
		if d < -eps {
			nonDecreasing = false
		}
		if d > eps {
			nonIncreasing = false
		}
	}
	out.Monotonic = nonDecreasing || nonIncreasing
	return out
}

func isFiniteFloat(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// paretoEntry projects a point into a front entry.
func paretoEntry(p sweepPoint) sweepParetoEntry {
	entry := sweepParetoEntry{Index: p.Index, Scores: make(map[string]float64, len(p.Scores)), Knobs: make(map[string]float64, len(p.Knobs))}
	for k, v := range p.Scores {
		entry.Scores[k] = v
	}
	for k, v := range p.Knobs {
		entry.Knobs[k] = v
	}
	return entry
}

// paretoFront extracts the non-dominated set on (primary, secondary), BOTH
// minimised: a dominates b iff a is no worse in both objectives and strictly
// better in at least one.
//
// Exact duplicates in objective space collapse onto their lowest-index
// representative — weak dominance would otherwise keep every copy, which
// inflates the front without adding information. The result is sorted by
// primary ascending, ties broken by index.
func paretoFront(points []sweepPoint, primary string, secondary string) []sweepParetoEntry {
	type scored struct {
		point sweepPoint
		a, b  float64
	}
	usable := make([]scored, 0, len(points))
	for _, p := range points {
		if !p.ok() {
			continue
		}
		a, okA := p.Scores[primary]
		b, okB := p.Scores[secondary]
		if !okA || !okB || !isFiniteFloat(a) || !isFiniteFloat(b) {
			continue
		}
		usable = append(usable, scored{point: p, a: a, b: b})
	}

	// The lowest-index guarantee above is only real if the scan meets the
	// duplicates in index order, which is not something a caller's point
	// ordering can be trusted to provide.
	sort.SliceStable(usable, func(i, j int) bool { return usable[i].point.Index < usable[j].point.Index })

	front := make([]sweepParetoEntry, 0, 8)
	seen := make(map[[2]float64]bool, len(usable))
	for i, cand := range usable {
		dominated := false
		for j, other := range usable {
			if i == j {
				continue
			}
			if other.a <= cand.a && other.b <= cand.b && (other.a < cand.a || other.b < cand.b) {
				dominated = true
				break
			}
		}
		if dominated {
			continue
		}
		key := [2]float64{cand.a, cand.b}
		if seen[key] {
			continue
		}
		seen[key] = true
		front = append(front, paretoEntry(cand.point))
	}

	sort.Slice(front, func(i, j int) bool {
		ai, aj := front[i].Scores[primary], front[j].Scores[primary]
		if ai == aj {
			return front[i].Index < front[j].Index
		}
		return ai < aj
	})
	return front
}

// constrainedBest returns the best point that improves the primary objective
// without regressing the secondary one, plus how many points satisfy the
// secondary constraint.
//
// Both constraints matter and they are not the same: a point qualifies for
// constrainedBest only if its secondary score is no worse than secondaryCap
// AND its primary score is strictly better than primaryCap (both baseline
// scores). Without the primary test a run in which every non-regressing sample
// is *worse* on the primary objective would still return the least-bad one,
// and the report and console read a non-nil result as "a non-regressing
// improvement region exists" — a false positive.
//
// The returned count keeps the documented secondary-only meaning ("how many
// sampled points hold the secondary line"); it is a property of the sampled
// set, not of the answer.
//
// This is the headline number: "did anything buy decay without paying in
// legacy?" A nil result means nothing in the sampled set did.
func constrainedBest(points []sweepPoint, primary string, secondary string, primaryCap, secondaryCap float64) (*sweepParetoEntry, int) {
	var best *sweepPoint
	count := 0
	for i := range points {
		p := points[i]
		if !p.ok() {
			continue
		}
		a, okA := p.Scores[primary]
		b, okB := p.Scores[secondary]
		if !okA || !okB || !isFiniteFloat(a) || !isFiniteFloat(b) {
			continue
		}
		if b > secondaryCap {
			continue
		}
		count++
		// The count above is the secondary-only tally; only a strict primary
		// improvement makes a point eligible to be the answer.
		if a >= primaryCap {
			continue
		}
		if best == nil {
			best = &points[i]
			continue
		}
		bestA := best.Scores[primary]
		// Ties break by index so the answer does not depend on iteration
		// order.
		if a < bestA || (a == bestA && p.Index < best.Index) {
			best = &points[i]
		}
	}
	if best == nil {
		return nil, count
	}
	entry := paretoEntry(*best)
	return &entry, count
}

// runSweep executes the whole deterministic sweep and builds the report.
func runSweep(cfg sweepRunConfig) (*sweepReport, error) {
	start := time.Now()
	if len(cfg.defs) == 0 {
		return nil, fmt.Errorf("no knobs to sweep")
	}
	if len(cfg.profiles) == 0 {
		return nil, fmt.Errorf("no sweep profiles selected")
	}
	primary := cfg.primary
	if primary == "" {
		primary = cfg.profiles[0]
	}

	report := &sweepReport{
		Schema:              sweepSchema,
		ReferencePath:       cfg.referencePath,
		PresetPath:          cfg.presetPath,
		Note:                cfg.note,
		Velocity:            cfg.velocity,
		ReleaseAfterSeconds: cfg.releaseAfter,
		SampleRate:          cfg.sampleRate,
		Pass:                cfg.pass,
		PassWindow:          cfg.passWindow,
		Profiles:            append([]string(nil), cfg.profiles...),
		PrimaryProfile:      primary,
		ScoreNorms:          make(map[string]string, len(cfg.profiles)),
		Samples:             cfg.samples,
		JointEvals:          cfg.jointEvals,
		JointSequence:       sweepJointSequence,
		JointSkip:           cfg.jointSkip,
		Sensitivity:         []sweepSensitivity{},
		Pareto:              []sweepParetoEntry{},
		OAT:                 []sweepPoint{},
		Joint:               []sweepPoint{},
	}
	for _, name := range cfg.profiles {
		w, err := analysis.WeightsForProfile(name)
		if err != nil {
			return nil, err
		}
		report.ScoreNorms[name] = analysis.NormsGeneration(w.Norms)
	}

	basePos := toNormalized(cfg.baseline, cfg.defs)
	for i, d := range cfg.defs {
		v := 0.0
		if i < len(cfg.baseline.Vals) {
			v = cfg.baseline.Vals[i]
		}
		report.Knobs = append(report.Knobs, sweepKnob{
			Name: d.Name, Min: d.Min, Max: d.Max,
			LogScale: d.LogScale, IsInt: d.IsInt, BaselineValue: v,
		})
	}

	// Baseline first: everything downstream is relative to it, and its
	// reproduction against a known preset is the acceptance test for the whole
	// tool.
	baseline := evaluateSweepPoint(0, sweepSample{
		Stage: sweepStageBaseline, Pos: basePos, Cand: cloneCandidate(cfg.baseline),
	}, cfg.defs, cfg.eval, cfg.profiles, primary)
	if !baseline.ok() {
		return nil, fmt.Errorf("baseline evaluation failed: %s", baseline.Err)
	}
	report.Baseline = baseline
	report.Evals = 1

	oatPlan, err := generateOATSamples(cfg.defs, cfg.baseline, cfg.samples)
	if err != nil {
		return nil, err
	}
	jointPlan, err := generateJointSamples(cfg.defs, cfg.jointEvals, cfg.jointSkip, cfg.jointMaxDims)
	if err != nil {
		return nil, err
	}

	oatPlan, oatDropped := dedupeSamples(oatPlan, cfg.baseline)
	jointPlan, jointDropped := dedupeSamples(jointPlan, cfg.baseline)
	report.Deduped = oatDropped + jointDropped

	report.OAT = evaluateSweepSamples(oatPlan, cfg.defs, cfg.eval, cfg.profiles, primary, cfg.workers, 1)
	report.Joint = evaluateSweepSamples(jointPlan, cfg.defs, cfg.eval, cfg.profiles, primary, cfg.workers, 1+len(oatPlan))
	report.Evals += len(report.OAT) + len(report.Joint)

	all := make([]sweepPoint, 0, len(report.OAT)+len(report.Joint))
	all = append(all, report.OAT...)
	all = append(all, report.Joint...)
	for _, p := range all {
		if !p.ok() {
			report.Errors++
		}
	}

	// Sensitivity: each knob's line, with the baseline inserted at its own
	// coordinate so the slice covers the point the OAT star radiates from.
	for k, d := range cfg.defs {
		line := []sweepPoint{baseline}
		for _, p := range report.OAT {
			if p.Knob == d.Name && p.ok() {
				line = append(line, p)
			}
		}
		sort.SliceStable(line, func(i, j int) bool { return line[i].Pos[k] < line[j].Pos[k] })
		report.Sensitivity = append(report.Sensitivity, computeSensitivity(d.Name, line, cfg.profiles, primary))
	}

	// Pareto extraction needs a second objective; with one profile there is
	// nothing to trade off against, so the stage is skipped rather than
	// producing a degenerate one-point front.
	if len(cfg.profiles) >= 2 {
		secondary := cfg.profiles[1]
		if secondary == primary && len(cfg.profiles) > 2 {
			secondary = cfg.profiles[2]
		}
		report.Pareto = paretoFront(all, primary, secondary)
		secondaryCap := baseline.Scores[secondary]
		primaryCap := baseline.Scores[primary]
		report.ConstrainedBest, report.ConstrainedCount = constrainedBest(all, primary, secondary, primaryCap, secondaryCap)
	}

	report.ElapsedSeconds = time.Since(start).Seconds()
	return report, nil
}

// writeSweepReport marshals the report to path, creating parent directories.
func writeSweepReport(path string, report *sweepReport) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// printSweepReport writes the human-readable summary to stdout.
func printSweepReport(report *sweepReport) {
	primary := report.PrimaryProfile
	// With a single requested profile there is no second objective. Falling
	// back to primary keeps the sensitivity table honest — an empty name would
	// print a blank column header and a column of 0.0000 spans read out of a
	// missing map entry.
	secondary := primary
	for _, p := range report.Profiles {
		if p != primary {
			secondary = p
			break
		}
	}

	fmt.Printf("Baseline: ")
	printScoreLine(report.Baseline.Scores, report.Profiles)
	fmt.Printf("  time_rmse=%.4f envelope=%.2f dB spectral=%.2f dB decay_diff=%.2f dB/s\n",
		report.Baseline.Metrics.TimeRMSE, report.Baseline.Metrics.EnvelopeRMSEDB,
		report.Baseline.Metrics.SpectralRMSEDB, report.Baseline.Metrics.DecayDiffDBPerS)

	fmt.Printf("\nSensitivity (one-at-a-time, %d samples per knob):\n", report.Samples)
	fmt.Printf("  %-24s %-22s %12s %12s %14s %s\n", "knob", "range", primary, secondary, "argmin", "monotonic")
	for _, s := range report.Sensitivity {
		var def sweepKnob
		for _, k := range report.Knobs {
			if k.Name == s.Knob {
				def = k
				break
			}
		}
		primarySpan := s.Profiles[primary]
		secondarySpan := s.Profiles[secondary]
		fmt.Printf("  %-24s %-22s %12.4f %12.4f %14.6g %v\n",
			s.Knob,
			fmt.Sprintf("[%g, %g]", def.Min, def.Max),
			primarySpan.Span, secondarySpan.Span, primarySpan.ArgminValue, s.Monotonic)
	}

	if len(report.Pareto) == 0 {
		fmt.Printf("\nPareto: skipped (needs two profiles; got %v)\n", report.Profiles)
	} else {
		fmt.Printf("\nPareto front on (%s, %s), both minimised — %d point(s):\n", primary, secondary, len(report.Pareto))
		for i, e := range report.Pareto {
			if i >= 12 {
				fmt.Printf("  ... %d more\n", len(report.Pareto)-i)
				break
			}
			fmt.Printf("  #%-6d %s=%.4f %s=%.4f\n", e.Index, primary, e.Scores[primary], secondary, e.Scores[secondary])
		}
		fmt.Printf("\nConstrained best (%s < baseline %.4f and %s <= baseline %.4f): ",
			primary, report.Baseline.Scores[primary], secondary, report.Baseline.Scores[secondary])
		if report.ConstrainedBest == nil {
			fmt.Printf("none — no sampled point improves %s without regressing %s\n", primary, secondary)
		} else {
			fmt.Printf("#%d %s=%.4f %s=%.4f\n", report.ConstrainedBest.Index,
				primary, report.ConstrainedBest.Scores[primary],
				secondary, report.ConstrainedBest.Scores[secondary])
		}
		fmt.Printf("Constrained count: %d of %d sampled points hold %s <= baseline\n",
			report.ConstrainedCount, report.Evals-1, secondary)
	}
	fmt.Printf("\nEvals=%d errors=%d deduped=%d elapsed=%.1fs\n",
		report.Evals, report.Errors, report.Deduped, report.ElapsedSeconds)
}

func printScoreLine(scores map[string]float64, profiles []string) {
	parts := make([]string, 0, len(profiles))
	for _, p := range profiles {
		parts = append(parts, fmt.Sprintf("%s=%.4f", p, scores[p]))
	}
	fmt.Println(strings.Join(parts, " "))
}

// sweepModeArgs carries the --sweep-* flag values into the glue below.
type sweepModeArgs struct {
	outPath      string
	samples      int
	jointEvals   int
	jointSkip    int
	jointMaxDims int
	profilesRaw  string
	passProfile  string
	passName     string
	passWindow   *windowSpec
	note         int
	workers      int
	timeBudget   float64
}

// sweepEvalRate is the measured throughput of a final-settings evaluation on a
// 12-core box (out/passes/sustain.json.report.json: 2645 evals in 181.2 s). It
// only feeds the up-front ETA, so being off by a factor is harmless.
const sweepEvalRate = 14.6

// runSweepMode is the --sweep entry point: it wires the CLI flags into
// runSweep, prints the tables and writes the report. It never writes a preset,
// a run report or a checkpoint.
func runSweepMode(cfg *optimizationConfig, args sweepModeArgs) {
	profiles, err := parseSweepProfiles(args.profilesRaw, args.passProfile)
	if err != nil {
		die("invalid --sweep-profiles: %v", err)
	}
	if len(profiles) < 2 {
		fmt.Printf("sweep: only one profile (%s) requested - Pareto extraction and constrained_best are skipped\n", profiles[0])
	}

	// --time-budget is ignored on purpose: a wall-clock cutoff would drop a
	// different tail of the sample plan on every machine, and a report that
	// cannot be reproduced is not a measurement. This is a deliberate
	// inconsistency with the optimizer, which is budget-driven by design.
	fmt.Printf("sweep: --time-budget (%.0fs) ignored (the sample plan is fixed, so the run stays reproducible)\n", args.timeBudget)

	if args.passWindow != nil {
		fmt.Printf("sweep: WARNING pass window %g:%g is set - BOTH profile scores are windowed and are NOT comparable "+
			"to `just distance-c4` or to any full-signal number in the docs\n", args.passWindow.StartSec, args.passWindow.EndSec)
	}

	settings := evalSettings{
		final:           true,
		sampleRate:      cfg.finalSampleRate,
		minDuration:     cfg.finalMinDuration,
		maxDuration:     cfg.finalMaxDuration,
		decayDBFS:       cfg.decayDBFS,
		decayHoldBlocks: cfg.decayHoldBlocks,
		renderBlockSize: cfg.renderBlockSize,
	}

	window := windowSpec{}
	if args.passWindow != nil {
		window = *args.passWindow
	}
	eval, err := newSweepEvaluator(cfg, profiles, window, settings)
	if err != nil {
		die("sweep: %v", err)
	}

	planned := 1 + len(cfg.defs)*args.samples + maxInt(args.jointEvals, 0)
	fmt.Printf("Sweep: %d knobs, %d OAT samples each, %d joint Halton points (skip %d) = %d evals at final settings\n",
		len(cfg.defs), args.samples, args.jointEvals, args.jointSkip, planned)
	fmt.Printf("ETA ~%.0fs at the measured %.1f evals/s\n", float64(planned)/sweepEvalRate, sweepEvalRate)
	for _, d := range cfg.defs {
		fmt.Printf("  knob %-24s [%g, %g]\n", d.Name, d.Min, d.Max)
	}

	report, err := runSweep(sweepRunConfig{
		defs:          cfg.defs,
		baseline:      cfg.initCandidate,
		eval:          eval,
		profiles:      profiles,
		primary:       profiles[0],
		samples:       args.samples,
		jointEvals:    args.jointEvals,
		jointSkip:     args.jointSkip,
		jointMaxDims:  args.jointMaxDims,
		workers:       args.workers,
		referencePath: cfg.referencePath,
		presetPath:    cfg.presetPath,
		note:          args.note,
		velocity:      cfg.baseVelocity,
		releaseAfter:  cfg.baseReleaseAfter,
		sampleRate:    cfg.finalSampleRate,
		pass:          args.passName,
		passWindow:    args.passWindow,
	})
	if err != nil {
		die("sweep failed: %v", err)
	}

	printSweepReport(report)

	out := args.outPath
	if out == "" {
		name := args.passName
		if name == "" {
			name = passNone
		}
		out = filepath.Join("out", "sweep", fmt.Sprintf("%s-note%d.json", name, args.note))
	}
	if err := writeSweepReport(out, report); err != nil {
		die("failed to write sweep report: %v", err)
	}
	fmt.Printf("Wrote %s\n", out)
	fmt.Println("Every sample carries its full analysis.Metrics there, so the gate constraint stays a post-hoc filter " +
		"rather than a coupling to the thresholds loader: " +
		"jq '[.oat[],.joint[]] | map(select(.metrics.time_rmse <= 0.112))' " + out)
}
