package main

import (
	"fmt"
	"math"
	"strings"
)

// baselineConfig and controlConfig name the two conditions the verdict line
// compares. baseline is piano-fit exactly as it ships; halton is the strongest
// of the two trivial controls (a low-discrepancy sequence covers the box more
// evenly than uniform sampling at the same budget), so it is the one the
// metaheuristic has to beat to justify itself.
const (
	baselineConfig = "baseline"
	controlConfig  = "halton"
)

// configStats aggregates every seed of one (case, config) cell.
type configStats struct {
	Config     string
	Seeds      int
	Failures   int
	MedianBest float64
	MinBest    float64
	MaxBest    float64
	MedianWall float64
	// DeltaVsControl is MedianBest - median(halton). Lower score is better, so
	// a negative delta means this config won.
	DeltaVsControl float64
	// MedianCheckpoints is the median-over-seeds incumbent at each
	// checkpointFractions entry.
	MedianCheckpoints []float64
	// Spread* are medians over seeds of the per-run trace spread. They report
	// what the search proposed rather than what it kept, which is what
	// separates "the search is weak" from "the objective is flat".
	SpreadMin    float64
	SpreadP05    float64
	SpreadMedian float64
	SpreadP95    float64
	SpreadIQR    float64
}

// caseReport is one case's block of the document.
type caseReport struct {
	Case    string
	Desc    string
	Configs []configStats
	Verdict string
}

// benchDoc is everything the markdown renderer needs.
type benchDoc struct {
	Command   string
	OutDir    string
	MaxEvals  int
	Seeds     []int64
	Jobs      int
	Workers   int
	BuildTags string
	Reference string
	Preset    string
	Cases     []caseReport
}

// buildDoc aggregates a dataset into the document model. It is pure: the tests
// drive it from a fabricated in-memory dataset.
func buildDoc(ds dataset, command string) benchDoc {
	caseNames := map[string]bool{}
	for _, rec := range ds.Records {
		caseNames[rec.Case] = true
	}

	descs := map[string]string{}
	for _, c := range defaultCases() {
		descs[c.Name] = c.Desc
	}

	doc := benchDoc{
		Command:   command,
		OutDir:    ds.OutDir,
		MaxEvals:  ds.MaxEvals,
		Seeds:     ds.Seeds,
		Jobs:      ds.Jobs,
		Workers:   ds.Workers,
		BuildTags: ds.BuildTags,
		Reference: ds.Reference,
		Preset:    ds.Preset,
	}
	for _, name := range caseOrder(caseNames) {
		doc.Cases = append(doc.Cases, buildCaseReport(ds, name, descs[name]))
	}

	return doc
}

// buildCaseReport aggregates one case.
func buildCaseReport(ds dataset, caseName, desc string) caseReport {
	cfgNames := map[string]bool{}
	byConfig := map[string][]runRecord{}
	for _, rec := range ds.Records {
		if rec.Case != caseName {
			continue
		}
		cfgNames[rec.Config] = true
		byConfig[rec.Config] = append(byConfig[rec.Config], rec)
	}

	rep := caseReport{Case: caseName, Desc: desc}
	for _, name := range configOrder(cfgNames) {
		rep.Configs = append(rep.Configs, aggregateConfig(name, byConfig[name]))
	}

	control := math.NaN()
	baseline := math.NaN()
	for _, cs := range rep.Configs {
		switch cs.Config {
		case controlConfig:
			control = cs.MedianBest
		case baselineConfig:
			baseline = cs.MedianBest
		}
	}
	for i := range rep.Configs {
		rep.Configs[i].DeltaVsControl = rep.Configs[i].MedianBest - control
	}
	rep.Verdict = verdictLine(baseline, control)

	return rep
}

// aggregateConfig reduces the seeds of one cell.
func aggregateConfig(name string, recs []runRecord) configStats {
	cs := configStats{Config: name, MedianBest: math.NaN(), MinBest: math.NaN(), MaxBest: math.NaN(), MedianWall: math.NaN()}
	scores := make([]float64, 0, len(recs))
	walls := make([]float64, 0, len(recs))
	var sMin, sP05, sMed, sP95, sIQR []float64
	perCheckpoint := make([][]float64, len(checkpointFractions))
	for _, rec := range recs {
		if !rec.OK {
			cs.Failures++

			continue
		}
		cs.Seeds++
		scores = append(scores, rec.BestScore)
		walls = append(walls, rec.WallSeconds)
		for i := range perCheckpoint {
			if i < len(rec.Checkpoints) {
				perCheckpoint[i] = append(perCheckpoint[i], rec.Checkpoints[i])
			}
		}
		if rec.Spread.Valid {
			sMin = append(sMin, rec.Spread.Min)
			sP05 = append(sP05, rec.Spread.P05)
			sMed = append(sMed, rec.Spread.Median)
			sP95 = append(sP95, rec.Spread.P95)
			sIQR = append(sIQR, rec.Spread.IQR)
		}
	}
	cs.SpreadMin, cs.SpreadP05 = median(sMin), median(sP05)
	cs.SpreadMedian, cs.SpreadP95, cs.SpreadIQR = median(sMed), median(sP95), median(sIQR)
	cs.MedianBest = median(scores)
	cs.MinBest, cs.MaxBest = minMax(scores)
	cs.MedianWall = median(walls)
	cs.MedianCheckpoints = make([]float64, len(perCheckpoint))
	for i, xs := range perCheckpoint {
		cs.MedianCheckpoints[i] = median(xs)
	}

	return cs
}

