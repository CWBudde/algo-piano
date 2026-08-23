# Optimizer audit: is the Mayfly search doing its job?

`PLAN.md` 11.6 wants the whole fitting pipeline re-run once the model fixes land.
Before spending that time we need to know whether `cmd/piano-fit`'s Mayfly search
is a competent optimizer or an expensive random sampler — otherwise 11.6 would
attribute optimizer failure to the model. **Nothing in this audit tries to
improve the piano sound.** The deliverable is evidence about the search.

The audit starts from one damning data point already in the repo: six 300 s
Mayfly runs lost to the deterministic Halton sweep in `sweep.go`. That was
recorded in prose and never followed up.

Two documents hold the measurements, and both are generated rather than
transcribed:

- [`optimizer-screening.md`](optimizer-screening.md) — Stage 0, render-free
  screening over closed-form objectives (`just opt-screen`).
- [`optimizer-benchmark.md`](optimizer-benchmark.md) — Stages A and B, the real
  audio matrix (`just opt-bench` then `just opt-bench-report`).

## The four suspected mechanisms

1. **The per-round evaluation budget is wrong.** `roundIterations` derives
   `iters = roundEvals / (2*pop)`, which assumes a round costs `NPop + NPopF`
   evaluations per iteration. The library also evaluates every crossover
   offspring and every mutant, and DESMA evaluates its elites on top.
2. **Rounds are far too short for the algorithm's own annealing schedule.**
   Twelve iterations against the library's `MaxIterations: 2000` default. With
   `DanceDamp 0.8` the nuptial dance is at `0.8^12 ≈ 0.07` by the end of a round
   that is then discarded.
3. **The incumbent is never seeded into the swarm.** `mayfly.Optimize` takes no
   run options, so `WithInitialPopulation` was never reachable. Every round
   restarts from a uniformly random population; `--preset` chaining, `--resume`
   and cross-round progress survive only in the external best tracker and the
   polish stage.
4. **Two conditioning problems that would masquerade as optimizer weakness.**
   `per_note.*.loss` is linear over `[0.985, 0.99995]` although decay time goes
   as `1/(1-loss)`, and under `legacy-v1` the spectral component is saturated
   across the whole preset population, so the term meant to dominate the score
   carries almost no gradient.

## Instrumentation

Every flag added for the audit defaults to the tool's prior behaviour, so
existing recipes and every tracked report reproduce bit for bit.
`TestSearchOptionsZeroValueIsTodaysBehaviour` pins that invariant, and it was
also checked end to end: binaries built from `main` and from this branch, run at
`--pass sustain --seed 1 --max-evals 300 --workers 1 --resume=false`, produce the
same `best_score`, `best_knobs` and `best_metrics`.

| flag                                                                              | purpose                                                                                             |
| --------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `--search mayfly\|random\|halton`                                                 | control conditions, sharing the objective, budget accounting and best-tracking with the real search |
| `--trace <path>`                                                                  | JSONL, one record per evaluation, for convergence curves                                            |
| `--mayfly-iters <n>`                                                              | round length set directly instead of derived from the wrong evaluation model                        |
| `--mayfly-warm-start`                                                             | seeds one male and one female at the incumbent                                                      |
| `--mayfly-stagnation <n>`                                                         | ends a round when it stops improving, via the library's own `ConvergenceConfig`                     |
| `--mayfly-nc-ratio`, `--mayfly-dance-damp`, `--mayfly-fl-damp`, `--mayfly-g-damp` | the settings the audit needed to vary                                                               |

## Finding 1, confirmed: a round costs 47.7 evaluations per iteration, not 20

Measured, not derived — `mayfly.Result.FuncEvalCount` and `IterationCount` are
now read instead of discarded. At the tool's own defaults (`--mayfly-pop 10`,
DESMA) the median is **47.7 evaluations per iteration against the 20 the
derivation assumes, a factor of 2.38**. The measurement scales with population
exactly as the unaccounted operators predict: 92.7 at `pop 20` and 193.7 at
`pop 40`, and dropping the crossover offspring count to `NC = pop` brings it
down to 37.9.

The consequence is not a slow round; it is a round that never finishes. The
budget runs out roughly 5 iterations into a 12-iteration round, `reserveEval`
poisons the objective, and the partial round is discarded.

`TestMeasuredEvalsPerIterationExceedsTheDerivation` asserts this so it cannot
regress silently.

## Stage 0 results (synthetic, 5/9/20/30 dimensions, 20 seeds)

Full tables in [`optimizer-screening.md`](optimizer-screening.md). The
substance:

**The search does beat the controls, decisively.** On every objective and every
dimensionality, stock Mayfly lands one to three orders of magnitude below both
random and Halton at the same evaluation budget — e.g. sphere at 5 dimensions,
`0.0029` against Halton's `2.05`. Whatever went wrong in the recorded
piano-fit-versus-sweep comparison, "a metaheuristic that cannot beat a
low-discrepancy sequence" is not the explanation.

