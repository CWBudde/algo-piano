package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

// fitReport is the subset of cmd/piano-fit's runReport this driver reads. The
// field names are copied from cmd/piano-fit/output.go; anything else in the
// file is ignored on purpose so a new piano-fit field cannot break the driver.
type fitReport struct {
	BestScore         float64 `json:"best_score"`
	BestSimilarity    float64 `json:"best_similarity"`
	Evaluations       int     `json:"evaluations"`
	ElapsedSeconds    float64 `json:"elapsed_seconds"`
	ScoreProfile      string  `json:"score_profile"`
	SearchMode        string  `json:"search_mode"`
	EvalsPerIteration float64 `json:"evals_per_iteration"`
}

// runOutcome is one row of summary.json.
type runOutcome struct {
	Case    string `json:"case"`
	Config  string `json:"config"`
	Seed    int64  `json:"seed"`
	Dir     string `json:"dir"`
	Skipped bool   `json:"skipped,omitempty"`
	// Complete records that the run reached the end of its evaluation budget
	// and left a completion marker behind. Only complete runs belong in a
	// fixed-budget comparison.
	Complete          bool    `json:"complete,omitempty"`
	OK                bool    `json:"ok"`
	ExitCode          int     `json:"exit_code"`
	Error             string  `json:"error,omitempty"`
	WallSeconds       float64 `json:"wall_seconds"`
	BestScore         float64 `json:"best_score,omitempty"`
	Evaluations       int     `json:"evaluations,omitempty"`
	EvalsPerIteration float64 `json:"evals_per_iteration,omitempty"`
	SearchMode        string  `json:"search_mode,omitempty"`
	ScoreProfile      string  `json:"score_profile,omitempty"`
}

// matrixSummary is out/optbench/summary.json.
type matrixSummary struct {
	GeneratedAt string       `json:"generated_at"`
	Command     string       `json:"command"`
	MaxEvals    int          `json:"max_evals"`
	Seeds       []int64      `json:"seeds"`
	Jobs        int          `json:"jobs"`
	Workers     int          `json:"piano_fit_workers"`
	BuildTags   string       `json:"build_tags"`
	Reference   string       `json:"reference,omitempty"`
	Preset      string       `json:"preset,omitempty"`
	Runs        []runOutcome `json:"runs"`
}

// defaultJobs leaves headroom so the driver does not starve the machine it
// measures on: an oversubscribed box inflates every wall-clock number.
func defaultJobs() int {
	if n := runtime.GOMAXPROCS(0) - 2; n > 1 {
		return n
	}

	return 1
}

// buildPianoFit builds the fitter once and returns the binary path.
//
// -buildvcs=false matters: the driver runs with a dirty tree by design, and
// VCS stamping fails or churns the build cache in that situation.
func buildPianoFit(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("build dir: %w", err)
	}
	bin := filepath.Join(dir, "piano-fit")
	cmd := exec.Command("go", "build", "-tags", "asm", "-buildvcs=false", "-o", bin, "./cmd/piano-fit")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build piano-fit: %w", err)
	}

	return bin, nil
}

// executeOne runs a single piano-fit process to completion, tees its combined
// output into run.log and parses the report it wrote.
func executeOne(bin string, spec runSpec) runOutcome {
	out := runOutcome{Case: spec.Case, Config: spec.Config, Seed: spec.Seed, Dir: spec.Dir}

	if err := os.MkdirAll(spec.Dir, 0o755); err != nil {
		out.Error = err.Error()

		return out
	}
	logFile, err := os.Create(spec.LogPath)
	if err != nil {
		out.Error = err.Error()

		return out
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(bin, spec.Args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	start := time.Now()
	runErr := cmd.Run()
	out.WallSeconds = time.Since(start).Seconds()

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			out.ExitCode = exitErr.ExitCode()
		} else {
			out.ExitCode = -1
		}
		out.Error = runErr.Error()

		return out
	}

	rep, err := readFitReport(spec.ReportPath)
	if err != nil {
		out.Error = err.Error()

		return out
	}
	// A zero exit is not proof the search ran. piano-fit writes a full report
	// even when the search never started — a --search halton run whose
	// dimensionality exceeded the base table used to exit 0 after its single
	// seed evaluation, with a report that read like a finished run. A cell
	// that did not spend its budget is not a fixed-budget observation.
	if spec.MaxEvals > 0 && rep.Evaluations < minCompleteEvals(spec.MaxEvals) {
		out.Error = fmt.Sprintf("run spent only %d of %d evaluations; the search did not run",
			rep.Evaluations, spec.MaxEvals)
		out.ExitCode = -1

		return out
	}

	out.OK = true
	out.BestScore = rep.BestScore
	out.Evaluations = rep.Evaluations
	out.EvalsPerIteration = rep.EvalsPerIteration
	out.SearchMode = rep.SearchMode
	out.ScoreProfile = rep.ScoreProfile
	out.Complete = writeCompletionMarker(spec.Dir)

	return out
}

// readFitReport parses one piano-fit report JSON.
func readFitReport(path string) (fitReport, error) {
	var rep fitReport
	raw, err := os.ReadFile(path) //nolint:gosec // path is driver-constructed
	if err != nil {
		return rep, fmt.Errorf("read report: %w", err)
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return rep, fmt.Errorf("parse report %s: %w", path, err)
	}

	return rep, nil
}