// verdictLine renders the plain-language head-to-head sentence. Lower score is
// better, so baseline < control means the metaheuristic won.
func verdictLine(baseline, control float64) string {
	if math.IsNaN(baseline) || math.IsNaN(control) {
		return "**Verdict:** not enough data (need both `baseline` and `halton` seeds to compare)."
	}
	delta := baseline - control
	switch {
	case delta < 0:
		return fmt.Sprintf("**Verdict:** mayfly beats halton by %.6f (lower is better).", -delta)
	case delta > 0:
		return fmt.Sprintf("**Verdict:** mayfly loses to halton by %.6f (lower is better).", delta)
	default:
		return "**Verdict:** mayfly ties halton exactly."
	}
}

// fmtScore renders a score cell, or an em dash when there is no observation.
func fmtScore(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}

	return fmt.Sprintf("%.6f", v)
}

// fmtDelta renders a signed delta cell.
func fmtDelta(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}

	return fmt.Sprintf("%+.6f", v)
}

// fmtSeconds renders a wall-clock cell.
func fmtSeconds(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}

	return fmt.Sprintf("%.1f", v)
}

// renderMarkdown turns the document model into docs/optimizer-benchmark.md.
func renderMarkdown(doc benchDoc) string {
	var b strings.Builder

	// The budget belongs in the title: the audit produces more than one of
	// these documents, and two tables that differ only in evaluation budget are
	// not comparable to each other.
	if doc.MaxEvals > 0 {
		fmt.Fprintf(&b, "# Optimizer benchmark (%d evaluations)\n\n", doc.MaxEvals)
	} else {
		b.WriteString("# Optimizer benchmark\n\n")
	}
	b.WriteString("<!-- GENERATED FILE - DO NOT EDIT BY HAND. -->\n")
	fmt.Fprintf(&b, "<!-- Regenerate with: %s -->\n\n", doc.Command)
	outDir := doc.OutDir
	if outDir == "" {
		outDir = "out/optbench"
	}
	fmt.Fprintf(&b, "> **Generated.** This document is regenerated from the artifacts under `%s/`.\n", outDir)
	fmt.Fprintf(&b, "> Do not edit it by hand; run `%s` instead.\n\n", doc.Command)

	b.WriteString("**Lower score is better.** `best_score` is the objective `cmd/piano-fit` *minimizes* ")
	b.WriteString("(`cmd/piano-fit/optimize.go` accepts a candidate on `evalRes.aggregate < state.bestEval.aggregate`), ")
	b.WriteString("so a negative delta in the tables below means the row won.\n\n")

	renderCases(&b, doc)
	renderMethod(&b, doc)

	return b.String()
}

// renderCases writes the per-case result and convergence tables.
func renderCases(b *strings.Builder, doc benchDoc) {
	for _, cr := range doc.Cases {
		fmt.Fprintf(b, "## Case `%s`\n\n", cr.Case)
		if cr.Desc != "" {
			fmt.Fprintf(b, "%s\n\n", cr.Desc)
		}

		b.WriteString("| Config | median best | min | max | median wall (s) | n seeds | failures | Δ vs `halton` |\n")
		b.WriteString("| ------ | ----------- | --- | --- | --------------- | ------- | -------- | ------------- |\n")
		for _, cs := range cr.Configs {
			fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s | %d | %d | %s |\n",
				cs.Config, fmtScore(cs.MedianBest), fmtScore(cs.MinBest), fmtScore(cs.MaxBest),
				fmtSeconds(cs.MedianWall), cs.Seeds, cs.Failures, fmtDelta(cs.DeltaVsControl))
		}
		fmt.Fprintf(b, "\n%s\n\n", cr.Verdict)

		renderConvergence(b, cr)
		renderSpread(b, cr)
	}
}

