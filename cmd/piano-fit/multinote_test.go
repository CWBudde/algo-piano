package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
)

// syntheticReference generates a deterministic, exponentially decaying sum of
// inharmonic partials for a MIDI note. It stands in for a reference WAV so the
// multi-note tests need no audio fixtures (only reference/c4.wav exists in this
// repo, and it is gitignored).
func syntheticReference(t *testing.T, note, sampleRate int, seconds float64) []float64 {
	t.Helper()
	if sampleRate <= 0 || seconds <= 0 {
		t.Fatalf("syntheticReference: bad sampleRate=%d seconds=%g", sampleRate, seconds)
	}
	f0 := 440.0 * math.Pow(2, float64(note-69)/12.0)
	const (
		partials      = 6
		inharmonicity = 0.0004
	)
	n := int(float64(sampleRate) * seconds)
	out := make([]float64, n)
	for p := 1; p <= partials; p++ {
		fp := f0 * float64(p) * math.Sqrt(1+inharmonicity*float64(p*p))
		if fp >= float64(sampleRate)/2 {
			break
		}
		amp := 1.0 / float64(p)
		decay := 2.0 + 0.9*float64(p)
		w := 2 * math.Pi * fp / float64(sampleRate)
		phase := 0.37 * float64(p) // deterministic, partial-dependent offset
		for i := 0; i < n; i++ {
			tt := float64(i) / float64(sampleRate)
			out[i] += amp * math.Exp(-decay*tt) * math.Sin(w*float64(i)+phase)
		}
	}
	return out
}

// testEvalSettings is a fast rendering configuration: at 8 kHz with a 0.1-0.3 s
// render a two-note evaluation completes in well under a second.
func testEvalSettings() evalSettings {
	return evalSettings{
		sampleRate:      8000,
		minDuration:     0.1,
		maxDuration:     0.3,
		decayDBFS:       -90,
		decayHoldBlocks: 6,
		decayRelative:   true,
		renderBlockSize: 64,
	}
}

func testConfig(t *testing.T, notes []int) *optimizationConfig {
	t.Helper()
	settings := testEvalSettings()
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true}
	defs, cand := initCandidate(base, settings.sampleRate, notes, 100, 0.2, groups, false)

	targets := make([]noteTarget, 0, len(notes))
	for _, n := range notes {
		ref := syntheticReference(t, n, settings.sampleRate, 0.3)
		targets = append(targets, noteTarget{
			note:          n,
			referencePath: "synthetic",
			optRef:        ref,
			finalRef:      ref,
			weight:        1.0,
		})
	}

	return &optimizationConfig{
		targets:          targets,
		aggregate:        aggregateMean,
		baseParams:       base,
		defs:             defs,
		initCandidate:    cand,
		baseVelocity:     100,
		baseReleaseAfter: 0.2,
		sampleRate:       settings.sampleRate,
		finalSampleRate:  settings.sampleRate,
		groups:           groups,
	}
}

func TestParseNoteReferenceMap(t *testing.T) {
	t.Run("whitespace", func(t *testing.T) {
		got, err := parseNoteReferenceMap(" 48 = reference/c3.wav , 60=reference/c4.wav ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[int]string{48: "reference/c3.wav", 60: "reference/c4.wav"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got, err := parseNoteReferenceMap("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", got)
		}
	})

	t.Run("duplicate note", func(t *testing.T) {
		if _, err := parseNoteReferenceMap("60=a.wav,60=b.wav"); err == nil {
			t.Fatal("expected error for duplicate note")
		}
	})

	t.Run("missing equals", func(t *testing.T) {
		if _, err := parseNoteReferenceMap("60,reference/c4.wav"); err == nil {
			t.Fatal("expected error for missing =")
		}
	})

	t.Run("bad note", func(t *testing.T) {
		if _, err := parseNoteReferenceMap("c4=reference/c4.wav"); err == nil {
			t.Fatal("expected error for non-numeric note")
		}
	})

	t.Run("empty path", func(t *testing.T) {
		if _, err := parseNoteReferenceMap("60="); err == nil {
			t.Fatal("expected error for empty path")
		}
	})
}