// runMatrix executes every spec through a bounded worker pool.
//
// ctx cancellation (SIGINT) stops the pool from LAUNCHING further runs but
// never kills an in-flight process: a half-written result.json would be
// indistinguishable from a completed one on the next --force-less rerun.
func runMatrix(ctx context.Context, bin string, specs []runSpec, jobs int, force bool) []runOutcome {
	if jobs < 1 {
		jobs = 1
	}

	var (
		mu       sync.Mutex
		outcomes = make([]runOutcome, len(specs))
		done     int
	)
	total := len(specs)

	work := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				spec := specs[i]
				if ctx.Err() != nil {
					outcomes[i] = runOutcome{
						Case: spec.Case, Config: spec.Config, Seed: spec.Seed, Dir: spec.Dir,
						Skipped: true, Error: "interrupted before launch",
					}

					continue
				}
				if !force {
					// The marker, not result.json: piano-fit writes an initial
					// report at its first evaluation, so a run that was killed
					// or crashed leaves a result.json holding the score of a
					// search that never happened. Resuming against that would
					// silently mix truncated runs into a fixed-budget
					// comparison.
					if rep, err := readCompletedReport(spec); err == nil {
						outcomes[i] = runOutcome{
							Case: spec.Case, Config: spec.Config, Seed: spec.Seed, Dir: spec.Dir,
							Skipped: true, OK: true, Complete: true, WallSeconds: rep.ElapsedSeconds,
							BestScore: rep.BestScore, Evaluations: rep.Evaluations,
							EvalsPerIteration: rep.EvalsPerIteration, SearchMode: rep.SearchMode,
							ScoreProfile: rep.ScoreProfile,
						}
						mu.Lock()
						done++
						fmt.Fprintf(os.Stderr, "[%d/%d] %s/%s/seed%d skipped (already complete)\n",
							done, total, spec.Case, spec.Config, spec.Seed)
						mu.Unlock()

						continue
					}
				}

				res := executeOne(bin, spec)
				outcomes[i] = res

				mu.Lock()
				done++
				if res.OK {
					fmt.Fprintf(os.Stderr, "[%d/%d] %s/%s/seed%d done in %.1fs, best=%.6f\n",
						done, total, res.Case, res.Config, res.Seed, res.WallSeconds, res.BestScore)
				} else {
					fmt.Fprintf(os.Stderr, "[%d/%d] %s/%s/seed%d FAILED after %.1fs (exit %d): %s\n",
						done, total, res.Case, res.Config, res.Seed, res.WallSeconds, res.ExitCode, res.Error)
				}
				mu.Unlock()
			}
		}()
	}

	for i := range specs {
		work <- i
	}
	close(work)
	wg.Wait()

	return outcomes
}

// writeSummary persists summary.json next to the run artifacts.
func writeSummary(path string, sum matrixSummary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("summary dir: %w", err)
	}
	raw, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}

// sortOutcomes gives summary.json a deterministic row order regardless of the
// order the worker pool finished in.
func sortOutcomes(rows []runOutcome) {
	caseRank := rankOf(defaultCases(), func(c benchCase) string { return c.Name })
	cfgRank := rankOf(defaultConfigs(), func(c benchConfig) string { return c.Name })
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ra, rb := caseRank(a.Case), caseRank(b.Case); ra != rb {
			return ra < rb
		}
		if a.Case != b.Case {
			return a.Case < b.Case
		}
		if ra, rb := cfgRank(a.Config), cfgRank(b.Config); ra != rb {
			return ra < rb
		}
		if a.Config != b.Config {
			return a.Config < b.Config
		}

		return a.Seed < b.Seed
	})
}

// rankOf builds a name -> position lookup that sorts unknown names last.
func rankOf[T any](items []T, name func(T) string) func(string) int {
	rank := make(map[string]int, len(items))
	for i, it := range items {
		rank[name(it)] = i
	}

	return func(s string) int {
		if r, ok := rank[s]; ok {
			return r
		}

		return len(items)
	}
}

// minCompleteEvals is the evaluation count below which a run is treated as
// having failed rather than finished. The search reserves evaluations
// atomically and stops exactly at the budget, so a healthy run lands on it; the
// margin only tolerates a search that ends a hair early.
func minCompleteEvals(budget int) int {
	return budget - budget/10
}

// completionMarker is the file name a finished run leaves in its directory.
const completionMarker = "complete"

// writeCompletionMarker records that a run finished its whole budget. It is a
// separate file rather than a field in result.json because piano-fit owns that
// file and rewrites it throughout the search.
func writeCompletionMarker(dir string) bool {
	path := filepath.Join(dir, completionMarker)

	return os.WriteFile(path, []byte("ok\n"), 0o600) == nil
}

// readCompletedReport returns a run's report only when the run is known to have
// finished. A missing marker means "re-run it", which is the safe default: the
// alternative is treating a truncated search as a measurement.
func readCompletedReport(spec runSpec) (fitReport, error) {
	if _, err := os.Stat(filepath.Join(spec.Dir, completionMarker)); err != nil {
		return fitReport{}, fmt.Errorf("run %s is not marked complete: %w", spec.Dir, err)
	}

	return readFitReport(spec.ReportPath)
}
