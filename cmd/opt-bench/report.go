package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// checkpointFractions are the fractions of the eval budget the convergence
// table reports the incumbent at.
var checkpointFractions = []float64{0.10, 0.25, 0.50, 0.75, 1.00}

// traceRecord mirrors one line of piano-fit's --trace JSONL
// (cmd/piano-fit/trace.go). Best is the incumbent at that moment, which is the
// only field a convergence curve needs; Aggregate is what the sampler
// proposed.
type traceRecord struct {
	Eval      int64   `json:"eval"`
	TimeSec   float64 `json:"t_sec"`
	Worker    int     `json:"worker"`
	Aggregate float64 `json:"aggregate"`
	Best      float64 `json:"best"`
}

// runRecord is one (case, config, seed) observation as the reporter sees it.
// It is deliberately reconstructible from the artifacts on disk alone, so
// --report never has to rebuild or rerun anything.
type runRecord struct {
	Case        string
	Config      string
	Seed        int64
	Dir         string
	OK          bool
	BestScore   float64
	WallSeconds float64
	// Checkpoints holds the incumbent at each checkpointFractions entry, NaN
	// where the trace has no record that early (or no trace at all).
	Checkpoints []float64
	// Spread describes the distribution of what this run actually proposed,
	// as opposed to what it kept. See spreadStats.
	Spread spreadStats
}

// spreadStats summarises the objective values a run proposed across its whole
// trace, which is the diagnostic that separates two very different failures.
//
// A wide spread under the Halton control says the objective has dynamic range
// for a search to exploit; a narrow one says the knobs barely move the score,
// and no optimizer can beat a space-filling sequence on a landscape that flat.
// A narrow IQR under the real search says the swarm has concentrated — useful
// when it concentrates somewhere good, premature convergence when it does not.
// Neither is visible in the best-score tables, which report only the single
// value each run kept.
type spreadStats struct {
	Valid  bool
	Min    float64
	P05    float64
	Median float64
	P95    float64
	IQR    float64
}

// dataset is every observation plus the run parameters the Method section
// needs. Params come from summary.json when it exists and from the driver
// flags otherwise.
type dataset struct {
	Records   []runRecord
	OutDir    string
	MaxEvals  int
	Jobs      int
	Workers   int
	BuildTags string
	Command   string
	Seeds     []int64
	Reference string
	Preset    string
}

// median returns the median of xs, ignoring NaN. An empty (or all-NaN) input
// yields NaN.
//
// Medians, not means: each seed is an independent invocation of a stochastic
// search, and the distribution of best-scores across seeds is skewed by the
// occasional run that never escapes its starting basin. A mean over five seeds
// would let one such run decide the verdict.
func median(xs []float64) float64 {
	clean := make([]float64, 0, len(xs))
	for _, x := range xs {
		if !math.IsNaN(x) {
			clean = append(clean, x)
		}
	}
	if len(clean) == 0 {
		return math.NaN()
	}
	sort.Float64s(clean)
	n := len(clean)
	if n%2 == 1 {
		return clean[n/2]
	}

	return (clean[n/2-1] + clean[n/2]) / 2
}

// minMax returns the extremes of xs ignoring NaN, or (NaN, NaN) when empty.
func minMax(xs []float64) (float64, float64) {
	lo, hi := math.NaN(), math.NaN()
	for _, x := range xs {
		if math.IsNaN(x) {
			continue
		}
		if math.IsNaN(lo) || x < lo {
			lo = x
		}
		if math.IsNaN(hi) || x > hi {
			hi = x
		}
	}

	return lo, hi
}

// traceCheckpoints reduces one trace to the incumbent at each fraction of the
// eval budget.
//
// The incumbent is read as the running minimum of Best rather than the last
// record's Best: piano-fit is a MINIMIZER (optimize.go accepts a candidate on
// `aggregate < bestEval.aggregate`), records arrive from every worker on a
// lossy queue, and a dropped or out-of-order record must not make the curve
// appear to go back uphill.
func traceCheckpoints(recs []traceRecord, budget int, fracs []float64) []float64 {
	out := make([]float64, len(fracs))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(recs) == 0 || budget < 1 {
		return out
	}

	sorted := make([]traceRecord, len(recs))
	copy(sorted, recs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Eval < sorted[j].Eval })

	// Walk the records once, emitting a checkpoint as each target eval is
	// passed. Both slices are ascending, so one pass suffices.
	best := math.NaN()
	next := 0
	for _, rec := range sorted {
		for next < len(fracs) {
			target := int64(math.Round(fracs[next] * float64(budget)))
			if rec.Eval <= target {
				break
			}
			out[next] = best
			next++
		}
		if next >= len(fracs) {
			break
		}
		if math.IsNaN(best) || rec.Best < best {
			best = rec.Best
		}
	}
	for ; next < len(fracs); next++ {
		out[next] = best
	}

	return out
}

