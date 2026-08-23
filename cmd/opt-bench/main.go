// Command opt-bench drives cmd/piano-fit across a matrix of fitting cases,
// search configurations and seeds, and renders the collected artifacts into
// docs/optimizer-benchmark.md.
//
// It exists to answer one question: does the Mayfly metaheuristic in
// cmd/piano-fit actually beat trivial controls (uniform random sampling, a
// Halton low-discrepancy sequence) at the same evaluation budget? Answering it
// honestly needs independent invocations rather than one run's internal
// statistics, which is why every cell of the matrix is a separate process with
// its own seed and its own artifact directory.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// buildTags is the tag set the driver builds piano-fit with. It is recorded in
// summary.json and in the generated document because the asm kernels change
// how many evaluations fit in a given wall time.
const buildTags = "asm"

// options collects every driver flag.
type options struct {
	outDir    string
	buildDir  string
	docPath   string
	configs   string
	cases     string
	reference string
	preset    string
	maxEvals  int
	seeds     int
	seedList  string
	jobs      int
	dryRun    bool
	force     bool
	report    bool
}

func main() {
	opts := parseFlags()

	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "opt-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.outDir, "out-dir", "out/optbench", "Root directory for run artifacts and summary.json")
	flag.StringVar(&opts.buildDir, "build-dir", "out/optbench/bin", "Directory the piano-fit binary is built into")
	flag.StringVar(&opts.docPath, "doc", "docs/optimizer-benchmark.md", "Markdown document --report writes")
	flag.StringVar(&opts.configs, "configs", "", "Optional JSON file with extra search configurations to append to the matrix")
	flag.StringVar(&opts.cases, "cases", "",
		"Optional comma-separated case names to restrict the matrix to, e.g. \"sustain,attack\" (default: all)")
	flag.StringVar(&opts.reference, "reference", "reference/c4.wav", "Reference WAV passed to every run")
	flag.StringVar(&opts.preset, "preset", "assets/presets/default.json", "Base preset passed to every run")
	flag.IntVar(&opts.maxEvals, "max-evals", 1500, "Evaluation budget per run (never a time budget: runs must be comparable by eval count)")
	flag.IntVar(&opts.seeds, "seeds", 5, "Number of seeds per (case, config), numbered 1..n")
	flag.StringVar(&opts.seedList, "seed-list", "", "Explicit comma-separated seed list, e.g. \"1,7,13\" (wins over --seeds)")
	flag.IntVar(&opts.jobs, "jobs", 0, "Concurrent piano-fit processes (0 = max(1, GOMAXPROCS-2))")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "Print the command lines that would be executed and exit")
	flag.BoolVar(&opts.force, "force", false, "Re-run cells whose result.json already exists")
	flag.BoolVar(&opts.report, "report", false, "Skip the matrix: render the document from the existing artifacts under --out-dir")
	flag.Parse()

	return opts
}

// run dispatches to the report or matrix path.
func run(opts options) error {
	if opts.report {
		return runReport(opts)
	}

	return runBench(opts)
}

// resolveMatrix builds the case/config/seed matrix from the flags.
func resolveMatrix(opts options) ([]benchCase, []benchConfig, []int64, error) {
	cases, err := selectCases(defaultCases(), opts.cases)
	if err != nil {
		return nil, nil, nil, err
	}
	configs := defaultConfigs()
	if opts.configs != "" {
		if configs, err = loadConfigs(opts.configs); err != nil {
			return nil, nil, nil, err
		}
	}
	seeds, err := resolveSeeds(opts.seedList, opts.seeds)
	if err != nil {
		return nil, nil, nil, err
	}

	return cases, configs, seeds, nil
}

