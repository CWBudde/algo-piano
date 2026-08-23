# Optimizer benchmark (600 evaluations)

<!-- GENERATED FILE - DO NOT EDIT BY HAND. -->
<!-- Regenerate with: go run -buildvcs=false ./cmd/opt-bench --report -->

> **Generated.** This document is regenerated from the artifacts under `out/optbench/`.
> Do not edit it by hand; run `go run -buildvcs=false ./cmd/opt-bench --report` instead.

**Lower score is better.** `best_score` is the objective `cmd/piano-fit` _minimizes_ (`cmd/piano-fit/optimize.go` accepts a candidate on `evalRes.aggregate < state.bestEval.aggregate`), so a negative delta in the tables below means the row won.

## Case `sustain`

`--pass sustain`, 5 knobs

| Config            | median best | min      | max      | median wall (s) | n seeds | failures | Δ vs `halton` |
| ----------------- | ----------- | -------- | -------- | --------------- | ------- | -------- | ------------- |
| `baseline`        | 0.353913    | 0.339981 | 0.357134 | 340.8           | 5       | 0        | -0.057866     |
| `random`          | 0.441305    | 0.399765 | 0.485740 | 240.1           | 5       | 0        | +0.029527     |
| `halton`          | 0.411779    | 0.411779 | 0.411779 | 230.5           | 5       | 0        | +0.000000     |
| `long-round`      | 0.350981    | 0.339981 | 0.354155 | 338.6           | 5       | 0        | -0.060798     |
| `warm-long-round` | 0.342558    | 0.338805 | 0.380377 | 336.6           | 5       | 0        | -0.069221     |
| `warm-start`      | 0.346250    | 0.338805 | 0.380377 | 334.8           | 5       | 0        | -0.065529     |

**Verdict:** mayfly beats halton by 0.057866 (lower is better).

### Convergence (`sustain`)

Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.

| Config            | 10%      | 25%      | 50%      | 75%      | 100%     |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.493265 | 0.443117 | 0.357001 | 0.354659 | 0.353913 |
| `random`          | 0.478722 | 0.453575 | 0.445012 | 0.445012 | 0.441305 |
| `halton`          | 0.527896 | 0.504218 | 0.502152 | 0.411779 | 0.411779 |
| `long-round`      | 0.493265 | 0.443117 | 0.357001 | 0.354659 | 0.350981 |
| `warm-long-round` | 0.493265 | 0.400307 | 0.355417 | 0.352735 | 0.342558 |
| `warm-start`      | 0.493265 | 0.400307 | 0.355417 | 0.352735 | 0.346250 |

### Proposed-score distribution (`sustain`)

Median over seeds of each run's own trace quantiles, over every score the run proposed (not just the one it kept). Penalty scores, which report the budget rather than the landscape, are excluded.

| Config            | min      | p05      | median   | p95      | IQR      |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.353913 | 0.357522 | 0.453991 | 0.673836 | 0.194167 |
| `random`          | 0.441305 | 0.590987 | 0.930238 | 1.000000 | 0.228231 |
| `halton`          | 0.411779 | 0.596814 | 0.923291 | 1.000000 | 0.226850 |
| `long-round`      | 0.350981 | 0.357392 | 0.439145 | 0.663791 | 0.187408 |
| `warm-long-round` | 0.342558 | 0.351203 | 0.388806 | 0.649757 | 0.153023 |
| `warm-start`      | 0.346250 | 0.354570 | 0.397467 | 0.658313 | 0.153381 |

## Case `attack`

`--pass attack`, 9 knobs

| Config            | median best | min      | max      | median wall (s) | n seeds | failures | Δ vs `halton` |
| ----------------- | ----------- | -------- | -------- | --------------- | ------- | -------- | ------------- |
| `baseline`        | 0.443300    | 0.425318 | 0.444360 | 350.1           | 5       | 0        | +0.006663     |
| `random`          | 0.445212    | 0.443410 | 0.445876 | 359.3           | 5       | 0        | +0.008575     |
| `halton`          | 0.436636    | 0.436636 | 0.436636 | 364.4           | 5       | 0        | +0.000000     |
| `long-round`      | 0.443270    | 0.425318 | 0.444270 | 355.3           | 5       | 0        | +0.006633     |
| `warm-long-round` | 0.445491    | 0.439882 | 0.447131 | 344.5           | 5       | 0        | +0.008855     |
| `warm-start`      | 0.445492    | 0.439971 | 0.447154 | 353.5           | 5       | 0        | +0.008855     |