**Round length is the dominant setting, by a wide margin.** Raising
`--mayfly-iters` from the derived 12 improves every cell of every table:

| objective  | dimensions |  stock | long round |
| ---------- | ---------: | -----: | ---------: |
| sphere     |          5 | 0.0029 |  0.0000010 |
| sphere     |         30 |   15.9 |        3.4 |
| rastrigin  |         20 |  122.9 |       74.0 |
| rosenbrock |         30 |  553.2 |      251.8 |

At the 1500-evaluation budget `--mayfly-iters 40`, `120` and "never restart"
give _identical_ results, which is finding 1 seen from the other side: the round
is always ended by the budget, never by its iteration count. Repeating the sweep
at 8000 evaluations — where 40 iterations really does mean several restarts —
keeps the ordering and widens the gap (rastrigin at 30 dimensions: 197.8 stock,
101.7 at `iters 40`, 78.2 never restarting), so this is not an artifact of a
small budget. Restarting the search is costing more than it buys.

**Warm start is the second-largest effect**, and it is largely independent of
the first: rastrigin at 30 dimensions goes 197.8 → 78.9 on warm start alone at
the 8000-evaluation budget, and the combination is at or near the best cell in
almost every column.

**Three settings turned out not to matter**, which is worth recording so they
are not revisited:

- `--mayfly-stagnation 15` is _exactly_ baseline in every cell. Rounds are only
  12 iterations long, so a 15-iteration stagnation window can never fire.
- `--mayfly-dance-damp` at 0.95 and 0.99 is within seed noise of 0.8.
- The variant choice barely moves the result. `olce` is marginally ahead of the
  shipped `desma` on the smoother objectives and `aoblmoa` is far behind
  everything, but no variant is worth a default change on this evidence.

Larger populations are clearly _worse_ at these budgets (`pop 40` is 4-8× worse
than `pop 10` on sphere), which is the same finding again: a bigger population
costs proportionally more evaluations per iteration, so it buys fewer
iterations.

A synthetic landscape is not the piano landscape, and none of this is a
conclusion about the fitting pipeline. Its job was to cut the configuration
matrix down to what is worth spending real renders on: **warm start, round
length, and their combination.**

## Stages A and B — real audio

Generated tables: [`optimizer-benchmark.md`](optimizer-benchmark.md) at 600
evaluations across all four cases, and
[`optimizer-benchmark-2400.md`](optimizer-benchmark-2400.md) at 2400
evaluations on `sustain` and `attack`.

The second document exists because of a mistake worth recording. The first
matrix was budgeted at 600 evaluations, chosen to match what a real
`just fit-c4-passes` run actually spends. But a round is about 47.7 evaluations
per iteration and the derived round length is 12 iterations, so 600 evaluations
is _one round_. At that budget "restart every 12 iterations" and "never
restart" are the same configuration — and three of five `sustain` seeds
returned bit-identical scores under both, which is how it was noticed. The
round-length question needs a budget several rounds long, hence the 2400-eval
re-run.

### Does the search beat the controls?

Medians of five seeds at 600 evaluations, lower is better:

| case        | knobs | `baseline` | `warm-start` |  `halton` | `random` |
| ----------- | ----: | ---------: | -----------: | --------: | -------: |
| `sustain`   |     5 |      0.354 |    **0.346** |     0.412 |    0.441 |
| `attack`    |     9 |      0.443 |        0.446 | **0.437** |    0.445 |
| `piano-mix` |    20 |  **0.537** |        0.538 |     0.561 |    0.569 |
| `joint-ir`  |    39 |      0.513 |    **0.494** |     0.548 |    0.557 |

**On three of four cases the search earns its cost comfortably**, and on
`joint-ir` warm start beats Halton by 0.054. `attack` is the exception, and it
is the case that matters most for interpreting the record in `PLAN.md:1016`:
there, Halton beats every Mayfly configuration tried.

The controls behaved as controls should. Halton returned bit-identical scores
across all five seeds of every case, which is the determinism check passing.

### Why `attack` is different

Not because it is high-dimensional — the search wins at both 5 and 20 knobs and
loses at 9. The proposed-score distributions say what actually happens:

| `attack`, 600 evals |    min | median |   p95 |        IQR |
| ------------------- | -----: | -----: | ----: | ---------: |
| `halton`            | 0.4366 | 0.4729 | 0.660 | **0.0626** |
| `random`            | 0.4452 | 0.4747 | 0.663 | **0.0612** |
| `baseline`          | 0.4433 | 0.4470 | 0.497 | **0.0041** |
| `warm-start`        | 0.4425 | 0.4470 | 0.498 | **0.0034** |