// readTrace parses a JSONL trace. A missing file is not an error: tracing is
// opt-in on the piano-fit side and an older artifact tree may predate it.
// penaltyFloor is the smallest aggregate the objective's own penalty paths can
// return. piano-fit answers an over-budget or failed evaluation with
// "current best + 0.8" rather than a real score, so those records describe the
// budget, not the landscape, and would inflate every spread statistic.
const penaltyFloor = 0.75

// traceSpread summarises the proposed objective values in a trace.
func traceSpread(recs []traceRecord) spreadStats {
	vals := make([]float64, 0, len(recs))
	for _, r := range recs {
		if math.IsNaN(r.Aggregate) || math.IsInf(r.Aggregate, 0) {
			continue
		}
		vals = append(vals, r.Aggregate)
	}
	if len(vals) == 0 {
		return spreadStats{}
	}
	sort.Float64s(vals)
	// Trim the penalty tail only when it is a tail: on a case whose real
	// scores live above the floor, dropping everything above it would discard
	// the landscape itself.
	if trimmed := trimPenaltyTail(vals); len(trimmed) > len(vals)/2 {
		vals = trimmed
	}
	q := func(f float64) float64 { return vals[int(f*float64(len(vals)-1))] }

	return spreadStats{
		Valid: true, Min: vals[0], P05: q(0.05), Median: q(0.5), P95: q(0.95),
		IQR: q(0.75) - q(0.25),
	}
}

// trimPenaltyTail drops the sorted values at or above the penalty floor.
func trimPenaltyTail(sorted []float64) []float64 {
	cut := sort.SearchFloat64s(sorted, penaltyFloor)

	return sorted[:cut]
}

func readTrace(path string) ([]traceRecord, error) {
	file, err := os.Open(path) //nolint:gosec // path is driver-constructed
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer func() { _ = file.Close() }()

	var recs []traceRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec traceRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// A truncated final line is expected when a run was interrupted;
			// keeping what parsed is better than discarding the whole trace.
			continue
		}
		recs = append(recs, rec)
	}
	if err := scanner.Err(); err != nil {
		return recs, fmt.Errorf("read trace %s: %w", path, err)
	}

	return recs, nil
}

// readSummary loads summary.json when it exists.
func readSummary(path string) (*matrixSummary, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is driver-constructed
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("read summary: %w", err)
	}
	var sum matrixSummary
	if err := json.Unmarshal(raw, &sum); err != nil {
		return nil, fmt.Errorf("parse summary %s: %w", path, err)
	}

	return &sum, nil
}

// runIsComplete reports whether a run finished its whole evaluation budget.
func runIsComplete(dir string, rec *runRecord, known bool) bool {
	if known && rec.OK {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, completionMarker))

	return err == nil
}

