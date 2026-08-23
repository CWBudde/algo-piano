package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTraceCheckpointsMonotone pins that the checkpoint curve reads the
// running minimum: piano-fit minimizes, so a convergence curve must never go
// back uphill even if a trace record arrives out of order.
func TestTraceCheckpointsMonotone(t *testing.T) {
	recs := []traceRecord{
		{Eval: 1, Best: 0.9},
		{Eval: 25, Best: 0.7},
		{Eval: 50, Best: 0.5},
		{Eval: 40, Best: 0.6}, // out of order on purpose
		{Eval: 75, Best: 0.45},
		{Eval: 100, Best: 0.4},
	}
	got := traceCheckpoints(recs, 100, []float64{0.10, 0.25, 0.50, 0.75, 1.00})
	want := []float64{0.9, 0.7, 0.5, 0.45, 0.4}
	if len(got) != len(want) {
		t.Fatalf("traceCheckpoints returned %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Fatalf("traceCheckpoints = %v, want %v", got, want)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i] > got[i-1] {
			t.Fatalf("checkpoint curve went uphill: %v", got)
		}
	}
}

// TestTraceCheckpointsSparse covers a trace that starts after the first
// checkpoint and one that stops early.
func TestTraceCheckpointsSparse(t *testing.T) {
	got := traceCheckpoints([]traceRecord{{Eval: 60, Best: 0.3}}, 100, checkpointFractions)
	if !math.IsNaN(got[0]) || !math.IsNaN(got[1]) || !math.IsNaN(got[2]) {
		t.Errorf("checkpoints before the first record must be NaN, got %v", got)
	}
	if got[3] != 0.3 || got[4] != 0.3 {
		t.Errorf("checkpoints after the last record must hold the incumbent, got %v", got)
	}

	empty := traceCheckpoints(nil, 100, checkpointFractions)
	for _, v := range empty {
		if !math.IsNaN(v) {
			t.Fatalf("an empty trace must yield all-NaN checkpoints, got %v", empty)
		}
	}
	if zero := traceCheckpoints([]traceRecord{{Eval: 1, Best: 1}}, 0, checkpointFractions); !math.IsNaN(zero[0]) {
		t.Errorf("a zero budget must yield NaN checkpoints, got %v", zero)
	}
}

func TestParseArtifactPath(t *testing.T) {
	c, cfg, seed, ok := parseArtifactPath("out/optbench", "out/optbench/attack/halton/seed13/result.json")
	if !ok || c != "attack" || cfg != "halton" || seed != 13 {
		t.Fatalf("parseArtifactPath = (%q, %q, %d, %v)", c, cfg, seed, ok)
	}
	if _, _, _, ok := parseArtifactPath("out/optbench", "out/optbench/attack/result.json"); ok {
		t.Errorf("a short path must be rejected")
	}
	if _, _, _, ok := parseArtifactPath("out/optbench", "out/optbench/attack/halton/run3/result.json"); ok {
		t.Errorf("a non-seed directory must be rejected")
	}
}

// fabricated returns an in-memory dataset so the rendering tests never touch
// the filesystem or build piano-fit.
func fabricated() dataset {
	mk := func(c, cfg string, seed int64, score float64, wall float64, cps ...float64) runRecord {
		return runRecord{Case: c, Config: cfg, Seed: seed, OK: true, BestScore: score, WallSeconds: wall, Checkpoints: cps}
	}

	return dataset{
		MaxEvals:  1500,
		Jobs:      4,
		Workers:   1,
		BuildTags: "asm",
		Seeds:     []int64{1, 2, 3},
		Reference: "reference/c4.wav",
		Preset:    "assets/presets/default.json",
		Records: []runRecord{
			// baseline wins this case.
			mk("sustain", "baseline", 1, 0.40, 10, 0.9, 0.8, 0.6, 0.45, 0.40),
			mk("sustain", "baseline", 2, 0.42, 12, 0.9, 0.8, 0.7, 0.50, 0.42),
			mk("sustain", "baseline", 3, 0.44, 11, 0.9, 0.8, 0.7, 0.50, 0.44),
			mk("sustain", "halton", 1, 0.50, 9, 0.9, 0.85, 0.7, 0.55, 0.50),
			mk("sustain", "halton", 2, 0.52, 9, 0.9, 0.85, 0.7, 0.55, 0.52),
			{Case: "sustain", Config: "halton", Seed: 3, OK: false, BestScore: math.NaN(), WallSeconds: 3},
			// halton wins this case.
			mk("attack", "baseline", 1, 0.70, 20, 0.99, 0.9, 0.8, 0.75, 0.70),
			mk("attack", "baseline", 2, 0.72, 21, 0.99, 0.9, 0.8, 0.75, 0.72),
			mk("attack", "halton", 1, 0.60, 19, 0.99, 0.9, 0.7, 0.65, 0.60),
			mk("attack", "halton", 2, 0.62, 19, 0.99, 0.9, 0.7, 0.65, 0.62),
		},
	}
}

