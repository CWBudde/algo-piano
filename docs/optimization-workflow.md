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
- `--cpuprofile <file>`: Write CPU profile for performance analysis.

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

### Measured results (2026-08-21, 180 s per pass, from `fitted-c4-mayfly.json`)

> **Superseded — measured on the pre-#14 renderer.** Every number in this
> section was produced before the DWG treble-collapse fix, which changed every
> render (the baseline alone moved 0.5194 → 0.5330). The relative story below
> still reads correctly, but do not quote these figures as current. Re-running
> the three passes on the current renderer, under the calibrated norms, is the
> open follow-up.

Judged the only honest way — by re-running the full `legacy-v1` compare on each
pass's output preset:

| Stage                     | legacy score | what its own profile did                |
| ------------------------- | ------------ | --------------------------------------- |
| baseline                  | 0.5194       | —                                       |
| after `attack`            | **0.5117**   | attack centroid 0.440 → 0.084 oct       |
| after `sustain` (chained) | 0.5581       | segmented decay RMSE 14.58 → 12.84 dB/s |
| after `inharmonicity`     | 0.5691       | partial-frequency RMSE barely moved     |

Only the `attack` pass is currently a net win. Both others improve exactly the
metric their profile weights and pay for it elsewhere — the `sustain` pass buys
1.7 dB/s of segmented-decay accuracy with 9 dB of spectral RMSE (58.0 → 66.9)
and 13 dB of partial-level RMSE (14.0 → 27.4).

That was not a flaw in the pass machinery, it was `NormSpectral = 30.0` being
saturated. At 58 dB and at 67 dB the spectral component normalised to exactly
1.0, so it contributed a _constant_ to `decay-v1` and supplied no restoring
force at all against a spectral degradation of that size. Partial level carries
weight 0 in `decay-v1`, so nothing objected there either.

`decay-v1` now uses `analysis.CalibratedNorms()`, where `Spectral = 80.0`, so
that particular blind spot is gone — a 58 → 67 dB degradation now normalises
0.73 → 0.84 and the profile can see it. **The pass has not been re-run since,
so it is still not chained into a shipping preset**; run it in isolation and
check `just gate-c4` on the result until a re-measurement says otherwise.

Run `inharmonicity` from the attack-pass preset rather than the sustain-pass
preset — done that way it is score-neutral (0.5117 → 0.5121) and nudges
partial-frequency RMSE from 34.9 to 34.5 cents. Its three knobs simply have
little leverage at C4.

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