// loadDataset reconstructs the dataset from the artifact tree.
//
// summary.json supplies the failed runs (a failure leaves no result.json, so
// the walk alone cannot see it) and the run parameters. The walk supplies
// everything else, which is what lets --report work on a tree copied from
// another machine, or on one whose summary.json was never written.
func loadDataset(outDir string, fallback dataset) (dataset, error) {
	ds := fallback
	byKey := map[string]*runRecord{}
	key := func(c, cfg string, seed int64) string {
		return c + "\x00" + cfg + "\x00" + strconv.FormatInt(seed, 10)
	}

	sum, err := readSummary(filepath.Join(outDir, "summary.json"))
	if err != nil {
		return ds, err
	}
	if sum != nil {
		applySummary(&ds, sum)
		for _, row := range sum.Runs {
			rec := &runRecord{
				Case: row.Case, Config: row.Config, Seed: row.Seed, Dir: row.Dir,
				OK: row.OK, BestScore: row.BestScore, WallSeconds: row.WallSeconds,
			}
			if !row.OK {
				rec.BestScore = math.NaN()
			}
			byKey[key(row.Case, row.Config, row.Seed)] = rec
		}
	}

	walkErr := filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "result.json" {
			return nil //nolint:nilerr // an unreadable subtree must not abort the report
		}
		c, cfg, seed, ok := parseArtifactPath(outDir, path)
		if !ok {
			return nil
		}
		rep, err := readFitReport(path)
		if err != nil {
			return nil //nolint:nilerr // a corrupt report is reported as a missing observation
		}
		rec, exists := byKey[key(c, cfg, seed)]
		// A run that was killed or crashed part-way still leaves a parseable
		// result.json — piano-fit writes one at its first evaluation — so
		// completion has to be established from something else, or a truncated
		// search would enter a fixed-budget table as if it had spent the whole
		// budget. Either the driver's completion marker or an OK row in
		// summary.json is proof; requiring the marker alone would reject trees
		// produced before the marker existed.
		if !runIsComplete(filepath.Dir(path), rec, exists) {
			return nil
		}
		if !exists {
			rec = &runRecord{Case: c, Config: cfg, Seed: seed, Dir: filepath.Dir(path)}
			byKey[key(c, cfg, seed)] = rec
		}
		rec.OK = true
		rec.BestScore = rep.BestScore
		if rec.WallSeconds == 0 {
			rec.WallSeconds = rep.ElapsedSeconds
		}

		return nil
	})
	if walkErr != nil {
		return ds, fmt.Errorf("walk %s: %w", outDir, walkErr)
	}

	for _, rec := range byKey {
		if rec.OK {
			recs, _ := readTrace(filepath.Join(rec.Dir, "trace.jsonl"))
			rec.Checkpoints = traceCheckpoints(recs, ds.MaxEvals, checkpointFractions)
			rec.Spread = traceSpread(recs)
		}
		ds.Records = append(ds.Records, *rec)
	}
	// Without a summary.json the intended seed set is unknown, so the Method
	// section must describe what is actually on disk rather than the driver's
	// current --seeds default.
	if sum == nil {
		ds.Seeds = observedSeeds(ds.Records)
	}
	sort.SliceStable(ds.Records, func(i, j int) bool {
		a, b := ds.Records[i], ds.Records[j]
		if a.Case != b.Case {
			return a.Case < b.Case
		}
		if a.Config != b.Config {
			return a.Config < b.Config
		}

		return a.Seed < b.Seed
	})

	return ds, nil
}

// observedSeeds returns the distinct seeds present in the records, ascending.
func observedSeeds(recs []runRecord) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, rec := range recs {
		if !seen[rec.Seed] {
			seen[rec.Seed] = true
			out = append(out, rec.Seed)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out
}

// applySummary copies the run parameters recorded at matrix time over the
// driver-flag fallbacks.
func applySummary(ds *dataset, sum *matrixSummary) {
	if sum.MaxEvals > 0 {
		ds.MaxEvals = sum.MaxEvals
	}
	if sum.Jobs > 0 {
		ds.Jobs = sum.Jobs
	}
	if sum.Workers > 0 {
		ds.Workers = sum.Workers
	}
	if sum.BuildTags != "" {
		ds.BuildTags = sum.BuildTags
	}
	if len(sum.Seeds) > 0 {
		ds.Seeds = sum.Seeds
	}
	if sum.Reference != "" {
		ds.Reference = sum.Reference
	}
	if sum.Preset != "" {
		ds.Preset = sum.Preset
	}
}

// parseArtifactPath recovers (case, config, seed) from
// <outDir>/<case>/<config>/seed<N>/result.json.
func parseArtifactPath(outDir, path string) (string, string, int64, bool) {
	rel, err := filepath.Rel(outDir, path)
	if err != nil {
		return "", "", 0, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 {
		return "", "", 0, false
	}
	seedPart := parts[2]
	if !strings.HasPrefix(seedPart, "seed") {
		return "", "", 0, false
	}
	seed, err := strconv.ParseInt(strings.TrimPrefix(seedPart, "seed"), 10, 64)
	if err != nil {
		return "", "", 0, false
	}

	return parts[0], parts[1], seed, true
}
