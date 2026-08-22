# Optimization Workflow: Systematic Piano Fitting

## Goal

Get progressively closer to a reference recording without regressions, using alternating optimization stages with different knob groups via the unified `piano-fit` tool.

## Architecture Overview

The `piano-fit` tool optimizes different parameter groups selected via `--optimize`:

| Groups                      | What it optimizes                                                     | IR handling                           |
| --------------------------- | --------------------------------------------------------------------- | ------------------------------------- |
| `piano,mix`                 | Piano synthesis knobs (hammer, unison, per-note, output gain, IR mix) | Fixed IR loaded from preset           |
| `body-ir,mix`               | Body IR synthesis knobs + mix levels                                  | Generates mono body IR per eval       |
| `body-ir,room-ir,mix`       | Body + room IR synthesis knobs + mix                                  | Generates body + room IRs per eval    |
| `piano,body-ir,room-ir,mix` | All knobs jointly                                                     | Generates IRs + optimizes piano knobs |

The idea: alternate between piano-only and IR stages so each builds on the previous best result.

### Key flags

- `--no-resonance`: Disables the resonance engine during optimization. Use for stages 1-3 to avoid the CPU cost of sympathetic resonance (27x speedup). Only enable resonance for final polish stages.
  It is a fitting-speed knob, not a model change: the written preset keeps the
  input preset's own `resonance_enabled`, so a staged pipeline never hands the
  next stage a preset with resonance permanently switched off. `cmd/piano-modal-fit`
  accepts the same flag with the same semantics — there it silences resonance on
  both sides of the match, the DWG references and the modal candidates alike.
- `--cpuprofile <file>`: Write CPU profile for performance analysis.
- `--sweep`: Replaces the optimizer with a deterministic sensitivity + Pareto
  sweep over whatever knobs the current `--pass`/`--optimize` selection leaves
  active. It renders, measures, writes one JSON report and exits — no preset, no
  run report, no checkpoint, and no RNG anywhere (a fixed inclusive grid plus a
  Halton fill), so the report is reproducible from the flags alone.
- `--sweep-samples` / `--sweep-joint-evals` / `--sweep-joint-skip` /
  `--sweep-joint-max-dims` / `--sweep-profiles` / `--sweep-out`: inert unless
  `--sweep` is set. Every sample is scored under **all** requested profiles from
  one render, which is what makes a trade-off between two profiles measurable.
- In `--sweep` mode `--time-budget` and `--resume` are deliberately **ignored**
  (each prints a notice): a wall-clock cutoff would make the report
  irreproducible, and a stale checkpoint would silently move the baseline off
  `--preset`. For the same reason the optimizer-only flag validation is skipped
  in sweep mode: `--output-ir`, `--output-preset`, `--max-evals` and a positive
  `--time-budget` are not required, so `--sweep --optimize body-ir` runs without
  a dummy output IR and `--time-budget 0` is accepted rather than rejected.
- `constrained_best` is the lowest-primary sample that **strictly improves** the
  primary profile over the baseline **and** scores the secondary profile no
  worse than the baseline. Both halves are required: without the primary test a
  run in which every non-regressing sample is worse on the primary objective
  would still report a `constrained_best`, which reads as "a non-regressing
  improvement region exists". `constrained_count` is the plainer number of
  sampled points that hold the secondary line, improvement or not.

## Workflow

### Stage 0: Reset to Clean State

Before starting (or restarting) the optimization pipeline, clear any stale checkpoints and output files. This is necessary whenever the model code or knob definitions have changed, because:

- **Report files contain knob snapshots**: The `*.report.json` files store the optimizer's best knob values by name. If knobs were added, removed, or renamed, a resumed run would start from an incomplete or mismatched state.
- **Preset files reflect old parameters**: Stage output presets may lack newly added fields (e.g. `attack_noise_level`, `high_freq_damping`) or contain values calibrated for an older model version. Feeding them forward would propagate stale settings.
- **The optimizer uses `--resume=true` by default**: Without clearing reports, `piano-fit` silently resumes from the old checkpoint rather than exploring the new parameter space.

```bash
# Remove all stage outputs and checkpoints
rm -f out/stages/stage*.json out/stages/stage*.report.json
rm -f out/stages/stage*-ir-body.wav out/stages/stage*-ir-room.wav
rm -f out/stages/final.json

# Verify clean state
ls out/stages/
# Should be empty (or contain only non-stage files)
```

After clearing, proceed with Stage 1 using the default preset as the starting point.