// runBench builds piano-fit once and executes the whole matrix.
func runBench(opts options) error {
	if opts.maxEvals < 1 {
		return fmt.Errorf("--max-evals must be >= 1, got %d", opts.maxEvals)
	}
	cases, configs, seeds, err := resolveMatrix(opts)
	if err != nil {
		return err
	}

	jobs := opts.jobs
	if jobs < 1 {
		jobs = defaultJobs()
	}

	// --dry-run must not build anything, so the binary path is only a label
	// here. It is the same path a real run would use.
	bin := filepath.Join(opts.buildDir, "piano-fit")
	matOpts := matrixOptions{
		OutDir:    opts.outDir,
		MaxEvals:  opts.maxEvals,
		Reference: opts.reference,
		Preset:    opts.preset,
	}
	specs := expandMatrix(cases, configs, seeds, matOpts)

	if opts.dryRun {
		fmt.Printf("# go build -tags %s -buildvcs=false -o %s ./cmd/piano-fit\n", buildTags, bin)
		fmt.Printf("# %d runs = %d cases x %d configs x %d seeds, %d concurrent\n",
			len(specs), len(cases), len(configs), len(seeds), jobs)
		for _, spec := range specs {
			fmt.Println(spec.commandLine(bin))
		}

		return nil
	}

	if bin, err = buildPianoFit(opts.buildDir); err != nil {
		return err
	}

	// SIGINT stops the pool from launching further runs; in-flight processes
	// are left to finish so their result.json is complete and reusable.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	outcomes := runMatrix(ctx, bin, specs, jobs, opts.force)
	sortOutcomes(outcomes)

	sum := matrixSummary{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Command:     regenerateCommand(opts),
		MaxEvals:    opts.maxEvals,
		Seeds:       seeds,
		Jobs:        jobs,
		Workers:     1,
		BuildTags:   buildTags,
		Reference:   opts.reference,
		Preset:      opts.preset,
		Runs:        outcomes,
	}
	summaryPath := filepath.Join(opts.outDir, "summary.json")
	if err := writeSummary(summaryPath, sum); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "opt-bench: wrote %s (%d runs, %d failed)\n",
		summaryPath, len(outcomes), countFailures(outcomes))

	if ctx.Err() != nil {
		return fmt.Errorf("interrupted: the matrix is incomplete, rerun to fill the gaps")
	}

	return nil
}

// countFailures counts runs with no usable result.
func countFailures(rows []runOutcome) int {
	n := 0
	for _, r := range rows {
		if !r.OK {
			n++
		}
	}

	return n
}

// runReport regenerates the document from artifacts alone. It never builds or
// runs piano-fit, so it works on an artifact tree copied from another machine.
func runReport(opts options) error {
	_, _, seeds, err := resolveMatrix(opts)
	if err != nil {
		return err
	}
	fallback := dataset{
		OutDir:    opts.outDir,
		MaxEvals:  opts.maxEvals,
		Jobs:      opts.jobs,
		Workers:   1,
		BuildTags: buildTags,
		Seeds:     seeds,
		Reference: opts.reference,
		Preset:    opts.preset,
	}
	if fallback.Jobs < 1 {
		fallback.Jobs = defaultJobs()
	}

	ds, err := loadDataset(opts.outDir, fallback)
	if err != nil {
		return err
	}
	if len(ds.Records) == 0 {
		return fmt.Errorf("no run artifacts found under %s (run the matrix first)", opts.outDir)
	}

	doc := buildDoc(ds, regenerateCommand(opts))
	if err := os.MkdirAll(filepath.Dir(opts.docPath), 0o755); err != nil {
		return fmt.Errorf("doc dir: %w", err)
	}
	if err := os.WriteFile(opts.docPath, []byte(renderMarkdown(doc)), 0o600); err != nil {
		return fmt.Errorf("write doc: %w", err)
	}
	fmt.Fprintf(os.Stderr, "opt-bench: wrote %s from %d runs\n", opts.docPath, len(ds.Records))

	return nil
}

// regenerateCommand renders the exact command that reproduces the current
// invocation, so the generated header is actionable rather than decorative.
func regenerateCommand(opts options) string {
	parts := []string{"go", "run", "-buildvcs=false", "./cmd/opt-bench", "--report"}
	if opts.outDir != "out/optbench" {
		parts = append(parts, "--out-dir", opts.outDir)
	}
	if opts.docPath != "docs/optimizer-benchmark.md" {
		parts = append(parts, "--doc", opts.docPath)
	}
	if opts.configs != "" {
		parts = append(parts, "--configs", opts.configs)
	}
	if opts.cases != "" {
		parts = append(parts, "--cases", opts.cases)
	}
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		quoted = append(quoted, shellQuote(p))
	}

	return strings.Join(quoted, " ")
}
