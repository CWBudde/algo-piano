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

## Stages A and B

Pending — see [`optimizer-benchmark.md`](optimizer-benchmark.md).