// TestBuildDocAggregates checks the medians, the head-to-head delta and the
// failure count against a hand-computed fabricated dataset.
func TestBuildDocAggregates(t *testing.T) {
	doc := buildDoc(fabricated(), "just opt-bench-report")

	if len(doc.Cases) != 2 || doc.Cases[0].Case != "sustain" || doc.Cases[1].Case != "attack" {
		t.Fatalf("cases must follow matrix order, got %+v", doc.Cases)
	}

	sustain := doc.Cases[0]
	base := findConfig(t, sustain, "baseline")
	if base.MedianBest != 0.42 || base.MinBest != 0.40 || base.MaxBest != 0.44 {
		t.Errorf("baseline stats = %+v, want median 0.42 min 0.40 max 0.44", base)
	}
	if base.Seeds != 3 || base.Failures != 0 {
		t.Errorf("baseline seeds/failures = %d/%d, want 3/0", base.Seeds, base.Failures)
	}
	if base.MedianWall != 11 {
		t.Errorf("baseline median wall = %v, want 11", base.MedianWall)
	}

	halton := findConfig(t, sustain, "halton")
	if halton.Seeds != 2 || halton.Failures != 1 {
		t.Errorf("halton seeds/failures = %d/%d, want 2/1", halton.Seeds, halton.Failures)
	}
	if halton.MedianBest != 0.51 {
		t.Errorf("halton median = %v, want 0.51", halton.MedianBest)
	}
	if math.Abs(base.DeltaVsControl-(0.42-0.51)) > 1e-12 {
		t.Errorf("baseline delta vs halton = %v, want %v", base.DeltaVsControl, 0.42-0.51)
	}
	if !strings.Contains(sustain.Verdict, "mayfly beats halton") {
		t.Errorf("sustain verdict = %q, want a win for mayfly", sustain.Verdict)
	}

	attack := doc.Cases[1]
	if !strings.Contains(attack.Verdict, "mayfly loses to halton") {
		t.Errorf("attack verdict = %q, want a loss for mayfly", attack.Verdict)
	}

	// Median over seeds of the incumbent at 50% of the budget.
	if got := base.MedianCheckpoints[2]; math.Abs(got-0.7) > 1e-12 {
		t.Errorf("baseline 50%% checkpoint = %v, want 0.7", got)
	}
}

// TestVerdictLineDirection guards the direction of the comparison. Getting it
// backwards would invert every conclusion in the document.
func TestVerdictLineDirection(t *testing.T) {
	if got := verdictLine(0.4, 0.5); !strings.Contains(got, "beats") {
		t.Errorf("a LOWER baseline score must be a win: %q", got)
	}
	if got := verdictLine(0.6, 0.5); !strings.Contains(got, "loses") {
		t.Errorf("a HIGHER baseline score must be a loss: %q", got)
	}
	if got := verdictLine(0.5, 0.5); !strings.Contains(got, "ties") {
		t.Errorf("equal medians must tie: %q", got)
	}
	if got := verdictLine(math.NaN(), 0.5); !strings.Contains(got, "not enough data") {
		t.Errorf("a missing median must not produce a verdict: %q", got)
	}
}

// TestRenderMarkdown checks the document contains its generated header, both
// tables and a correct Method section.
func TestRenderMarkdown(t *testing.T) {
	doc := buildDoc(fabricated(), "just opt-bench-report")
	md := renderMarkdown(doc)

	for _, want := range []string{
		"GENERATED FILE - DO NOT EDIT BY HAND",
		"just opt-bench-report",
		"**Lower score is better.**",
		"## Case `sustain`",
		"### Convergence (`sustain`)",
		"| `baseline` | 0.420000 | 0.400000 | 0.440000 |",
		"-0.090000",
		"mayfly beats halton by 0.090000",
		"mayfly loses to halton by 0.100000",
		"## Method",
		"`--max-evals 1500`",
		"`--time-budget 86400` pinned wide open",
		"`--workers 1`",
		"-buildvcs=false",
		"-tags asm",
		"reference/c4.wav",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered document is missing %q", want)
		}
	}

	if strings.Contains(md, "NaN") {
		t.Errorf("missing observations must render as an em dash, not NaN:\n%s", md)
	}
}