**Verdict:** mayfly loses to halton by 0.006663 (lower is better).

### Convergence (`attack`)

Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.

| Config            | 10%      | 25%      | 50%      | 75%      | 100%     |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.446090 | 0.444052 | 0.443643 | 0.443327 | 0.443286 |
| `random`          | 0.446201 | 0.445940 | 0.445212 | 0.445212 | 0.445212 |
| `halton`          | 0.446437 | 0.444807 | 0.439828 | 0.436636 | 0.436636 |
| `long-round`      | 0.446090 | 0.444052 | 0.443643 | 0.443327 | 0.443270 |
| `warm-long-round` | 0.446253 | 0.445961 | 0.445805 | 0.443951 | 0.442040 |
| `warm-start`      | 0.446253 | 0.445961 | 0.445805 | 0.443951 | 0.442484 |

### Proposed-score distribution (`attack`)

Median over seeds of each run's own trace quantiles, over every score the run proposed (not just the one it kept). Penalty scores, which report the budget rather than the landscape, are excluded.

| Config            | min      | p05      | median   | p95      | IQR      |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.443286 | 0.443479 | 0.446987 | 0.497350 | 0.004077 |
| `random`          | 0.445212 | 0.447392 | 0.474663 | 0.663103 | 0.061178 |
| `halton`          | 0.436636 | 0.447251 | 0.472880 | 0.659976 | 0.062639 |
| `long-round`      | 0.443270 | 0.443406 | 0.446905 | 0.470894 | 0.003736 |
| `warm-long-round` | 0.442040 | 0.443698 | 0.446801 | 0.471202 | 0.002505 |
| `warm-start`      | 0.442484 | 0.444648 | 0.446968 | 0.498094 | 0.003416 |

## Case `piano-mix`

`--pass none --optimize piano,mix`, 20 knobs

| Config            | median best | min      | max      | median wall (s) | n seeds | failures | Δ vs `halton` |
| ----------------- | ----------- | -------- | -------- | --------------- | ------- | -------- | ------------- |
| `baseline`        | 0.537056    | 0.498957 | 0.547363 | 371.6           | 5       | 0        | -0.024380     |
| `random`          | 0.569002    | 0.562463 | 0.572820 | 251.7           | 5       | 0        | +0.007566     |
| `halton`          | 0.561436    | 0.561436 | 0.561436 | 242.1           | 5       | 0        | +0.000000     |
| `long-round`      | 0.536995    | 0.498957 | 0.545792 | 369.9           | 5       | 0        | -0.024441     |
| `warm-long-round` | 0.537903    | 0.505777 | 0.540809 | 406.6           | 5       | 0        | -0.023533     |
| `warm-start`      | 0.538285    | 0.506188 | 0.542004 | 370.4           | 5       | 0        | -0.023152     |

**Verdict:** mayfly beats halton by 0.024380 (lower is better).

### Convergence (`piano-mix`)

Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.

| Config            | 10%      | 25%      | 50%      | 75%      | 100%     |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.572820 | 0.554986 | 0.541628 | 0.538024 | 0.537056 |
| `random`          | 0.572820 | 0.572160 | 0.569002 | 0.569002 | 0.569002 |
| `halton`          | 0.584307 | 0.584307 | 0.561974 | 0.561436 | 0.561436 |
| `long-round`      | 0.572820 | 0.554986 | 0.541628 | 0.538024 | 0.536695 |
| `warm-long-round` | 0.572820 | 0.554270 | 0.542762 | 0.539236 | 0.537903 |
| `warm-start`      | 0.572820 | 0.554270 | 0.542762 | 0.539236 | 0.538285 |

### Proposed-score distribution (`piano-mix`)

Median over seeds of each run's own trace quantiles, over every score the run proposed (not just the one it kept). Penalty scores, which report the budget rather than the landscape, are excluded.

| Config            | min      | p05      | median   | p95      | IQR      |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.537056 | 0.538603 | 0.562430 | 0.663642 | 0.034953 |
| `random`          | 0.569002 | 0.603378 | 0.797089 | 0.903363 | 0.171928 |
| `halton`          | 0.561436 | 0.598630 | 0.806961 | 0.904423 | 0.174911 |
| `long-round`      | 0.536695 | 0.538315 | 0.560641 | 0.650809 | 0.031350 |
| `warm-long-round` | 0.537903 | 0.540213 | 0.556906 | 0.645474 | 0.027244 |
| `warm-start`      | 0.538285 | 0.540495 | 0.558698 | 0.658858 | 0.029523 |

