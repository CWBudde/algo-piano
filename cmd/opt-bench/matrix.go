package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// benchCase is one objective the optimizer is asked to solve: a named set of
// piano-fit flags that pins the pass and the knob groups, and therefore the
// dimensionality of the search space. The four defaults span 5 to 30+ knobs on
// purpose — a metaheuristic that only pays for itself in low dimensions (or
// only in high ones) is a different finding from one that never pays for
// itself.
type benchCase struct {
	Name  string   `json:"name"`
	Desc  string   `json:"desc,omitempty"`
	Flags []string `json:"flags"`
}

// benchConfig is one search condition applied on top of every case. The
// control conditions (`random`, `halton`) exist so the matrix answers "does
// the stochastic search beat a trivial sampler at the same eval budget?"
// rather than "how good is the score in absolute terms?".
type benchConfig struct {
	Name  string   `json:"name"`
	Desc  string   `json:"desc,omitempty"`
	Flags []string `json:"flags"`
}

// defaultCases returns the fixed four-case matrix.
//
// The --optimize tokens are the ones cmd/piano-fit/knobs.go parseGroups
// accepts: piano, body-ir, room-ir, mix. Anything else is a hard error there,
// so they are spelled out rather than derived.
func defaultCases() []benchCase {
	return []benchCase{
		{
			Name:  "sustain",
			Desc:  "`--pass sustain`, 5 knobs",
			Flags: []string{"--pass", "sustain"},
		},
		{
			Name:  "attack",
			Desc:  "`--pass attack`, 9 knobs",
			Flags: []string{"--pass", "attack"},
		},
		{
			Name:  "piano-mix",
			Desc:  "`--pass none --optimize piano,mix`, 20 knobs",
			Flags: []string{"--pass", "none", "--optimize", "piano,mix"},
		},
		{
			Name:  "joint-ir",
			Desc:  "`--optimize piano,body-ir,room-ir,mix`, 30+ knobs",
			Flags: []string{"--optimize", "piano,body-ir,room-ir,mix"},
		},
	}
}

// defaultConfigs returns the minimum shipped condition set. `baseline` passes
// no extra flags at all, which is what makes it the reference: every
// audit-added --mayfly-* flag defaults to today's behaviour, so "no extra
// flags" is exactly the tool as it shipped.
func defaultConfigs() []benchConfig {
	return []benchConfig{
		{Name: "baseline", Desc: "mayfly, stock settings", Flags: nil},
		{Name: "random", Desc: "uniform random sampler", Flags: []string{"--search", "random"}},
		{Name: "halton", Desc: "scrambled Halton low-discrepancy sampler", Flags: []string{"--search", "halton"}},
	}
}

// selectCases restricts the matrix to the named cases, in the order the
// defaults declare them so the document's case order stays stable however the
// flag was spelled. An empty selection keeps every case.
//
// An unknown name is an error rather than an empty matrix: a typo would
// otherwise produce a document that silently covers nothing.
func selectCases(all []benchCase, names string) ([]benchCase, error) {
	names = strings.TrimSpace(names)
	if names == "" {
		return all, nil
	}
	want := map[string]bool{}
	for _, n := range strings.Split(names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	out := make([]benchCase, 0, len(want))
	for _, c := range all {
		if want[c.Name] {
			out = append(out, c)
			delete(want, c.Name)
		}
	}
	if len(want) > 0 {
		unknown := make([]string, 0, len(want))
		for n := range want {
			unknown = append(unknown, n)
		}
		sort.Strings(unknown)

		return nil, fmt.Errorf("--cases: unknown case name(s) %s", strings.Join(unknown, ", "))
	}

	return out, nil
}

// loadConfigs reads an extra condition set from JSON so Stage B can extend the
// matrix without recompiling. The file is a JSON array of benchConfig, e.g.
//
//	[{"name": "warm-start", "flags": ["--mayfly-warm-start"]}]
//
// The shipped defaults always stay in the matrix; the file appends to them,
// and a file entry whose name collides with a default replaces it.
func loadConfigs(path string) ([]benchConfig, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied matrix definition
	if err != nil {
		return nil, fmt.Errorf("read configs: %w", err)
	}
	var extra []benchConfig
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, fmt.Errorf("parse configs %s: %w", path, err)
	}
	out := defaultConfigs()
	for _, cfg := range extra {
		if strings.TrimSpace(cfg.Name) == "" {
			return nil, fmt.Errorf("parse configs %s: entry with empty name", path)
		}
		replaced := false
		for i := range out {
			if out[i].Name == cfg.Name {
				out[i] = cfg
				replaced = true

				break
			}
		}
		if !replaced {
			out = append(out, cfg)
		}
	}

	return out, nil
}

// matrixOptions carries everything the command-line builder needs that is not
// part of the matrix itself.
type matrixOptions struct {
	OutDir    string
	MaxEvals  int
	Reference string
	Preset    string
}

// runSpec is one fully resolved (case, config, seed) invocation.
type runSpec struct {
	Case       string
	MaxEvals   int
	Config     string
	Seed       int64
	Dir        string
	ReportPath string
	TracePath  string
	LogPath    string
	Args       []string
}

// parseSeedList turns "1,7,13" into seeds. An empty string yields nil.
func parseSeedList(s string) ([]int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("seed-list: %q is not an integer", part)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("seed-list: no seeds parsed from %q", s)
	}

	return out, nil
}

