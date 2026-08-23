# Optimizer benchmark (2400 evaluations)

<!-- GENERATED FILE - DO NOT EDIT BY HAND. -->
<!-- Regenerate with: go run -buildvcs=false ./cmd/opt-bench --report --out-dir out/optbench-2400 --doc docs/optimizer-benchmark-2400.md -->

> **Generated.** This document is regenerated from the artifacts under `out/optbench-2400/`.
> Do not edit it by hand; run `go run -buildvcs=false ./cmd/opt-bench --report --out-dir out/optbench-2400 --doc docs/optimizer-benchmark-2400.md` instead.

**Lower score is better.** `best_score` is the objective `cmd/piano-fit` _minimizes_ (`cmd/piano-fit/optimize.go` accepts a candidate on `evalRes.aggregate < state.bestEval.aggregate`), so a negative delta in the tables below means the row won.

## Case `sustain`

`--pass sustain`, 5 knobs

| Config            | median best | min      | max      | median wall (s) | n seeds | failures | Δ vs `halton` |
| ----------------- | ----------- | -------- | -------- | --------------- | ------- | -------- | ------------- |
| `baseline`        | 0.343766    | 0.339981 | 0.349786 | 1324.2          | 3       | 0        | -0.065874     |
| `random`          | 0.416313    | 0.402906 | 0.436027 | 894.3           | 3       | 0        | +0.006674     |
| `halton`          | 0.409639    | 0.409639 | 0.409639 | 892.9           | 3       | 0        | +0.000000     |
| `long-round`      | 0.338633    | 0.338520 | 0.348512 | 1374.2          | 3       | 0        | -0.071006     |
| `warm-long-round` | 0.338061    | 0.337613 | 0.377782 | 1396.9          | 3       | 0        | -0.071579     |

**Verdict:** mayfly beats halton by 0.065874 (lower is better).

### Convergence (`sustain`)

Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.

| Config            | 10%      | 25%      | 50%      | 75%      | 100%     |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.388257 | 0.353913 | 0.349786 | 0.349786 | 0.343766 |
| `random`          | 0.485740 | 0.453575 | 0.441305 | 0.416313 | 0.416313 |
| `halton`          | 0.502152 | 0.411779 | 0.409639 | 0.409639 | 0.409639 |
| `long-round`      | 0.388257 | 0.350981 | 0.338794 | 0.338633 | 0.338633 |
| `warm-long-round` | 0.385234 | 0.343053 | 0.338657 | 0.338496 | 0.338061 |

### Proposed-score distribution (`sustain`)

Median over seeds of each run's own trace quantiles, over every score the run proposed (not just the one it kept). Every trace record counts: the objective sums clamped components, so 1.0 is a genuine worst case rather than a sentinel.

| Config            | min      | p05      | median   | p95      | IQR      |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.343766 | 0.354463 | 0.471788 | 0.959155 | 0.233604 |
| `random`          | 0.416313 | 0.603575 | 0.926516 | 1.000000 | 0.225273 |
| `halton`          | 0.409639 | 0.597357 | 0.921043 | 1.000000 | 0.229089 |
| `long-round`      | 0.338633 | 0.339432 | 0.354416 | 0.635131 | 0.076539 |
| `warm-long-round` | 0.338061 | 0.340430 | 0.350407 | 0.610512 | 0.041216 |

## Case `attack`

`--pass attack`, 9 knobs

| Config            | median best | min      | max      | median wall (s) | n seeds | failures | Δ vs `halton` |
| ----------------- | ----------- | -------- | -------- | --------------- | ------- | -------- | ------------- |
| `baseline`        | 0.439940    | 0.431386 | 0.444187 | 1399.0          | 3       | 0        | +0.003304     |
| `random`          | 0.439288    | 0.439054 | 0.455077 | 1428.3          | 3       | 0        | +0.002651     |
| `halton`          | 0.436636    | 0.436636 | 0.436636 | 1456.0          | 3       | 0        | +0.000000     |
| `long-round`      | 0.441306    | 0.437965 | 0.442609 | 1327.4          | 3       | 0        | +0.004669     |
| `warm-long-round` | 0.443818    | 0.439697 | 0.446501 | 1186.8          | 3       | 0        | +0.007182     |

**Verdict:** mayfly loses to halton by 0.003304 (lower is better).

### Convergence (`attack`)

Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.

| Config            | 10%      | 25%      | 50%      | 75%      | 100%     |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.444720 | 0.444287 | 0.443286 | 0.443286 | 0.439954 |
| `random`          | 0.445842 | 0.445842 | 0.443926 | 0.442748 | 0.439054 |
| `halton`          | 0.439828 | 0.436636 | 0.436636 | 0.436636 | 0.436636 |
| `long-round`      | 0.444720 | 0.444239 | 0.442866 | 0.442041 | 0.441306 |
| `warm-long-round` | 0.445965 | 0.442040 | 0.439693 | 0.439693 | 0.439693 |

### Proposed-score distribution (`attack`)

Median over seeds of each run's own trace quantiles, over every score the run proposed (not just the one it kept). Every trace record counts: the objective sums clamped components, so 1.0 is a genuine worst case rather than a sentinel.

| Config            | min      | p05      | median   | p95      | IQR      |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.439954 | 0.444596 | 0.447334 | 0.487709 | 0.003947 |
| `random`          | 0.439054 | 0.447279 | 0.476086 | 0.666012 | 0.063245 |
| `halton`          | 0.436636 | 0.447210 | 0.473942 | 0.660869 | 0.062966 |
| `long-round`      | 0.441306 | 0.441921 | 0.445182 | 0.458510 | 0.005073 |
| `warm-long-round` | 0.439693 | 0.439760 | 0.446215 | 0.454682 | 0.006180 |

## Method

- **Score direction:** lower is better. `cmd/piano-fit` minimizes the aggregate objective; `best_score` in each `result.json` is that minimum.
- **Budget:** `--max-evals 2400` per run, with `--time-budget 86400` pinned wide open. piano-fit enforces a wall-clock deadline unconditionally and defaults it to 120s, so the deadline has to be disabled explicitly for runs to be comparable by evaluation count rather than by machine speed and load.
- **Seeds:** 1, 2, 3 (one independent piano-fit process each; all statistics are medians over these, never a mean of one repeated run).
- **piano-fit workers:** `--workers 1`. One worker keeps a run a single search rather than a portfolio of independent Mayfly rounds.
- **Driver parallelism:** 10 concurrent runs in the most recent driver invocation. A tree assembled over several invocations may have used other values; parallelism affects wall time only, never the eval-budgeted scores.
- **Binary:** `go build -tags asm -buildvcs=false -o <work>/piano-fit ./cmd/piano-fit`.
- **Reference:** `reference/c4.wav`.
- **Base preset:** `assets/presets/default.json`.
- **Comparability caveat:** scores are only comparable within one scoring profile, and each `--pass` selects its own profile. Compare configs within a case, never scores across cases.