func TestResolveReferencesSingleNoteFallback(t *testing.T) {
	got, err := resolveReferences([]int{60}, map[int]string{}, "reference/c4.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "reference/c4.wav" {
		t.Fatalf("got %v, want [reference/c4.wav]", got)
	}

	// An explicit mapping wins over --reference.
	got, err = resolveReferences([]int{60}, map[int]string{60: "other.wav"}, "reference/c4.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != "other.wav" {
		t.Fatalf("got %v, want [other.wav]", got)
	}
}

func TestResolveReferencesMissingNoteErrors(t *testing.T) {
	_, err := resolveReferences([]int{48, 60, 72}, map[int]string{60: "c4.wav"}, "reference/c4.wav")
	if err == nil {
		t.Fatal("expected error listing the unmapped notes")
	}
	for _, want := range []string{"48", "72"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention note %s", err, want)
		}
	}
}

func TestAggregateScores(t *testing.T) {
	reports := []noteReport{
		{Note: 48, Score: 0.2},
		{Note: 60, Score: 0.4},
		{Note: 72, Score: 0.6},
	}

	tests := []struct {
		name    string
		mode    string
		weights []float64
		want    float64
	}{
		{name: "mean uniform", mode: aggregateMean, want: 0.4},
		{name: "max uniform", mode: aggregateMax, want: 0.6},
		{name: "mean-max uniform", mode: aggregateMeanMax, want: 0.4 + 0.25*0.2},
		{name: "mean weighted", mode: aggregateMean, weights: []float64{1, 2, 1}, want: (0.2 + 0.8 + 0.6) / 4},
		{name: "max weighted ignores weights", mode: aggregateMax, weights: []float64{5, 1, 1}, want: 0.6},
		{
			name: "mean-max weighted", mode: aggregateMeanMax, weights: []float64{1, 2, 1},
			want: 0.4 + 0.25*(0.6-0.4),
		},
		{name: "unknown mode falls back to mean", mode: "bogus", want: 0.4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateScores(reports, tt.weights, tt.mode)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("aggregateScores(%s) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}

	t.Run("empty", func(t *testing.T) {
		if got := aggregateScores(nil, nil, aggregateMean); got != 1.0 {
			t.Fatalf("aggregateScores(nil) = %v, want 1.0", got)
		}
	})
}

func TestEvaluateCandidateMultiNote(t *testing.T) {
	cfg := testConfig(t, []int{48, 60})
	settings := testEvalSettings()

	ev, err := evaluateCandidate(cfg, cfg.initCandidate, "", settings)
	if err != nil {
		t.Fatalf("evaluateCandidate: %v", err)
	}
	if len(ev.notes) != 2 {
		t.Fatalf("per-note reports = %d, want 2", len(ev.notes))
	}
	if ev.notes[0].Note != 48 || ev.notes[1].Note != 60 {
		t.Fatalf("note order = %d,%d want 48,60", ev.notes[0].Note, ev.notes[1].Note)
	}
	// Compare the sanitized forms: short synthetic references leave the partial,
	// attack and segment-decay metrics undefined (NaN), and reflect.DeepEqual
	// reports NaN != NaN even when both structs hold the identical value.
	if !reflect.DeepEqual(ev.metrics.Sanitized(), ev.notes[0].Metrics.Sanitized()) {
		t.Fatal("metrics should mirror notes[0].Metrics")
	}
	want := aggregateScores(ev.notes, targetWeights(cfg.targets), aggregateMean)
	if ev.aggregate != want {
		t.Fatalf("aggregate = %v, want %v", ev.aggregate, want)
	}
	for _, nr := range ev.notes {
		if nr.Score < 0 || nr.Score > 1 {
			t.Fatalf("note %d score %v out of [0,1]", nr.Note, nr.Score)
		}
	}
}

func TestSingleNoteAggregateEqualsMetricsScore(t *testing.T) {
	cfg := testConfig(t, []int{60})
	settings := testEvalSettings()

	for _, mode := range []string{aggregateMean, aggregateMax, aggregateMeanMax} {
		cfg.aggregate = mode
		ev, err := evaluateCandidate(cfg, cfg.initCandidate, "", settings)
		if err != nil {
			t.Fatalf("evaluateCandidate(%s): %v", mode, err)
		}
		if ev.aggregate != ev.metrics.Score {
			t.Fatalf("aggregate(%s) = %v, want exactly metrics.Score %v", mode, ev.aggregate, ev.metrics.Score)
		}
	}
}

// TestOutputGainIsScoreInvariant validates the premise of the analytic
// output-gain match: analysis.Compare RMS-normalises both signals
// (analysis/distance.go), and piano.OutputGain is a plain output multiplier
// (piano/engine.go), so changing it cannot meaningfully move the score.
//
// The invariance is exact in the analysis stage (it holds to within one ULP
// when the same float64 buffer is rescaled). Through the renderer it is exact
// only up to float32 quantisation: the engine multiplies every sample by
// OutputGain in float32, so a different gain rounds each sample differently.
// The measured residual is ~3e-10 in score, roughly seven orders of magnitude
// below the smallest improvement the optimizer reports, so output_gain remains
// a flat dimension that search can only waste evaluations on.
func TestOutputGainIsScoreInvariant(t *testing.T) {
	settings := testEvalSettings()
	const note = 60
	ref := syntheticReference(t, note, settings.sampleRate, 0.3)

	// Analysis stage: rescaling the exact same buffer must not move the score.
	t.Run("analysis stage is exact", func(t *testing.T) {
		cand := syntheticReference(t, note+2, settings.sampleRate, 0.3)
		scaled := make([]float64, len(cand))
		for i := range cand {
			scaled[i] = cand[i] * 3.7
		}
		a := analysis.Compare(ref, cand, settings.sampleRate)
		b := analysis.Compare(ref, scaled, settings.sampleRate)
		if math.Abs(a.Score-b.Score) > 1e-15 {
			t.Fatalf("a pure level change moved the score: %.17g vs %.17g", a.Score, b.Score)
		}
	})

	// Full render path: float32 quantisation makes it near-exact, not exact.
	t.Run("render path is invariant to float32 rounding", func(t *testing.T) {
		render := func(gain float32) analysis.Metrics {
			t.Helper()
			params := piano.NewDefaultParams()
			params.OutputGain = gain
			mono, _, err := renderCandidateFromParams(
				params, note, 100, settings.sampleRate,
				settings.decayDBFS, settings.decayHoldBlocks, settings.decayRelative,
				settings.minDuration, settings.maxDuration,
				settings.renderBlockSize, 0.2,
			)
			if err != nil {
				t.Fatalf("render at gain %v: %v", gain, err)
			}
			return analysis.Compare(ref, mono, settings.sampleRate)
		}

		a := render(1.0)
		b := render(3.7)
		if delta := math.Abs(a.Score - b.Score); delta > 1e-7 {
			t.Fatalf("output_gain moved the score by %g: %.17g vs %.17g", delta, a.Score, b.Score)
		}
		if delta := math.Abs(a.Similarity - b.Similarity); delta > 1e-7 {
			t.Fatalf("output_gain moved the similarity by %g: %v vs %v", delta, a.Similarity, b.Similarity)
		}
	})
}

func TestMatchOutputGainSolvesExactly(t *testing.T) {
	ref := syntheticReference(t, 60, 8000, 0.5)
	cand := make([]float64, len(ref))
	for i := range ref {
		cand[i] = ref[i] * 0.137
	}

	ratio := matchOutputGainRatio([]gainPair{{reference: ref, candidate: cand, weight: 1}})

	scaled := make([]float64, len(cand))
	for i := range cand {
		scaled[i] = cand[i] * ratio
	}
	got := rmsOf(scaled) / rmsOf(ref)
	if math.Abs(got-1.0) > 1e-6 {
		t.Fatalf("post-match RMS ratio = %.12f, want 1.0 (ratio %.6f)", got, ratio)
	}
}

func TestMatchOutputGainRatioDegenerateInputs(t *testing.T) {
	if got := matchOutputGainRatio(nil); got != 1.0 {
		t.Fatalf("empty pairs = %v, want 1.0", got)
	}
	silent := make([]float64, 100)
	if got := matchOutputGainRatio([]gainPair{{reference: silent, candidate: silent}}); got != 1.0 {
		t.Fatalf("silent pair = %v, want 1.0", got)
	}
}

func TestParseNotesFlag(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []int
		wantErr bool
	}{
		{name: "empty falls back", raw: "", want: []int{60}},
		{name: "list", raw: "48,60,72", want: []int{48, 60, 72}},
		{name: "whitespace", raw: " 48 , 60 ", want: []int{48, 60}},
		{name: "duplicate", raw: "60,60", wantErr: true},
		{name: "out of range", raw: "200", wantErr: true},
		{name: "non-numeric", raw: "c4", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNotesFlag(tt.raw, 60)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseNoteWeightsAndAggregate(t *testing.T) {
	w, err := parseNoteWeights(" 48=1 , 60=2.5 ")
	if err != nil {
		t.Fatalf("parseNoteWeights: %v", err)
	}
	if !reflect.DeepEqual(w, map[int]float64{48: 1, 60: 2.5}) {
		t.Fatalf("got %v", w)
	}
	if _, err := parseNoteWeights("60=0"); err == nil {
		t.Fatal("expected error for non-positive weight")
	}
	if _, err := parseNoteWeights("60"); err == nil {
		t.Fatal("expected error for missing =")
	}

	if got := resolveNoteWeights([]int{48, 60, 72}, w); !reflect.DeepEqual(got, []float64{1, 2.5, 1}) {
		t.Fatalf("resolveNoteWeights = %v", got)
	}

	for raw, want := range map[string]string{"": aggregateMean, "mean": aggregateMean, "MAX": aggregateMax, "mean-max": aggregateMeanMax} {
		got, err := parseAggregate(raw)
		if err != nil {
			t.Fatalf("parseAggregate(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseAggregate(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := parseAggregate("median"); err == nil {
		t.Fatal("expected error for unknown aggregate")
	}
}

// TestWriteOutputsMultiNoteReportRoundTrip asserts that a multi-note report
// still unmarshals into a reader that only knows the pre-change fields, and
// that best_knobs still round-trips through the resume path.
func TestWriteOutputsMultiNoteReportRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	presetPath := filepath.Join(tmp, "fitted.json")
	reportPath := filepath.Join(tmp, "fitted.report.json")

	defs := []knobDef{
		{Name: "output_gain", Min: 0.01, Max: 5.0},
		{Name: "per_note.48.loss", Min: 0.985, Max: 0.99995, Note: 48, NoteField: noteFieldLoss},
		{Name: "per_note.60.loss", Min: 0.985, Max: 0.99995, Note: 60, NoteField: noteFieldLoss},
	}
	best := candidate{Vals: []float64{1.25, 0.9991, 0.9992}}
	perNote := []noteReport{
		{Note: 48, ReferencePath: "c3.wav", Score: 0.30, Similarity: 0.70},
		{Note: 60, ReferencePath: "c4.wav", Score: 0.50, Similarity: 0.50},
	}

	err := writeOutputs(outputRequest{
		outputPreset:   presetPath,
		reportPath:     reportPath,
		referencePath:  "c3.wav",
		presetPath:     "base.json",
		sampleRate:     48000,
		note:           48,
		velocity:       118,
		releaseAfter:   3.5,
		elapsed:        1.5,
		evals:          42,
		variant:        "desma",
		defs:           defs,
		best:           best,
		bestScore:      0.40,
		bestMetrics:    analysis.Metrics{Score: 0.30, Similarity: 0.70},
		bestParams:     piano.NewDefaultParams(),
		checkpoints:    2,
		notes:          []int{48, 60},
		perNote:        perNote,
		aggregate:      aggregateMean,
		rendersPerEval: 2,
	})
	if err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	// An "old reader" that knows only the pre-change fields.
	var old struct {
		ReferencePath   string             `json:"reference_path"`
		SampleRate      int                `json:"sample_rate"`
		Note            int                `json:"note"`
		Velocity        int                `json:"velocity"`
		Evaluations     int                `json:"evaluations"`
		BestScore       float64            `json:"best_score"`
		BestSimilarity  float64            `json:"best_similarity"`
		BestKnobs       map[string]float64 `json:"best_knobs"`
		CheckpointCount int                `json:"checkpoint_count"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("old reader failed on new report: %v", err)
	}
	if old.Note != 48 || old.SampleRate != 48000 || old.Velocity != 118 || old.Evaluations != 42 {
		t.Fatalf("old scalar fields wrong: %+v", old)
	}
	if old.BestScore != 0.40 {
		t.Fatalf("best_score = %v, want the aggregate 0.40", old.BestScore)
	}
	if old.BestSimilarity != 0.70 {
		t.Fatalf("best_similarity = %v, want notes[0] similarity 0.70", old.BestSimilarity)
	}
	if len(old.BestKnobs) != 3 {
		t.Fatalf("best_knobs = %v, want 3 entries", old.BestKnobs)
	}

	// best_knobs still resumes.
	fallback := candidate{Vals: []float64{1, 0.99, 0.99}}
	resumed, ok, err := loadCandidateFromReport(reportPath, defs, fallback)
	if err != nil || !ok {
		t.Fatalf("resume failed: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(resumed.Vals, best.Vals) {
		t.Fatalf("resumed %v, want %v", resumed.Vals, best.Vals)
	}

	// And the new fields are present for new readers.
	var fresh runReport
	if err := json.Unmarshal(raw, &fresh); err != nil {
		t.Fatalf("new reader failed: %v", err)
	}
	if !reflect.DeepEqual(fresh.Notes, []int{48, 60}) {
		t.Fatalf("notes = %v", fresh.Notes)
	}
	if len(fresh.PerNote) != 2 || fresh.PerNote[1].Note != 60 {
		t.Fatalf("per_note = %+v", fresh.PerNote)
	}
	if fresh.Aggregate != aggregateMean || fresh.RendersPerEval != 2 {
		t.Fatalf("aggregate=%q renders_per_eval=%d", fresh.Aggregate, fresh.RendersPerEval)
	}
	if fresh.Pass != "" || fresh.Polish != nil || fresh.OutputGainMatched != 0 {
		t.Fatalf("unset optional fields leaked: pass=%q polish=%v gain=%v", fresh.Pass, fresh.Polish, fresh.OutputGainMatched)
	}
}

// TestWriteOutputsSingleNoteReportUnchanged asserts a single-note run still
// emits exactly the pre-change key set.
func TestWriteOutputsSingleNoteReportUnchanged(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "r.json")

	defs := []knobDef{{Name: "output_gain", Min: 0.01, Max: 5.0}}
	err := writeOutputs(outputRequest{
		outputPreset:  filepath.Join(tmp, "p.json"),
		reportPath:    reportPath,
		referencePath: "c4.wav",
		presetPath:    "base.json",
		sampleRate:    48000,
		note:          60,
		velocity:      118,
		releaseAfter:  3.5,
		variant:       "desma",
		defs:          defs,
		best:          candidate{Vals: []float64{1.0}},
		bestScore:     0.25,
		bestMetrics:   analysis.Metrics{Score: 0.25, Similarity: 0.75},
		bestParams:    piano.NewDefaultParams(),
	})
	if err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"notes", "per_note", "aggregate", "pass", "pass_window", "renders_per_eval", "polish", "output_gain_matched"} {
		if _, ok := generic[key]; ok {
			t.Fatalf("single-note report gained key %q", key)
		}
	}
}

// TestWriteOutputsSuppressesSingleNoteMultiNoteFields asserts that a run which
// happens to have exactly one target still emits the pre-multi-note report
// shape, even though the caller passes the multi-note fields unconditionally.
func TestWriteOutputsSuppressesSingleNoteMultiNoteFields(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "r.json")

	err := writeOutputs(outputRequest{
		outputPreset:   filepath.Join(tmp, "p.json"),
		reportPath:     reportPath,
		sampleRate:     48000,
		note:           60,
		defs:           []knobDef{{Name: "output_gain", Min: 0.01, Max: 5.0}},
		best:           candidate{Vals: []float64{1.0}},
		bestScore:      0.25,
		bestMetrics:    analysis.Metrics{Score: 0.25},
		bestParams:     piano.NewDefaultParams(),
		notes:          []int{60},
		perNote:        []noteReport{{Note: 60, Score: 0.25}},
		aggregate:      aggregateMean,
		pass:           passNone,
		rendersPerEval: 1,
	})
	if err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"notes", "per_note", "aggregate", "pass", "renders_per_eval"} {
		if _, ok := generic[key]; ok {
			t.Fatalf("single-target report gained key %q", key)
		}
	}
}

// TestWriteOutputsSanitizesNonFiniteMetrics guards against a hard write
// failure at the end of a run: analysis.Metrics can carry NaN (an undefined
// decay slope, for instance) and encoding/json refuses to marshal it.
func TestWriteOutputsSanitizesNonFiniteMetrics(t *testing.T) {
	tmp := t.TempDir()
	reportPath := filepath.Join(tmp, "r.json")

	nan := math.NaN()
	err := writeOutputs(outputRequest{
		outputPreset: filepath.Join(tmp, "p.json"),
		reportPath:   reportPath,
		sampleRate:   48000,
		note:         48,
		defs:         []knobDef{{Name: "output_gain", Min: 0.01, Max: 5.0}},
		best:         candidate{Vals: []float64{math.Inf(1)}},
		bestScore:    0.4,
		bestMetrics:  analysis.Metrics{Score: 0.3, CandDecayDBPerS: nan},
		bestParams:   piano.NewDefaultParams(),
		notes:        []int{48, 60},
		perNote: []noteReport{
			{Note: 48, Score: 0.3, Metrics: analysis.Metrics{CandDecayDBPerS: nan}},
			{Note: 60, Score: 0.5, Metrics: analysis.Metrics{RefDecayDBPerS: math.Inf(-1)}},
		},
		aggregate:      aggregateMean,
		rendersPerEval: 2,
	})
	if err != nil {
		t.Fatalf("writeOutputs must survive non-finite metrics: %v", err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "NaN") || strings.Contains(string(raw), "Inf") {
		t.Fatalf("report still contains a non-finite literal:\n%s", raw)
	}
	var fresh runReport
	if err := json.Unmarshal(raw, &fresh); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if fresh.PerNote[0].Metrics.CandDecayDBPerS != 0 {
		t.Fatalf("NaN was not sanitized: %v", fresh.PerNote[0].Metrics.CandDecayDBPerS)
	}
	if fresh.BestKnobs["output_gain"] != 0 {
		t.Fatalf("infinite knob value was not sanitized: %v", fresh.BestKnobs["output_gain"])
	}
}