func findConfig(t *testing.T, cr caseReport, name string) configStats {
	t.Helper()
	for _, cs := range cr.Configs {
		if cs.Config == name {
			return cs
		}
	}
	t.Fatalf("case %q has no config %q", cr.Case, name)

	return configStats{}
}

// TestLoadDatasetSkipsIncompleteRuns pins the guard that keeps a truncated
// search out of a fixed-budget table. piano-fit writes a parseable result.json
// at its first evaluation, so "the file exists" is not evidence that the run
// spent its budget — without this gate, a killed run would contribute the score
// it happened to hold when it died.
func TestLoadDatasetSkipsIncompleteRuns(t *testing.T) {
	outDir := t.TempDir()
	write := func(c, cfg string, seed int, score float64, complete bool) {
		dir := filepath.Join(outDir, c, cfg, fmt.Sprintf("seed%d", seed))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := fmt.Sprintf(`{"best_score":%v,"evaluations":1500,"elapsed_seconds":12}`, score)
		if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write result: %v", err)
		}
		if complete {
			if err := os.WriteFile(filepath.Join(dir, completionMarker), []byte("ok\n"), 0o600); err != nil {
				t.Fatalf("write marker: %v", err)
			}
		}
	}
	write("sustain", "baseline", 1, 0.25, true)
	write("sustain", "baseline", 2, 0.99, false)

	ds, err := loadDataset(outDir, dataset{OutDir: outDir, MaxEvals: 1500})
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	if len(ds.Records) != 1 {
		t.Fatalf("got %d records, want only the complete one: %+v", len(ds.Records), ds.Records)
	}
	if ds.Records[0].Seed != 1 {
		t.Fatalf("kept seed %d, want the completed seed 1", ds.Records[0].Seed)
	}
}

// TestLoadDatasetAcceptsSummaryAsCompletion covers artifact trees written
// before the marker existed: an OK row in summary.json is the same proof.
func TestLoadDatasetAcceptsSummaryAsCompletion(t *testing.T) {
	outDir := t.TempDir()
	dir := filepath.Join(outDir, "sustain", "baseline", "seed1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"),
		[]byte(`{"best_score":0.25,"evaluations":1500,"elapsed_seconds":12}`), 0o600); err != nil {
		t.Fatalf("write result: %v", err)
	}
	summary := fmt.Sprintf(
		`{"max_evals":1500,"runs":[{"case":"sustain","config":"baseline","seed":1,"dir":%q,"ok":true,"best_score":0.25}]}`,
		dir,
	)
	if err := os.WriteFile(filepath.Join(outDir, "summary.json"), []byte(summary), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	ds, err := loadDataset(outDir, dataset{OutDir: outDir, MaxEvals: 1500})
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	if len(ds.Records) != 1 || !ds.Records[0].OK {
		t.Fatalf("summary-attested run must count as an observation: %+v", ds.Records)
	}
}

// TestTraceSpreadCountsEveryRecord pins that the spread describes the whole
// landscape. An earlier version trimmed values above 0.75, believing them to be
// the objective's "current best + 0.8" penalty answers — but no penalty path
// reaches trace.record, and 1.0 is a legitimate worst-case score because the
// objective sums clamped components. Trimming removed exactly the wide tail
// that distinguishes a flat objective from a concentrated swarm.
func TestTraceSpreadCountsEveryRecord(t *testing.T) {
	recs := make([]traceRecord, 0, 120)
	for i := range 100 {
		recs = append(recs, traceRecord{Eval: int64(i + 1), Aggregate: 0.40 + float64(i)*0.001})
	}
	for i := range 20 {
		recs = append(recs, traceRecord{Eval: int64(101 + i), Aggregate: 1.0})
	}

	got := traceSpread(recs)
	if !got.Valid {
		t.Fatal("spread must be valid for a non-empty trace")
	}
	if got.P95 < 1.0 {
		t.Errorf("p95 = %v, want the genuine worst-case tail of 1.0 retained", got.P95)
	}
	if got.IQR < 0.02 {
		t.Errorf("IQR = %v, want the full spread rather than the lower tail alone", got.IQR)
	}
}