// renderSpread writes the distribution of proposed objective values.
//
// It answers a question the best-score table cannot. Compare the `halton` row,
// which probes the box evenly and therefore measures the objective's own
// dynamic range, against the `baseline` row, which measures where the swarm
// ended up. A flat `halton` row means the knobs barely move the score, and no
// optimizer can beat a space-filling sequence on a landscape that flat. A
// `baseline` IQR far below halton's means the swarm concentrated — which is
// the goal when it concentrates on a good region and premature convergence
// when it does not.
func renderSpread(b *strings.Builder, cr caseReport) {
	fmt.Fprintf(b, "### Proposed-score distribution (`%s`)\n\n", cr.Case)
	b.WriteString("Median over seeds of each run's own trace quantiles, over every score the run proposed " +
		"(not just the one it kept). Every trace record counts: the objective sums clamped components, " +
		"so 1.0 is a genuine worst case rather than a sentinel.\n\n")
	b.WriteString("| Config | min | p05 | median | p95 | IQR |\n")
	b.WriteString("| ------ | --- | --- | ------ | --- | --- |\n")
	for _, cs := range cr.Configs {
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s | %s |\n",
			cs.Config, fmtScore(cs.SpreadMin), fmtScore(cs.SpreadP05), fmtScore(cs.SpreadMedian),
			fmtScore(cs.SpreadP95), fmtScore(cs.SpreadIQR))
	}
	b.WriteString("\n")
}

// renderConvergence writes the median-over-seeds incumbent at each checkpoint.
func renderConvergence(b *strings.Builder, cr caseReport) {
	fmt.Fprintf(b, "### Convergence (`%s`)\n\n", cr.Case)
	b.WriteString("Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.\n\n")

	b.WriteString("| Config |")
	for _, f := range checkpointFractions {
		fmt.Fprintf(b, " %d%% |", int(math.Round(f*100)))
	}
	b.WriteString("\n| ------ |")
	for range checkpointFractions {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, cs := range cr.Configs {
		fmt.Fprintf(b, "| `%s` |", cs.Config)
		for i := range checkpointFractions {
			v := math.NaN()
			if i < len(cs.MedianCheckpoints) {
				v = cs.MedianCheckpoints[i]
			}
			fmt.Fprintf(b, " %s |", fmtScore(v))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// renderMethod writes the reproducibility block.
func renderMethod(b *strings.Builder, doc benchDoc) {
	b.WriteString("## Method\n\n")
	b.WriteString("- **Score direction:** lower is better. `cmd/piano-fit` minimizes the aggregate objective; ")
	b.WriteString("`best_score` in each `result.json` is that minimum.\n")
	fmt.Fprintf(b, "- **Budget:** `--max-evals %d` per run, with `--time-budget %d` pinned wide open. "+
		"piano-fit enforces a wall-clock deadline unconditionally and defaults it to 120s, so the deadline "+
		"has to be disabled explicitly for runs to be comparable by evaluation count rather than by "+
		"machine speed and load.\n", doc.MaxEvals, benchTimeBudgetSeconds)
	fmt.Fprintf(b, "- **Seeds:** %s (one independent piano-fit process each; all statistics are medians over these, never a mean of one repeated run).\n",
		formatSeeds(doc.Seeds))
	fmt.Fprintf(b, "- **piano-fit workers:** `--workers %d`. One worker keeps a run a single search rather than a portfolio of independent Mayfly rounds.\n", doc.Workers)
	// Qualified, because an artifact tree can be assembled over several driver
	// invocations — a re-run of one case, a follow-up at another budget — and
	// summary.json only records the most recent one. Stating a single number
	// flatly would be a claim about cells it never covered.
	fmt.Fprintf(b, "- **Driver parallelism:** %d concurrent runs in the most recent driver invocation. "+
		"A tree assembled over several invocations may have used other values; parallelism affects wall "+
		"time only, never the eval-budgeted scores.\n", doc.Jobs)
	fmt.Fprintf(b, "- **Binary:** `go build -tags %s -buildvcs=false -o <work>/piano-fit ./cmd/piano-fit`.\n", doc.BuildTags)
	if doc.Reference != "" {
		fmt.Fprintf(b, "- **Reference:** `%s`.\n", doc.Reference)
	}
	if doc.Preset != "" {
		fmt.Fprintf(b, "- **Base preset:** `%s`.\n", doc.Preset)
	}
	b.WriteString("- **Comparability caveat:** scores are only comparable within one scoring profile, and each `--pass` ")
	b.WriteString("selects its own profile. Compare configs within a case, never scores across cases.\n")
}

// formatSeeds renders the seed list for the Method block.
func formatSeeds(seeds []int64) string {
	if len(seeds) == 0 {
		return "unknown"
	}
	parts := make([]string, 0, len(seeds))
	for _, s := range seeds {
		parts = append(parts, fmt.Sprintf("%d", s))
	}

	return strings.Join(parts, ", ")
}