```
Stage 1: --optimize=piano,mix         (piano knobs, default IR, no resonance)
    ↓ output preset
Stage 2: --optimize=body-ir,mix       (body IR, fixed piano from Stage 1, no resonance)
    ↓ output preset + body IR WAV
Stage 3: --optimize=piano,mix         (piano knobs, fixed body IR from Stage 2, no resonance)
    ↓ output preset (preserves body IR)
Stage 4: --optimize=piano,body-ir,room-ir,mix  (joint optimization)
    ↓ output preset + body IR WAV + room IR WAV
Stage 5: --optimize=piano,mix         (final polish with dual IR)
    ↓ final preset
```

Each stage's output preset becomes the next stage's input. The preset carries forward all previously optimized parameters.

### Stage 1: Initial Piano Fit

Optimize piano synthesis against reference with the default IR.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset assets/presets/default.json \
    --output-preset out/stages/stage1.json \
    --optimize piano,mix \
    --no-resonance \
    --note 60 \
    --time-budget 600 \
    --max-evals 5000 \
    --workers auto
```

**What this does:** Finds best hammer parameters, per-note tuning (loss, inharmonicity, strike position), output gain, and IR mix levels using the default IR. Resonance is disabled for speed.

### Stage 2: Body IR Fit

Optimize a short mono body IR using the piano knobs from Stage 1.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage1.json \
    --output-preset out/stages/stage2.json \
    --output-ir out/stages/stage2-ir.wav \
    --optimize body-ir,mix \
    --no-resonance \
    --note 60 \
    --time-budget 600 \
    --max-evals 2000 \
    --workers auto \
    --resume=false
```

**What this does:** Synthesizes body coloration IR (short, mono) while keeping piano knobs fixed from Stage 1.

### Stage 3: Refine Piano with Body IR

Re-optimize piano knobs now that we have a better body IR.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage2.json \
    --output-preset out/stages/stage3.json \
    --optimize piano,mix \
    --no-resonance \
    --note 60 \
    --time-budget 600 \
    --max-evals 5000 \
    --workers auto \
    --resume=false
```

**What this does:** The body IR changes the tonal character, so piano knobs that were optimal with the default IR may no longer be optimal. This stage re-tunes them.

### Stage 4: Joint IR + Piano Refinement

Joint optimization of both IR synthesis and piano parameters.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage3.json \
    --output-preset out/stages/stage4.json \
    --output-ir out/stages/stage4-ir.wav \
    --optimize piano,body-ir,room-ir,mix \
    --note 60 \
    --time-budget 600 \
    --max-evals 3000 \
    --workers auto \
    --resume=false
```

**What this does:** Jointly optimizes IR synthesis AND piano knobs (30 knobs total). This can find interactions between IR shape and piano parameters that alternating stages miss. Give it more time since the search space is larger.

### Stage 5: Final Polish

One more piano-only pass to fine-tune with the joint-optimized IR.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage4.json \
    --output-preset out/stages/final.json \
    --optimize piano,mix \
    --note 60 \
    --time-budget 300 \
    --max-evals 5000 \
    --workers auto \
    --resume=false