// TestTraceSpreadHandlesAFlatLandscape is the attack case in miniature: a run
// whose proposals all sit inside a narrow band must report a correspondingly
// tiny IQR, because that number is what the audit reads as swarm
// concentration.
func TestTraceSpreadHandlesAFlatLandscape(t *testing.T) {
	recs := make([]traceRecord, 0, 100)
	for i := range 100 {
		recs = append(recs, traceRecord{Eval: int64(i + 1), Aggregate: 0.4430 + float64(i%5)*0.0002})
	}

	got := traceSpread(recs)
	if !got.Valid || got.IQR > 0.001 {
		t.Fatalf("spread %+v must report a tiny IQR for a flat landscape", got)
	}
}

// TestLoadDatasetSkipsShortRuns pins the guard against a search that exited
// cleanly without running. piano-fit writes a complete-looking report after its
// single seed evaluation when the sampler cannot produce points at all, so a
// zero exit code and a parseable report are together still not evidence that a
// cell spent its budget.
func TestLoadDatasetSkipsShortRuns(t *testing.T) {
	outDir := t.TempDir()
	write := func(cfg string, evals int) {
		dir := filepath.Join(outDir, "joint-ir", cfg, "seed1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := fmt.Sprintf(`{"best_score":0.5,"evaluations":%d,"elapsed_seconds":30}`, evals)
		if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write result: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, completionMarker), []byte("ok\n"), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
	}
	write("baseline", 600)
	write("halton", 1)

	ds, err := loadDataset(outDir, dataset{OutDir: outDir, MaxEvals: 600})
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	if len(ds.Records) != 1 || ds.Records[0].Config != "baseline" {
		t.Fatalf("got %+v, want only the run that spent its budget", ds.Records)
	}
}

// TestLoadDatasetIgnoresSummaryRowsWithoutArtifacts pins the provenance rule: a
// summary.json row attests that a run finished, but only the artifacts make it
// an observation. Without this, deleting a bad cell's directory would leave its
// score in the tables, sourced from a summary that was written before the cell
// was known to be invalid.
func TestLoadDatasetIgnoresSummaryRowsWithoutArtifacts(t *testing.T) {
	outDir := t.TempDir()
	dir := filepath.Join(outDir, "joint-ir", "halton", "seed1")
	summary := fmt.Sprintf(
		`{"max_evals":600,"runs":[{"case":"joint-ir","config":"halton","seed":1,"dir":%q,"ok":true,"best_score":0.69}]}`,
		dir,
	)
	if err := os.WriteFile(filepath.Join(outDir, "summary.json"), []byte(summary), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	ds, err := loadDataset(outDir, dataset{OutDir: outDir, MaxEvals: 600})
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	for _, rec := range ds.Records {
		if rec.OK {
			t.Fatalf("a summary row with no artifacts must not become an observation: %+v", rec)
		}
	}
}

// TestEvalsMatchBudget pins both directions. Too few evaluations means the
// search did not run; too many means the artifact belongs to a different,
// larger matrix, which is what makes reusing an output directory at a new
// --max-evals dangerous rather than merely wasteful.
func TestEvalsMatchBudget(t *testing.T) {
	cases := []struct {
		name   string
		evals  int
		budget int
		want   bool
	}{
		{"exact", 600, 600, true},
		{"a hair early", 560, 600, true},
		{"far short", 1, 600, false},
		{"cached from a larger matrix", 2400, 600, false},
		{"unknown budget", 2400, 0, true},
		{"unknown evals", 0, 600, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalsMatchBudget(tc.evals, tc.budget); got != tc.want {
				t.Errorf("evalsMatchBudget(%d, %d) = %v, want %v", tc.evals, tc.budget, got, tc.want)
			}
		})
	}
}

// TestLoadDatasetSkipsOversizedRuns is the cached-cell hazard seen from the
// report side: a 2400-evaluation cell must not be tabulated as if it had run
// at 600.
func TestLoadDatasetSkipsOversizedRuns(t *testing.T) {
	outDir := t.TempDir()
	dir := filepath.Join(outDir, "sustain", "baseline", "seed1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"),
		[]byte(`{"best_score":0.30,"evaluations":2400,"elapsed_seconds":1300}`), 0o600); err != nil {
		t.Fatalf("write result: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, completionMarker), []byte("ok\n"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	ds, err := loadDataset(outDir, dataset{OutDir: outDir, MaxEvals: 600})
	if err != nil {
		t.Fatalf("loadDataset: %v", err)
	}
	for _, rec := range ds.Records {
		if rec.OK {
			t.Fatalf("a 2400-eval cell must not count as a 600-eval observation: %+v", rec)
		}
	}
}