## Case `joint-ir`

`--optimize piano,body-ir,room-ir,mix`, 30+ knobs

| Config            | median best | min      | max      | median wall (s) | n seeds | failures | Δ vs `halton` |
| ----------------- | ----------- | -------- | -------- | --------------- | ------- | -------- | ------------- |
| `baseline`        | 0.512916    | 0.495244 | 0.534487 | 3520.7          | 5       | 0        | -0.035366     |
| `random`          | 0.557177    | 0.525975 | 0.571672 | 4744.0          | 5       | 0        | +0.008894     |
| `halton`          | 0.548282    | 0.548282 | 0.548282 | 1573.6          | 5       | 0        | +0.000000     |
| `long-round`      | 0.512916    | 0.493368 | 0.529214 | 3181.6          | 5       | 0        | -0.035366     |
| `warm-long-round` | 0.494332    | 0.485851 | 0.497758 | 3958.3          | 5       | 0        | -0.053950     |
| `warm-start`      | 0.494413    | 0.485851 | 0.497758 | 5531.7          | 5       | 0        | -0.053869     |

**Verdict:** mayfly beats halton by 0.035366 (lower is better).

### Convergence (`joint-ir`)

Median over seeds of the incumbent `best` in the `--trace` JSONL, at fractions of the eval budget.

| Config            | 10%      | 25%      | 50%      | 75%      | 100%     |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.589235 | 0.547185 | 0.524697 | 0.515603 | 0.512914 |
| `random`          | 0.587685 | 0.564879 | 0.557974 | 0.557178 | 0.557178 |
| `halton`          | 0.566701 | 0.566701 | 0.563704 | 0.548282 | 0.548282 |
| `long-round`      | 0.589235 | 0.547185 | 0.524697 | 0.515603 | 0.512914 |
| `warm-long-round` | 0.577639 | 0.518224 | 0.506074 | 0.496260 | 0.494332 |
| `warm-start`      | 0.577639 | 0.518224 | 0.506074 | 0.496260 | 0.494413 |

### Proposed-score distribution (`joint-ir`)

Median over seeds of each run's own trace quantiles, over every score the run proposed (not just the one it kept). Penalty scores, which report the budget rather than the landscape, are excluded.

| Config            | min      | p05      | median   | p95      | IQR      |
| ----------------- | -------- | -------- | -------- | -------- | -------- |
| `baseline`        | 0.512914 | 0.518686 | 0.552306 | 0.660620 | 0.054986 |
| `random`          | 0.557178 | 0.613377 | 0.809929 | 0.916178 | 0.155315 |
| `halton`          | 0.548282 | 0.598829 | 0.807230 | 0.914884 | 0.153343 |
| `long-round`      | 0.512914 | 0.517956 | 0.550571 | 0.657790 | 0.056008 |
| `warm-long-round` | 0.494332 | 0.496802 | 0.542225 | 0.658043 | 0.050792 |
| `warm-start`      | 0.494413 | 0.498118 | 0.545455 | 0.668396 | 0.052055 |

## Method

- **Score direction:** lower is better. `cmd/piano-fit` minimizes the aggregate objective; `best_score` in each `result.json` is that minimum.
- **Budget:** `--max-evals 600` per run, with `--time-budget 86400` pinned wide open. piano-fit enforces a wall-clock deadline unconditionally and defaults it to 120s, so the deadline has to be disabled explicitly for runs to be comparable by evaluation count rather than by machine speed and load.
- **Seeds:** 1, 2, 3, 4, 5 (one independent piano-fit process each; all statistics are medians over these, never a mean of one repeated run).
- **piano-fit workers:** `--workers 1`. One worker keeps a run a single search rather than a portfolio of independent Mayfly rounds.
- **Driver parallelism:** 6 concurrent runs in the most recent driver invocation. A tree assembled over several invocations may have used other values; parallelism affects wall time only, never the eval-budgeted scores.
- **Binary:** `go build -tags asm -buildvcs=false -o <work>/piano-fit ./cmd/piano-fit`.
- **Reference:** `reference/c4.wav`.
- **Base preset:** `assets/presets/default.json`.
- **Comparability caveat:** scores are only comparable within one scoring profile, and each `--pass` selects its own profile. Compare configs within a case, never scores across cases.
