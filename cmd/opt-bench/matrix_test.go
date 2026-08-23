package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpandMatrixShape pins the matrix cardinality and its iteration order:
// case-major, then config, then seed. The document tables rely on that order.
func TestExpandMatrixShape(t *testing.T) {
	opts := matrixOptions{OutDir: "out/optbench", MaxEvals: 1500}
	specs := expandMatrix(defaultCases(), defaultConfigs(), []int64{1, 2}, opts)

	want := len(defaultCases()) * len(defaultConfigs()) * 2
	if len(specs) != want {
		t.Fatalf("expandMatrix produced %d specs, want %d", len(specs), want)
	}
	if specs[0].Case != "sustain" || specs[0].Config != "baseline" || specs[0].Seed != 1 {
		t.Fatalf("first spec = %s/%s/seed%d, want sustain/baseline/seed1", specs[0].Case, specs[0].Config, specs[0].Seed)
	}
	if specs[1].Seed != 2 {
		t.Fatalf("seed is not the innermost loop: second spec seed = %d", specs[1].Seed)
	}

	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s.Dir] {
			t.Fatalf("duplicate artifact directory %q: runs would overwrite each other", s.Dir)
		}
		seen[s.Dir] = true
	}
}

// TestBuildSpecArgs pins the flags every run must carry, and the ones it must
// never carry.
func TestBuildSpecArgs(t *testing.T) {
	opts := matrixOptions{OutDir: "out/optbench", MaxEvals: 1500, Reference: "reference/c4.wav", Preset: "assets/presets/default.json"}
	spec := buildSpec(defaultCases()[0], defaultConfigs()[2], 7, opts)

	joined := strings.Join(spec.Args, " ")
	for _, want := range []string{
		"--workers 1",
		"--resume=false",
		"--max-evals 1500",
		"--seed 7",
		"--pass sustain",
		"--search halton",
		filepath.Join("out/optbench", "sustain", "halton", "seed7", "result.json"),
		filepath.Join("out/optbench", "sustain", "halton", "seed7", "trace.jsonl"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	// The wall-clock deadline must be pinned wide open, not left at
	// piano-fit's 120s default: an enforced default would truncate the slower
	// cases and turn an eval-budgeted comparison into a time-budgeted one
	// without saying so anywhere in the report.
	if want := fmt.Sprintf("--time-budget %d", benchTimeBudgetSeconds); !strings.Contains(joined, want) {
		t.Errorf("args %q must pin the wall-clock deadline with %q", joined, want)
	}
	if benchTimeBudgetSeconds < 3600 {
		t.Errorf("benchTimeBudgetSeconds = %d is short enough to truncate a real run", benchTimeBudgetSeconds)
	}
	// Case and config flags come last so a config can override a driver
	// default.
	if idx := strings.Index(joined, "--search halton"); idx < strings.Index(joined, "--max-evals") {
		t.Errorf("config flags must come after the driver defaults: %q", joined)
	}
}

// TestBuildSpecIROutput pins that the joint-ir case supplies --output-ir:
// piano-fit hard-errors without it when body-ir or room-ir are active.
func TestBuildSpecIROutput(t *testing.T) {
	opts := matrixOptions{OutDir: "out/optbench", MaxEvals: 100}
	for _, c := range defaultCases() {
		spec := buildSpec(c, defaultConfigs()[0], 1, opts)
		has := strings.Contains(strings.Join(spec.Args, " "), "--output-ir")
		if want := caseNeedsIR(c); has != want {
			t.Errorf("case %q: --output-ir present = %v, want %v", c.Name, has, want)
		}
	}
	if !caseNeedsIR(defaultCases()[3]) {
		t.Errorf("joint-ir case must be detected as IR-synthesising")
	}
}

// TestCommandLine checks the dry-run rendering is copy-pasteable.
func TestCommandLine(t *testing.T) {
	spec := buildSpec(defaultCases()[2], defaultConfigs()[1], 3, matrixOptions{OutDir: "out/optbench", MaxEvals: 42})
	line := spec.commandLine("out/optbench/bin/piano-fit")
	if !strings.HasPrefix(line, "out/optbench/bin/piano-fit ") {
		t.Fatalf("command line does not start with the binary: %q", line)
	}
	if !strings.Contains(line, "--optimize piano,mix") {
		t.Errorf("command line lost the case flags: %q", line)
	}
	if strings.Contains(line, "''") {
		t.Errorf("command line contains an empty argument: %q", line)
	}
}

func TestResolveSeeds(t *testing.T) {
	got, err := resolveSeeds("", 3)
	if err != nil {
		t.Fatalf("resolveSeeds: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("resolveSeeds(\"\", 3) = %v, want [1 2 3]", got)
	}

	got, err = resolveSeeds("1, 7,13", 5)
	if err != nil {
		t.Fatalf("resolveSeeds: %v", err)
	}
	if len(got) != 3 || got[1] != 7 {
		t.Fatalf("resolveSeeds(\"1,7,13\") = %v, want [1 7 13]", got)
	}

	if _, err := resolveSeeds("nope", 5); err == nil {
		t.Errorf("resolveSeeds must reject a non-integer seed list")
	}
	if _, err := resolveSeeds("", 0); err == nil {
		t.Errorf("resolveSeeds must reject a zero seed count")
	}
}

// TestLoadConfigsAppendsAndOverrides pins the --configs extension contract.
func TestLoadConfigsAppendsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.json")
	extra := []benchConfig{
		{Name: "warm-start", Flags: []string{"--mayfly-warm-start"}},
		{Name: "baseline", Flags: []string{"--mayfly-pop", "20"}},
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := loadConfigs(path)
	if err != nil {
		t.Fatalf("loadConfigs: %v", err)
	}
	if len(got) != len(defaultConfigs())+1 {
		t.Fatalf("loadConfigs returned %d configs, want %d", len(got), len(defaultConfigs())+1)
	}
	if got[0].Name != "baseline" || len(got[0].Flags) != 2 {
		t.Errorf("a same-named entry must replace the default, got %+v", got[0])
	}
	if got[len(got)-1].Name != "warm-start" {
		t.Errorf("new entries must be appended, got %+v", got[len(got)-1])
	}
}

func TestConfigOrderPutsDefaultsFirst(t *testing.T) {
	names := map[string]bool{"zeta": true, "halton": true, "baseline": true, "alpha": true}
	got := configOrder(names)
	want := []string{"baseline", "halton", "alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("configOrder = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("configOrder = %v, want %v", got, want)
		}
	}
}

// TestMedian covers the odd, even and all-NaN cases.
func TestMedian(t *testing.T) {
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median([3 1 2]) = %v, want 2", got)
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("median([4 1 3 2]) = %v, want 2.5", got)
	}
	if got := median([]float64{1, math.NaN(), 3}); got != 2 {
		t.Errorf("median must ignore NaN, got %v", got)
	}
	if got := median(nil); !math.IsNaN(got) {
		t.Errorf("median(nil) = %v, want NaN", got)
	}
}

func TestMinMax(t *testing.T) {
	lo, hi := minMax([]float64{3, math.NaN(), 1, 2})
	if lo != 1 || hi != 3 {
		t.Errorf("minMax = (%v, %v), want (1, 3)", lo, hi)
	}
	lo, hi = minMax(nil)
	if !math.IsNaN(lo) || !math.IsNaN(hi) {
		t.Errorf("minMax(nil) = (%v, %v), want (NaN, NaN)", lo, hi)
	}
}

// TestSelectCases pins that a typo is an error rather than an empty matrix: a
// silently-empty selection would produce a document that covers nothing while
// looking like a completed run.
func TestSelectCases(t *testing.T) {
	all := defaultCases()

	t.Run("empty selection keeps every case", func(t *testing.T) {
		got, err := selectCases(all, "")
		if err != nil || len(got) != len(all) {
			t.Fatalf("got %d cases, err %v; want all %d", len(got), err, len(all))
		}
	})

	t.Run("selection follows the declared order, not the flag order", func(t *testing.T) {
		got, err := selectCases(all, "attack, sustain")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].Name != "sustain" || got[1].Name != "attack" {
			t.Fatalf("got %v, want [sustain attack]", got)
		}
	})

	t.Run("unknown name is an error", func(t *testing.T) {
		if _, err := selectCases(all, "sustain,attak"); err == nil {
			t.Fatal("a misspelled case name must not silently produce a partial matrix")
		}
	})
}