```

## Preventing Regressions

### The automatic gate: `just gate-c4`

`cmd/piano-distance --thresholds <file>` compares a rendered candidate against
the reference and checks the resulting metrics against a calibrated threshold
file. It exits `0` on pass and `2` on breach, so it works as a CI/pre-commit
gate. `just gate-c4` wraps it with the canonical C4 render settings:

```bash
just gate-c4
# gate: PASS 5 enforced metrics within thresholds
# gate: worst headroom decay_diff_db_per_s 3.18/3.45 (92% of budget used)
```

On a breach it names the metric and the overshoot on stderr:

```
gate: FAIL time_rmse=0.13 > max 0.12 (+8.9%)
gate: 1 of 5 enforced metrics breached
```

`gate-c4` is part of the `ci` recipe. It is deliberately **not** part of
`.github/workflows/ci.yml`: `reference/c4.wav` is gitignored (`.gitignore` has a
blanket `*.wav` with only `!assets/ir/*.wav`), so CI has no reference to gate
against. For the same reason the recipe **skips with exit 0** when the reference
file is missing, which keeps `just ci` green on a fresh clone.

The headroom line is the part that earns its keep on a _passing_ run: a metric
drifting from 70% to 97% of budget is a regression in progress, and you want to
see it before it trips.

### The threshold file

`assets/thresholds/c4.json` holds the caps under a `max` block plus a `recorded`
block documenting when, against what, and with which render settings the values
were measured.

- A **number** is enforced.
- **`null`** means _not yet calibrated_: the metric is listed so its existence is
  visible, but it is not enforced. Calibrating one later is a one-line diff, not
  a new file.
- An **unknown key is an error**. The gate resolves metric names by reflecting
  over the JSON tags of `analysis.Metrics`, so any new `float64` metric becomes
  gateable with no code change — and a typo cannot silently disable a cap.
  `TestMetricsByJSONTagCoversAllFloatFields` keeps that mapping complete.

Currently enforced: `score`, `time_rmse`, `envelope_rmse_db`,
`spectral_rmse_db`, `decay_diff_db_per_s`. The six newer metrics
(`partial_level_rmse_db`, `partial_freq_rmse_cents`, `tristimulus_distance`,
`attack_rise_diff_ms`, `attack_centroid_rmse_oct`,
`decay_segment_rmse_db_per_s`) are present as `null` pending calibration.

Thresholds sit at roughly 8-10% above the values measured on 2026-08-21. They
are a _fence_, not a target: tighten them whenever a fit genuinely improves.

> **Caveat on `spectral_rmse_db`.** The gate checks the _raw dB_ value, which is
> a real regression signal. The _normalised_ spectral component is not: at
> ~52.9 dB against `analysis.NormSpectral = 30.0` it saturates, `clamp01` pins it
> at 1.0, and it therefore contributes a constant to `score` with **no gradient
> for the optimizer**. Every preset in the repo saturates it (measured
> population range 47.8-68.6 dB on 2026-08-21).
>
> This is deliberate for `legacy-v1` and only for `legacy-v1`: the frozen norms
> are what make every recorded number comparable. The profiles that actually
> steer the optimizer — `balanced-v2`, `attack-v1`, `decay-v1`,
> `inharmonicity-v1` — use `analysis.CalibratedNorms()` instead, which raises
> `Spectral` to 80.0 along with `Decay`, `PartialLevel`, `PartialFreq`,
> `AttackRise` and `AttackCentroid`. So the gate saturates and the search does
> not.

> **Recorded scores are comparable only within a renderer/metric generation.**
> `analysis/distance.go` changed six times between 2026-02-14 and 2026-08-16
> (phase detection, normalisation), so numbers written into older
> `*.report.json` files were produced by a different metric implementation.
> Separately, the DWG treble-collapse fix (#14) changed every render, so any
> Phase 8B number measured before it describes a different synthesizer.
> Re-measure rather than compare across either boundary.

### Per-stage manual check

To compare stages against each other by hand:

```bash
for stage in stage1 stage2 stage3 stage4 final; do
    just gate-c4 "preset=out/stages/${stage}.json"
done
```

If a stage regresses, discard it and re-run with a different seed or a longer
budget. The previous stage's output is always intact.

### Why regressions can happen

1. **Piano-only after IR change:** The new IR changes what's "optimal" for piano knobs. The optimizer explores from defaults, not from the previous best in the old IR space. The fix is ensuring `--preset` carries forward the previous piano knobs as starting points.

2. **Joint optimization (all groups):** Joint optimization has 30 dimensions. With limited budget, it may not find solutions as good as the separated stages. Use longer budgets for joint runs.

3. **Different mix knob semantics:** Legacy (`ir_wet_mix`) vs dual-IR (`body_dry`, `room_wet`) use different mix logic. A preset switching between formats can produce different audio even with "equivalent" values.

## The Polish Stage

`--polish` runs a deterministic coordinate-descent sweep after the Mayfly
search. `--polish-only` skips the search entirely and runs only the sweep;
paired with `--resume` it is the standard **finishing move** on an existing
best:

```bash
go run -tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/final.json \
    --output-preset out/stages/final.json \
    --polish-only --resume \
    --polish-evals 200
```

Properties worth relying on:

- **It cannot regress.** A coordinate step is accepted only when it improves the
  score, so the output is never worse than the input.
- **It is deterministic.** Same input, same knobs, same result — unlike the
  seeded-but-stochastic Mayfly rounds.
- **It has a hard eval budget** (`--polish-evals`, default 200), so it costs a
  bounded amount of wall time.

Tuning knobs: `--polish-knobs` (default
`render.velocity,render.release_after,hammer_initial_velocity_scale`),
`--polish-rounds`, `--polish-step`, `--polish-shrink`, `--polish-min-step`. When
none of the default polish knobs are active for the current `--optimize`/`--pass`
selection, polish disables itself with a message rather than erroring.

`output_gain` is deliberately absent from the default polish knobs — see below.

### Output gain is solved, not searched

`analysis.Compare` RMS-normalises both signals before scoring, which makes
`output_gain` provably **score-invariant**: searching it burns budget on a
perfectly flat dimension. `--match-output-gain` (default on) therefore drops the
knob from the searched dimensions entirely and solves it in closed form after
the search, so the written preset lands at the right absolute level without ever
having been optimized for it. Because it is no longer a knob, the match is also
free of the `[0.01, 5.0]` search bounds and can reach whatever level the
reference actually needs.

Pass `--match-output-gain=false` to get the old behaviour: `output_gain` returns
to the knob set and is searched like any other dimension. Reports written in
either mode resume into the other, since resume matches knobs by name.

## Per-Aspect Passes

A pass does two things: it restricts which knobs may move, on top of whatever
`--optimize` selected, and it scores with the weighting profile that describes
the aspect being fitted. Everything else keeps its value from the input preset.
`--pass-window start:end` (seconds) additionally narrows the comparison to a
time window.

Both halves are needed. Restricting the knobs without changing the objective
would let the optimizer spend the attack knobs on whatever the full-signal
legacy score happens to reward; changing the objective without restricting the
knobs would let it reach the same target through parameters that belong to a
different aspect.

| Pass            | Knobs it may move                                                                                           | Profile            | Typical window          |
| --------------- | ----------------------------------------------------------------------------------------------------------- | ------------------ | ----------------------- |
| `none`          | everything `--optimize` selected                                                                            | `legacy-v1`        | whole signal            |
| `attack`        | `hammer_*_scale`, `attack_noise_level`, `attack_noise_duration_ms`, `attack_noise_color`, `render.velocity` | `attack-v1`        | `--pass-window 0:0.35`  |
| `sustain`       | `per_note.*.loss`, `high_freq_damping`, `unison_detune_scale`, `unison_crossfeed`, `render.release_after`   | `decay-v1`         | whole signal            |
| `inharmonicity` | `per_note.*.inharmonicity`, `per_note.*.strike_position`, `unison_detune_scale`                             | `inharmonicity-v1` | `--pass-window 0.2:2.0` |

`--profile <name>` overrides the pass default and works with `--pass none` too,
so an unrestricted run can be scored with `balanced-v2`. An unknown name is an
error rather than a silent fallback. The profile a run used is recorded as
`score_profile` in its report; a report without that key is a `legacy-v1`
report, which is what every report written so far is.

The `sustain` pass deliberately gets **no** window: `decay-v1` weights the
segmented decay metric at 0.55 and its late segment measures out to 5 s, so
cutting the tail off would remove the very thing the pass is fitting.

`just fit-c4-passes` runs all three and finishes with `legacy-v1` distance
reports, the only numbers comparable to anything else. Its final artifact is
`attack` → `inharmonicity`; the `sustain` pass is measured but deliberately not
chained into it, for the reason in "Measured results" below. The individual
runs are:

```bash
# 1. Attack pass on the first 350 ms
go run -tags asm ./cmd/piano-fit \
    --preset out/stages/final.json --output-preset out/passes/attack.json \
    --pass attack --pass-window 0:0.35 --time-budget 180

# 2. Sustain pass, chained from the attack pass OUTPUT PRESET
go run -tags asm ./cmd/piano-fit \
    --preset out/passes/attack.json --output-preset out/passes/sustain.json \
    --pass sustain --time-budget 180 --resume=false
```

Two caveats that will bite otherwise:

- **Windowed pass scores are NOT comparable to full scores.** The window slices
  _both_ signals before `Compare`, so `trimLeadingSilence`, `normalizeRMS` and
  lag estimation all re-run inside the window. A pass score of 0.31 and a full
  score of 0.52 are measurements of different things. Judge a pass by re-running
  the full compare (`just gate-c4`) on its output preset, never by its own
  reported score.
- **Chain passes via `--preset`, not via `--resume`.** A pass run's report
  contains only _that pass's_ knobs under `best_knobs`. Resuming a later stage
  from a pass report alone would seed it with a partial knob set; feed the
  previous stage's **output preset** forward instead (and pass `--resume=false`).

### Measured results (2026-08-22, 180 s per pass, from `fitted-c4-mayfly.json`)

> **Stale baseline, kept for the relative comparison.** The `0.5330` in the
> table below is `fitted-c4-mayfly.json` on the pre-#26 renderer. That preset
> then moved to `0.5249` with the `noteResonator` normalisation (#26) and was
> re-fitted on 2026-08-22 to **`0.5182`** — see
> "[Deterministic sweep chain](#deterministic-sweep-chain-how-the-2026-08-22-c4-re-fit-was-done)"
> below. The pass numbers here stay useful as a _relative_ comparison between
> passes; do not quote them against the current gate.

Re-measured on the post-#14 renderer under `analysis.CalibratedNorms()`. Judged
the only honest way — by re-running the full `legacy-v1` compare on each pass's
output preset, so the column is comparable across passes and against the C4
baseline:

| Stage                            | legacy score | what its own profile did                |
| -------------------------------- | ------------ | --------------------------------------- |
| baseline (`fitted-c4-mayfly`)    | 0.5330       | —                                       |
| after `attack`                   | **0.5214**   | attack centroid 0.545 → 0.130 oct       |
| after `attack` → `inharmonicity` | 0.5234       | partial-freq RMSE unmoved at 36.6 cents |
| `sustain`, chained from `attack` | 0.5436       | decay diff 5.2 → 2.8 dB/s               |

The `attack` pass is still the only net win, and it is now a bigger one: at
0.5214 it beats the tracked C4 gate baseline and clears every threshold in
`assets/thresholds/c4.json`. That is an improvement over the fitting baseline,
not over the preset users receive — `cmd/piano-render` still defaults to
`assets/presets/default.json`.

**The `sustain` pass still regresses, and the earlier explanation was only half
right.** The old diagnosis was that `NormSpectral = 30.0` was saturated, so
`decay-v1` could not see a spectral degradation and had no restoring force
against it. `decay-v1` now uses `CalibratedNorms()` with `Spectral = 80.0`, that
blind spot is genuinely gone — and the pass makes the same trade anyway. It buys
decay diff (5.2 → 2.8 dB/s) and envelope (10.1 → 9.3 dB), and pays with time
RMSE (0.0978 → 0.1299), spectral (51.7 → 56.0 dB) and partial level (11.3 → 19.0
dB). The time RMSE alone breaches the gate's `0.112`, so the result could not
ship regardless of the score.

Treat that as an **observed trade-off under this search**, not as a proven
limitation of the string model. What the run actually establishes is narrower
than it first looks: one 180 s stochastic search, one seed, one strategy, over
the restricted sustain knob set, found no candidate that improves decay without
costing spectrum and time. A different budget, seed, search strategy or a wider
knob surface could still find one, so "the model cannot do it" is not yet
supported.

Keep the pass out of the chain on the evidence — but the sweep below shows the
trade-off is **not** a property of the sustain box. A non-regressing region
exists; the 180 s stochastic search simply did not land in it.

Run `inharmonicity` from the attack-pass preset rather than the sustain-pass
preset. Done that way it is very slightly negative (0.5214 → 0.5234) and leaves
partial-frequency RMSE at 36.6 cents. Its three knobs simply have too little
leverage at C4 — which is why the recipe's chained artifact is marginally worse
than the attack pass on its own.

### Sensitivity and Pareto sweep over the sustain knobs

```bash
just sweep-sustain-c4
# equivalently:
#   go run ./cmd/piano-fit --sweep --pass sustain \
#       --preset out/passes/attack.json --reference reference/c4.wav \
#       --sweep-out out/sweep/sustain-note60.json \
#       --sweep-samples 9 --sweep-joint-evals 2048 --workers auto \
#       --note 60 --velocity 118 --release-after 3.5 --sample-rate 48000 \
#       --decay-dbfs -90 --decay-hold-blocks 6 --min-duration 2.0 --max-duration 30
```

2093 evaluations in 199 s on a 12-core box (~10.5 evals/s): one baseline, a
9-point one-at-a-time scan of each of the five sustain knobs, and 2048 Halton
points filling the 5-D box (primes 2/3/5/7/11, index offset 64). Every sample is
rendered once at the **final** settings `just distance-c4` uses and then scored
twice, under `decay-v1` and under `legacy-v1`, so the `legacy-v1` column is
directly comparable to the table above.

**Baseline** (`out/passes/attack.json`, note 60, velocity 118, release-after
3.5 s): `decay-v1 = 0.4380`, `legacy-v1 = 0.5189`, time RMSE `0.0958`, envelope
`10.15` dB, spectral `51.95` dB, decay diff `5.16` dB/s. That `legacy-v1` is
byte-identical to what `just distance-c4` reports on the same preset, which is
the sweep's correctness check. (It is `0.5189`, not the `0.5214` quoted in the
table above: the attack pass is stochastic, and the `out/passes/attack.json`
this sweep ran against is a different run of the same recipe. The comparison
here is always sweep-baseline against sweep-sample, so nothing depends on which
attack run is on disk.)

| knob                   | range            | `decay-v1` span | `legacy-v1` span | argmin (`decay-v1`) | monotonic |
| ---------------------- | ---------------- | --------------- | ---------------- | ------------------- | --------- |
| `high_freq_damping`    | [0, 0.6]         | 0.0605          | 0.1212           | 0.225               | no        |
| `unison_detune_scale`  | [0, 2]           | 0.1455          | 0.0334           | 1.00                | no        |
| `unison_crossfeed`     | [0, 0.005]       | 0.0619          | 0.0506           | 0.004375            | no        |
| `per_note.60.loss`     | [0.985, 0.99995] | 0.4568          | 0.3012           | 0.994344            | no        |
| `render.release_after` | [0.2, 3.5]       | 0.5620          | 0.3268           | 3.5                 | yes       |

`per_note.60.loss` and `render.release_after` dominate both objectives — they
are the knobs that decide how much tail exists at all. `unison_detune_scale` is
the interesting one: it moves `decay-v1` four times as far as it moves
`legacy-v1`, which is exactly the shape a knob needs to buy decay cheaply.

Pareto front on (`decay-v1`, `legacy-v1`), both minimised — 7 points:

| index | `decay-v1` | `legacy-v1` | time RMSE | stage                     |
| ----- | ---------- | ----------- | --------- | ------------------------- |
| 419   | 0.3230     | 0.5521      | 0.1405    | joint                     |
| 14    | 0.3273     | 0.5264      | 0.0933    | OAT `unison_detune_scale` |
| 650   | 0.3298     | 0.5237      | 0.1146    | joint                     |
| 17    | 0.3508     | **0.5141**  | 0.0947    | OAT `unison_detune_scale` |
| 1244  | 0.3765     | 0.5123      | 0.0989    | joint                     |
| 26    | 0.4143     | 0.5003      | 0.0982    | OAT `unison_crossfeed`    |
| 34    | 0.4404     | 0.4979      | 0.1002    | OAT `per_note.60.loss`    |

**`constrained_best` = sample #17, `constrained_count` = 22 of 2092.** Twenty-two
sampled points score `legacy-v1` no worse than the baseline; the best of those
that also improve `decay-v1` is #17, which changes exactly **one** knob:

```
unison_detune_scale 0.7743 -> 1.75    (everything else at baseline)
decay-v1   0.4380 -> 0.3508
legacy-v1  0.5189 -> 0.5141
time RMSE  0.0958 -> 0.0947     (gate cap 0.112: clears it)
envelope   10.15  -> 9.77 dB
spectral   51.95  -> 49.02 dB
decay segment RMSE 14.55 -> 10.16 dB/s
```

Confirmed independently: writing that preset out and running `just distance-c4`
on it reports `0.5141`, the same number the sweep recorded. Twelve of the 22
qualifying points improve `decay-v1` **and** keep time RMSE under the gate's
`0.112`, so this is a region, not a single lucky sample.

**What this licenses, and what it does not.** Across 2048 low-discrepancy
samples of the 5-D sustain box plus a 45-point one-at-a-time scan, all at final
render settings, there exists a non-empty region that improves `decay-v1`
without regressing `legacy-v1` — so the sustain pass's regression is a property
of _that search_, not of the sustain knob set and not of the string model. It
does **not** say the region is large (22 of 2092 samples, ~1%), that the
improvement is big (`legacy-v1` moves by 0.005), or that a constrained re-fit
will find something better than #17. 2048 samples in 5-D is only ~4.6 effective
grid levels per axis, so the region's true shape is undersampled.

The follow-up is therefore a **constrained re-fit**, not string-model work: re-run
`--pass sustain` with a `legacy-v1` floor, or simply start it from the #17
neighbourhood, and judge it with `just gate-c4` as always.

The report at `out/sweep/sustain-note60.json` carries the full
`analysis.Metrics` and the per-profile `analysis.Components` breakdown of every
sample, so the gate constraint stays a post-hoc filter rather than something the
sweep has to know about:

```bash
jq '[.oat[],.joint[]] | map(select(.metrics.time_rmse <= 0.112)) | length' out/sweep/sustain-note60.json
```

One caveat baked into the tool: the sustain pass currently has an **empty**
window, so both profile scores are full-signal and comparable to
`just distance-c4`. If that ever changes, the scores become windowed and stop
being comparable to anything in this document. The report records `pass_window`
and the tool prints a warning when it is set.

## Deterministic sweep chain: how the 2026-08-22 C4 re-fit was done

The C4 re-fit that closed the widened `spectral_rmse_db` fence was produced by a
chain of `--sweep` runs, **not** by `fit-c4`, `fit-c4-stages` or
`fit-c4-passes`. Six 300 s Mayfly runs were tried first and all lost: the best
(`legacy-v1`, seed 1) reached `score` 0.4937 with `spectral_rmse_db` 47.5 but
`time_rmse` 0.1158 against a 0.112 fence. The sweep chain won because it is
deterministic, reproducible from the flags alone, and — crucially — lets the
selection criterion be chosen _after_ the samples exist.

The recipe, repeated until no direction improves `balanced-v2`:

```bash
# One box at a time, always from the current best preset, always at the
# settings `just distance-c4` uses, always scored under both profiles.
go run -tags asm ./cmd/piano-fit --sweep --pass none --optimize mix \
    --preset out/refit/<current>.json --reference reference/c4.wav \
    --sweep-out out/sweep/<name>.json --sweep-samples 9 --sweep-joint-evals 2048 \
    --sweep-profiles legacy-v1,balanced-v2 \
    --workers auto --note 60 --velocity 118 --release-after 3.5 --sample-rate 48000 \
    --decay-dbfs -90 --decay-hold-blocks 6 --min-duration 2.0 --max-duration 30
# then --pass sustain / --pass inharmonicity with --optimize piano,mix,
# and --pass attack with --sweep-joint-evals 0 (see the 8-dimension cap below).
```

Pick the winner with `jq`, filtering on the gate's raw metrics and sorting on
`balanced-v2`, then apply the winning knobs with `jq` and **verify by direct
measurement** — knob effects compose only approximately:

```bash
jq -r '[.oat[],(.joint//[])[]]
  | map(select(.metrics.time_rmse <= 0.1018938
               and .metrics.envelope_rmse_db <= 10.1561
               and .metrics.spectral_rmse_db <= 62.287
               and .metrics.decay_diff_db_per_s <= 4.8003))
  | sort_by(.scores["balanced-v2"]) | .[0:3]' out/sweep/<name>.json
```

Four rules that this round paid for:

- **Select on `balanced-v2`, never on `legacy-v1` alone.** `legacy-v1` saturates
  its spectral component and puts _no_ weight on partial level, partial
  frequency, tristimulus, attack or the segmented decay, so a chain steered by
  it pays those six to buy the four it sees. Continuing this exact chain under
  `legacy-v1` reached 0.4654 with `spectral_rmse_db` 48.1 and
  `decay_diff_db_per_s` 1.54 — better on every gated metric than what shipped —
  while pushing `partial_level_rmse_db` 10.88 → 24.20 and
  `attack_centroid_rmse_oct` 0.544 → 2.445. That is gate-gaming, and it is what
  the `balanced-v2` criterion exists to catch.
- **Exclude samples that move `render.velocity` or `render.release_after`.**
  Neither is stored in the preset, so the gate renders at 118 / 3.5 s regardless
  and a sample that moved them is not reproducible from the written preset.
  Filter with `.knobs["render.velocity"]==118 and .knobs["render.release_after"]==3.5`
  — both, not just the velocity: `release_after` is not serialised into the preset
  either, so a sample that moved it reports metrics the gate cannot reproduce.
- **`output_gain` is not score-neutral**, despite what `--match-output-gain`'s
  help says. The auto-stop is an **absolute** −90 dBFS threshold, so a louder
  render crosses it later, yields a longer candidate and scores differently:
  measured on one fitted preset, same knobs otherwise, `output_gain` 7.096
  scores 0.5208 and 1.357 scores 0.5061. Compare fitted presets only after
  normalising `output_gain` to the tracked preset's value.
- **The joint stage caps at 8 dimensions.** The `attack` pass exposes 9 knobs,
  so `--sweep --pass attack` with a joint stage fails with
  `halton: 9 dimensions exceed the 8-prime base table`, and
  `--sweep-joint-max-dims 9` does not help. Run that box with
  `--sweep-joint-evals 0` (one-at-a-time only).

**What it found.** The dominant term was the wet level of the legacy single-IR
stage, not the string model. This preset sets `ir_wav_path`, so `ApplyDefaultRoomIR`
leaves it alone and the engine loads it as the **room** stage: `ir_wet_mix` and
`ir_gain` are room controls here, not body-IR controls. The old preset carried `ir_wet_mix` 1.1888 with `ir_gain` 1.7203, an
effective wet factor of 2.045. The first 2076-sample mix sweep
(`out/sweep/mix-mayfly.json`) contained **887 samples that improve all four
gated raw metrics at once** — a region, not a lucky sample. The shipped preset
sits at `ir_wet_mix` 0.2328 / `ir_gain` 1.0912 / `ir_dry_mix` 0.2107, plus
`high_freq_damping` 0 → 0.05 and `unison_crossfeed` 0.00236 → 0.0025 from the
sustain box and `per_note.60.strike_position` 0.2945 → 0.45 from the
inharmonicity box. `resonance_gain` was not touched.

| metric                | before  | after   | gate cap        |
| --------------------- | ------- | ------- | --------------- |
| `score` (`legacy-v1`) | 0.5249  | 0.5182  | 0.57 → 0.559    |
| `time_rmse`           | 0.10189 | 0.10113 | 0.112 → 0.109   |
| `envelope_rmse_db`    | 10.156  | 9.593   | 11.75 → 10.34   |
| `spectral_rmse_db`    | 62.287  | 56.572  | 67.0 → **61.0** |
| `decay_diff_db_per_s` | 4.800   | 4.503   | 5.9 → 4.85      |
| `balanced-v2`         | 0.47391 | 0.42687 | not gated       |

All five gated rows are measured at the shipped `output_gain` 7.096. The
`balanced-v2` row is the selection-time figure at the comparison gain 1.357 and
was not re-measured, because `piano-distance` has no `--profile` flag; treat it
as the criterion the search was steered by, not as a current measurement.

## Multi-Note Fitting

`--notes` fits several notes jointly against one shared parameter set:

```bash
go run -tags asm ./cmd/piano-fit \
    --notes 48,60 \
    --reference-map "48=reference/c3.wav,60=reference/c4.wav" \
    --aggregate mean \
    --preset assets/presets/default.json \
    --output-preset out/multi/c3c4.json \
    --time-budget 900
```

- `--reference-map "note=path,..."` supplies a per-note reference and wins over
  `--reference`.
- `--aggregate mean|max|mean-max` combines per-note scores. `max` optimizes the
  worst note; `mean-max` blends the mean with the spread.
- `--note-weights "48=1,60=2"` biases the aggregate (empty means uniform).

**Budget:** every evaluation renders every note, so an N-note run costs N times
the wall time of a single-note run at the same eval count. The report records
this as `renders_per_eval` — check it before choosing `--time-budget`.

**Which notes to pick.** `defaultUnisonForNote` (`piano/utils.go:22-31`) buckets
notes by string count:

| Note range | Strings | Detune            |
| ---------- | ------- | ----------------- |
| `< 40`     | 1       | none              |
| `40..69`   | 2       | -1.8 / +1.8 cents |
| `>= 70`    | 3       | -3 / 0 / +3 cents |

C3 (48) and C4 (60) share a bucket; C5 (72) does not. A single shared
`unison_detune_scale` across all three therefore settles on a compromise that is
optimal for neither bucket. **Start with `--notes 48,60`** and only add C5 once
the unison knobs are made per-bucket.

## File Layout

```
out/stages/
├── stage1.json                        # Piano knobs + default IR
├── stage1.json.report.json            # Resume data
├── stage2-ir-body.wav                 # Mono body IR
├── stage2-ir-room.wav                 # Stereo room IR
├── stage2.json                        # Piano knobs + body IR path
├── stage2.json.report.json            # Resume data
├── stage3.json                        # Refined piano knobs + body IR
├── stage4-ir-body.wav                 # Refined body IR
├── stage4-ir-room.wav                 # Refined room IR
├── stage4.json                        # Joint-optimized everything
└── final.json                         # Final polished preset
```

## Resuming Within a Stage

`piano-fit` supports `--resume` (default: true). To continue a run that was interrupted:

```bash
# Just re-run the same command — it reads the report and continues
go run --tags asm ./cmd/piano-fit \
    --preset out/stages/stage1.json \
    --output-preset out/stages/stage1.json \
    --optimize piano,mix \
    --time-budget 600   # more time
    # --resume is true by default
```

The report file (`*.report.json`) stores the best knob values under `best_knobs`. On resume, these become the initial candidate, so the optimizer starts from the previous best rather than from scratch.

Resume works across optimization modes: knob names are shared, so a report from `--optimize=piano,mix` can seed a `--optimize=piano,body-ir,room-ir,mix` run (the piano/mix knobs carry over, IR knobs start from defaults).

## Quick Smoke Test

To verify the full pipeline works end-to-end with minimal time:

```bash
# Stage 1: piano knobs
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset assets/presets/default.json \
    --output-preset out/smoke/s1.json \
    --optimize piano,mix \
    --no-resonance \
    --note 60 --time-budget 30 --max-evals 100 --workers auto

# Stage 2: body IR
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/smoke/s1.json \
    --output-preset out/smoke/s2.json \
    --output-ir out/smoke/s2-ir.wav \
    --optimize body-ir,mix \
    --no-resonance \
    --note 60 --time-budget 30 --max-evals 100 --workers auto --resume=false

# Stage 3: refine piano with body IR
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/smoke/s2.json \
    --output-preset out/smoke/s3.json \
    --optimize piano,mix \
    --no-resonance \
    --note 60 --time-budget 30 --max-evals 100 --workers auto --resume=false
```