The samplers, which probe the box evenly, see an interquartile range of ~0.062.
Every Mayfly configuration sees ~0.004. The convergence table shows the swarm is
already inside that band at 10% of the budget and never leaves it, while Halton
is still improving at the halfway mark. That is premature convergence: with
`DanceDamp 0.8` over ~12 iterations the nuptial dance is at `0.8^12 ≈ 0.07`, and
at a one-round budget no restart ever restores diversity.

The 2400-evaluation re-run adds the other half of the picture. **Halton on
`attack` returns exactly 0.436636 at 2400 evaluations — the identical value it
found at 600.** Four times the budget bought a space-filling sequence nothing at
all. So `attack` is a nearly flat landscape with one good pocket that Halton
happens to cover early and the swarm never reaches.

That also disposes of the equal-wall-clock objection. Mayfly runs cost ~45% more
wall time than the samplers at the same evaluation count, because the candidates
it favours render longer, so at equal wall clock Halton would get ~1.45× the
evaluations. On `sustain`, quadrupling Halton's budget moves it from 0.4118 to
0.4096 — 0.002 against a 0.06 gap. Extra evaluations are not what the control is
missing.

### Round length and warm start on real audio

At 2400 evaluations, where restart policy is finally expressible:

| case      | `baseline` | `long-round` | `warm-long-round` | `halton` |
| --------- | ---------: | -----------: | ----------------: | -------: |
| `sustain` |     0.3438 |   **0.3386** |            0.3381 |   0.4096 |
| `attack`  | **0.4399** |       0.4413 |            0.4438 |   0.4366 |

Round length helps on `sustain` and slightly hurts on `attack`. Warm start at
600 evaluations wins clearly on `sustain` (0.346 vs 0.354) and `joint-ir`
(0.494 vs 0.513), and loses slightly on `attack` (0.446 vs 0.443) and
`piano-mix` (0.538 vs 0.537).

**Neither change clears the promotion rule** — beat baseline on at least three
of four cases and regress none — so **no default changes.** Both stay behind
their flags with the measurement recorded as the reason. This is the rule doing
its job: on the synthetic objectives both looked like large, unambiguous wins,
and on real audio they are case-dependent.

### Finding 4b, confirmed on every case

`spectral_saturated` is true for the winning candidate of every single case, and
`piano-mix` additionally saturates `partial_level`:

| case        | search profile | spectral RMSE | that profile's norm | saturated weight |
| ----------- | -------------- | ------------: | ------------------: | ---------------: |
| `sustain`   | `decay-v1`     |       81.8 dB |                80.0 |              10% |
| `attack`    | `attack-v1`    |       82.0 dB |                80.0 |              20% |
| `piano-mix` | `legacy-v1`    |       68.2 dB |                30.0 |              30% |

So between a tenth and a third of the objective's weight carries no gradient at
the current model quality. `just gate-c4` passes, but its own breakdown of the
gated preset states the problem plainly:

```
Time RMSE        0.106971      42.8%  x0.30   -> 0.1284
Envelope RMSE    10.9 dB       36.2%  x0.25   -> 0.0905
Spectral RMSE    77.4 dB      100.0%  x0.30   -> 0.3000  <-- pinned
Decay diff       3.9 dB/s       9.8%  x0.15   -> 0.0146
Score:            0.5335
WARNING: spectral component saturated (77.4 dB >= NormSpectral 30.0) - this component provides no gradient
```

The largest single contributor to the score, 0.3000 of 0.5335 — 56% of it — is
a constant. An optimizer moving any knob that affects spectral RMSE sees no
change in the objective at all.

The shape matters: on the calibrated profiles the norm is exceeded by two
decibels, not by an order of magnitude. This is model distance, not a
normalization bug — `analysis/norms.go` already says as much about the
attack-rise metric — and **it should be re-checked after the model work rather
than "fixed" now.** Changing `analysis` norms is out of scope for this branch.

## Verdict for PLAN.md 11.6

**The optimizer is fit to run 11.6, and it is not the thing to fix first.**

At an equal evaluation budget the search beats both trivial controls on three of
four cases, decisively on the widest one. The one case it loses is a nearly flat
landscape where 2400 Halton evaluations find nothing better than 600 do, so no
optimizer would have distinguished itself there.

What should be checked before 11.6 is the objective, not the search. A fifth of
the `attack` profile's weight and a third of `legacy-v1`'s is pinned at the
clamp, which is a consequence of the model still sitting far from the reference.
Re-measure saturation once the model fixes land; if the spectral term comes back
inside its norm, the search has more signal to work with than anything measured
here.

Two smaller items are worth carrying forward:

- The evaluation accounting is wrong by 2.38×, so `--mayfly-round-evals` does
  not mean what it says and rounds are truncated. Fixing the derivation is a
  one-line change, but it changes the search's behaviour, so it belongs to a
  branch that can re-run the matrix rather than this one.
- `--mayfly-warm-start` is worth trying on the joint IR fit specifically, where
  it was the largest single improvement measured (0.513 → 0.494).