// resolveSeeds prefers an explicit list over the 1..n range.
func resolveSeeds(list string, count int) ([]int64, error) {
	seeds, err := parseSeedList(list)
	if err != nil {
		return nil, err
	}
	if seeds != nil {
		return seeds, nil
	}
	if count < 1 {
		return nil, fmt.Errorf("seeds must be >= 1, got %d", count)
	}
	seeds = make([]int64, 0, count)
	for i := 1; i <= count; i++ {
		seeds = append(seeds, int64(i))
	}

	return seeds, nil
}

// caseNeedsIR reports whether a case activates the body-ir or room-ir knob
// groups. piano-fit hard-errors without --output-ir in that situation
// (validateOptimizerFlags), so the driver has to supply one per run.
func caseNeedsIR(c benchCase) bool {
	for i, f := range c.Flags {
		if f != "--optimize" && f != "-optimize" {
			continue
		}
		if i+1 >= len(c.Flags) {
			continue
		}
		for _, g := range strings.Split(c.Flags[i+1], ",") {
			switch strings.TrimSpace(g) {
			case "body-ir", "room-ir":
				return true
			}
		}
	}

	return false
}

// benchTimeBudgetSeconds is "no wall-clock limit" expressed in the units the
// flag takes. It is a backstop, not a budget: it has to outlast the slowest
// case (joint-ir at a high --max-evals) by a wide margin, or it would become
// the thing the matrix measures.
const benchTimeBudgetSeconds = 86400

// buildSpec resolves one invocation's directory layout and argument vector.
//
// Argument order matters: the fixed driver flags come first, then the case
// flags, then the config flags. Go's flag package lets a later occurrence win,
// so a case may override a driver default and a config may override both —
// which is what makes --configs a general extension point.
//
// Two deliberate omissions:
//
//   - --workers is pinned to 1. piano-fit's workers run independent Mayfly
//     rounds, so a multi-worker run is a portfolio of short searches rather
//     than one long one; comparing that against a single-threaded sampler
//     would confound the search strategy with the restart schedule.
//   - --time-budget is pinned wide open rather than left at its default.
//     Runs must be comparable by evaluation count, and piano-fit enforces a
//     wall-clock deadline built from --time-budget unconditionally
//     (optimize.go:234) with a 120s default. Omitting the flag would therefore
//     time-budget every run instead of eval-budgeting it, and a slow case
//     would silently report the score it happened to reach in two minutes. A
//     --configs entry can still lower it deliberately.
func buildSpec(c benchCase, cfg benchConfig, seed int64, opts matrixOptions) runSpec {
	dir := filepath.Join(opts.OutDir, c.Name, cfg.Name, fmt.Sprintf("seed%d", seed))
	reportPath := filepath.Join(dir, "result.json")
	tracePath := filepath.Join(dir, "trace.jsonl")

	args := []string{
		"--workers", "1",
		"--resume=false",
		"--max-evals", strconv.Itoa(opts.MaxEvals),
		"--time-budget", strconv.Itoa(benchTimeBudgetSeconds),
		"--seed", strconv.FormatInt(seed, 10),
		"--report", reportPath,
		"--trace", tracePath,
		"--output-preset", filepath.Join(dir, "preset.json"),
		"--work-dir", filepath.Join(dir, "work"),
	}
	if opts.Reference != "" {
		args = append(args, "--reference", opts.Reference)
	}
	if opts.Preset != "" {
		args = append(args, "--preset", opts.Preset)
	}
	if caseNeedsIR(c) {
		args = append(args, "--output-ir", filepath.Join(dir, "synth-ir.wav"))
	}
	args = append(args, c.Flags...)
	args = append(args, cfg.Flags...)

	return runSpec{
		MaxEvals:   opts.MaxEvals,
		Case:       c.Name,
		Config:     cfg.Name,
		Seed:       seed,
		Dir:        dir,
		ReportPath: reportPath,
		TracePath:  tracePath,
		LogPath:    filepath.Join(dir, "run.log"),
		Args:       args,
	}
}

// expandMatrix walks cases x configs x seeds in a stable order.
func expandMatrix(cases []benchCase, configs []benchConfig, seeds []int64, opts matrixOptions) []runSpec {
	specs := make([]runSpec, 0, len(cases)*len(configs)*len(seeds))
	for _, c := range cases {
		for _, cfg := range configs {
			for _, seed := range seeds {
				specs = append(specs, buildSpec(c, cfg, seed, opts))
			}
		}
	}

	return specs
}

// commandLine renders a spec as a copy-pasteable shell command.
func (s runSpec) commandLine(bin string) string {
	parts := make([]string, 0, len(s.Args)+1)
	parts = append(parts, shellQuote(bin))
	for _, a := range s.Args {
		parts = append(parts, shellQuote(a))
	}

	return strings.Join(parts, " ")
}

// shellQuote quotes an argument only when it needs it, so the dry-run output
// stays readable.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !shellSafeRune(r) }) < 0 {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellSafeRune reports whether r may appear unquoted in a shell word.
func shellSafeRune(r rune) bool {
	switch {
	case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	case r == '-', r == '_', r == '.', r == '/', r == ',', r == '=', r == ':':
		return true
	default:
		return false
	}
}

// configOrder returns the config names in a stable presentation order: the
// shipped defaults first (baseline, random, halton), then anything a
// --configs file added, alphabetically.
func configOrder(names map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, cfg := range defaultConfigs() {
		if names[cfg.Name] {
			out = append(out, cfg.Name)
			seen[cfg.Name] = true
		}
	}
	var rest []string
	for name := range names {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)

	return append(out, rest...)
}

// caseOrder returns the case names in matrix order, then anything unexpected.
func caseOrder(names map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range defaultCases() {
		if names[c.Name] {
			out = append(out, c.Name)
			seen[c.Name] = true
		}
	}
	var rest []string
	for name := range names {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)

	return append(out, rest...)
}
