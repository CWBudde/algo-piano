# PLAN.md — Implementation plan (bite-sized TODOs, Go-only)

This is the actionable task list derived from `goal.md` (DWG strings + commuted convolution) and `research.md` (stability/contact + validation). It is written to be executed incrementally, with each phase producing something runnable/testable.

Important constraint:

- **All code in this repo is written in Go (Golang).** No C++/CMake-based core.

Conventions used in this plan:

- **MVP path** follows the waveguide + IR-convolution approach.
- Items marked **(optional)** are clearly skippable without blocking the main path.
- Prefer **block processing** (e.g. 64/128 frames) everywhere except where sample-accurate state is required.

---

## Phases 0–8A — Foundation through first calibration baseline ✓

All items in these phases are complete; details live in the code, tests and
linked docs. Kept here as a one-line record of what each phase established.

- **Phase 0 — Repo skeleton + build.** Go module layout (`cmd/`, `piano/`,
  `dsp/`, `conv/`, `preset/`, `assets/`), self-contained 16-bit WAV writer,
  core public API types (`Piano`, `Voice`, `StringWaveguide`, `HammerModel`,
  `SoundboardConvolver`, `Params`), and base DSP utilities (`FlushDenormals`,
  `Biquad`, `DelayLine`, linear/cubic-Lagrange fractional delay).
- **Phase 1 — First audible note.** Single-delay-line waveguide with feedback
  and bridge pickoff, `N = fs/f0` tuning with fractional delay
  (`TestTuningAccuracy`), temporary bipolar-triangular excitation with
  velocity scaling, `NoteOn`/`NoteOff` in `Voice`.
- **Phase 2 — Loss + dispersion.** Frequency-dependent loop loss, tunable
  allpass dispersion cascade with note→inharmonicity mapping, fractional
  strike position — each with its own behavioural test.
- **Phase 3 — Hammer model.** Time-limited power-law felt contact
  (`F = k·δ^p` plus Hunt–Crossley dissipation) with stability clamps,
  integrated into waveguide scattering at the strike point; louder strikes
  measurably brighten and long renders stay NaN/Inf free.
- **Phase 4 — Unison strings.** 1–3 strings per voice with per-string detune
  and gain, bridge-output sum with crossfeed, beating verified by test.
- **Phase 5 — Soundboard/body IR.** Uniform partitioned overlap-add
  `SoundboardConvolver` on the `algo-fft` backend (48 kHz, mono→stereo,
  reset/flush), bounded-error test against direct convolution, wired into
  `Piano`'s voice mix.
- **Phase 6 — Pedals, dampers, releases.** Per-voice damper state, sustain CC
  with smoothed transitions, una corda affecting strike position and hammer
  hardness, plus pedal-up/pedal-down decay tests.
- **Phase 7 — Sympathetic resonance.** `ResonanceEngine` tracking undamped
  strings and injecting band-limited bridge energy, including per-note tuned
  filtering; bloom under sustain verified by test.
- **Phase 8 — Presets + parameterization.** `Params` schema for per-note
  physical parameters plus a JSON preset loader and `assets/presets/default.*`,
  so presets are tweakable without recompiling.
- **Phase 8A — Reference distance harness.** `analysis` package (time-domain,
  envelope, log-spectral and decay-slope metrics with automatic lag
  alignment), `cmd/piano-distance` with render controls and JSON output, and
  the first reproducible C4 baseline (2026-02-13: score `0.6147`, similarity
  `8.55%`, envelope `15.708 dB`, spectral `23.756 dB`, decay slope
  `7.858 dB/s`).

Remaining non-blocking follow-ups from Phases 4, 5 and 8 were moved to
**Phase 15** so these phases close cleanly.

---

## Phase 8B — Distance-guided timbre matching (C4 first, then scale out)

> **Recorded scores are comparable only within a renderer/metric generation.**
> There are two boundaries, and both invalidate numbers across them:
>
> 1. **The metric.** `analysis/distance.go` changed six times between 2026-02-14
>    and 2026-08-16 (phase detection, normalisation), so every score written into
>    a report before that window was produced by a different metric
>    implementation. This applies to the `0.6147` Phase 8A baseline above and to
>    the `best_score 0.3839` recorded in
>    `assets/presets/fitted-c4-mayfly.json.report.json` alike.
> 2. **The renderer.** The DWG treble-collapse fix (#14) removed a DC pedestal
>    that was 44.6% of the rendered candidate's RMS. It changed every render, so
>    every Phase 8B number measured before it — including the whole pass-results
>    table in `docs/optimization-workflow.md` — describes a synthesizer that no
>    longer exists. Re-measure; never compare across it.
>
> `docs/plans/2026-08-21-phase8b-metrics.md` has the detail.
>
> **Current C4 baseline (re-measured 2026-08-21, post-#14):**
> `assets/presets/fitted-c4-mayfly.json` vs `reference/c4.wav`, note 60,
> velocity 118, release-after 3.5 s, 48 kHz, decay-dbfs -90, decay-hold-blocks 6,
> min-duration 2.0, max-duration 30 — score `0.5330`, similarity `11.86%`,
> time `0.1022`, envelope `10.801 dB`, spectral `52.890 dB`, decay diff
> `5.428 dB/s`. The exact value `0.5329500259948227` is what `just gate-c4`
> prints and what `assets/thresholds/c4.json` is fenced against.

- [x] First optimization surface exposed: preset-controlled hammer influence,
      unison detune/crossfeed and IR wet/dry/gain scales, with the knob groups,
      bounds and staged optimization order now documented in
      `docs/optimization-workflow.md`.
- [x] Add render-control fitting loop (before touching physical params)
  - [x] Fast inner loop in place: `cmd/piano-fit --optimize=piano,mix`
        (time-budgeted, checkpointed best preset/report), `just fit-c4`
        entrypoint, persisted control settings with score snapshot, and relative
        IR paths in fitter output that stay loadable from `assets/presets/`.
        Fitted controls (`velocity=118`, `release-after=3.5`) are the baseline
        for `just distance-c4` and `just gate-c4`.
  - [x] Deterministic coordinate polish over the render controls: `--polish` /
        `--polish-only` sweep `render.velocity`, `render.release_after` and
        `hammer_initial_velocity_scale` (configurable via `--polish-knobs`)
        under a hard `--polish-evals` budget. A step is accepted only when it
        improves, so the stage **cannot regress** and is fully deterministic —
        `--polish-only --resume` is the standard finishing move on an existing
        best.
  - [x] Output gain is **solved, not searched**: `analysis.Compare`
        RMS-normalises both signals **and** the render auto-stop is taken
        relative to the render's own running peak (`internal/render`,
        2026-08-22), so the render length does not depend on the absolute level
        either. Both halves are needed and both are tested
        (`TestOutputGainIsScoreInvariant`,
        `TestOutputGainDoesNotMoveTheScoreThroughRenderLength`); with only the
        first, a louder render crossed the absolute −90 dBFS stop later and was
        scored over a longer window. `output_gain` is therefore score-invariant
        and searching it would burn budget on a flat dimension.
        `--match-output-gain` (default on) solves it analytically after the
        search instead, and it is deliberately excluded from the polish knobs.
        `--decay-relative=false` restores the old absolute threshold for
        reproducing pre-2026-08-22 numbers.
- [ ] Add physically-meaningful fitting passes for note parameters
      (`--pass none|attack|sustain|inharmonicity` restricts the movable knobs,
      optionally windows the compare via `--pass-window`, **and** now scores with
      the profile that describes the aspect — `attack-v1`, `decay-v1`,
      `inharmonicity-v1`. `--profile` overrides that and works with `--pass none`
      too; the profile is recorded as `score_profile` in the report.
      `just fit-c4-passes` runs all three and ends with a `legacy-v1` distance
      report for **each** pass output — the only numbers comparable across
      passes, since each fit is scored with its own profile. Its final artifact
      chains `attack` → `inharmonicity` and leaves the regressing `sustain` pass
      out. Measurements below, all reproducible from that recipe.)
  - [x] Attack pass: fit hammer hardness/contact settings to reduce early-window spectral error
        (re-measured 2026-08-22 on the post-#14 renderer under
        `analysis.CalibratedNorms()`; 180 s from `fitted-c4-mayfly.json`,
        `--pass-window 0:0.35`: legacy score `0.5330` → **`0.5214`**, attack
        centroid error `0.545` → `0.130` octaves. Still the one pass that is a
        net win, and it now beats the tracked C4 gate baseline outright — see
        the re-fit follow-up below.)
  - [ ] Sustain/decay pass: fit loss/damper behavior to match decay slope and envelope shape
        (**re-run 2026-08-22 on the post-#14 renderer under the calibrated norms.
        It still regresses the comparable score — it stays out of the shipping
        chain.** Chained from the attack preset, legacy score `0.5214` →
        **`0.5436`**, worse than even the unfitted `0.5330` baseline. The trade
        has the same shape as before: it buys exactly what `decay-v1` weights —
        decay diff `5.2` → `2.8` dB/s and envelope `10.1` → `9.3` dB — and pays
        with time RMSE `0.0978` → `0.1299`, spectral `51.7` → `56.0` dB and
        partial level `11.3` → `19.0` dB. Note the time RMSE alone would breach
        `assets/thresholds/c4.json` (max `0.112`), so this preset could not ship
        even if the score had held.
        The earlier diagnosis was that saturated norms let this happen unseen.
        That was only half right: `CalibratedNorms()` did remove the saturation
        from `decay-v1`, and the pass still makes the same trade, so a blind
        objective is not the whole cause.
        What this run establishes is an **observed trade-off**, not a proven
        model limitation: one 180 s stochastic run over the sustain knob set,
        one seed, one search strategy, found no non-regressing candidate.
        **Settled 2026-08-22 by `just sweep-sustain-c4`** (`piano-fit --sweep`,
        deterministic: a 9-point one-at-a-time scan per knob plus 2048 Halton
        points over the 5-D box, 2093 evals in 199 s, all at final render
        settings; report in `out/sweep/sustain-note60.json`). **A non-regressing
        region exists**, so the trade-off is a property of the search, not of
        the knob set or of the string model. Baseline `decay-v1 0.4380` /
        `legacy-v1 0.5189`; `constrained_best` is sample #17 at
        `decay-v1 0.3508` / `legacy-v1 0.5141`, reached by moving a single knob
        (`unison_detune_scale 0.774` → `1.75`) and with time RMSE `0.0958` →
        `0.0947`, inside the gate's `0.112`. `constrained_count` is 22 of 2092
        sampled points, 12 of which also clear the gate cap. The two knobs with
        the largest leverage on both objectives are `per_note.60.loss` and
        `render.release_after`; `unison_detune_scale` is the cheap one, moving
        `decay-v1` by 0.146 against only 0.033 of `legacy-v1`.
        Scope, honestly: 2048 samples in 5-D is ~4.6 effective grid levels per
        axis, and the region is ~1% of the sampled points with a `legacy-v1`
        gain of only 0.005. The sweep shows the region is non-empty, not that it
        is large or that a re-fit will beat #17.
        **Constrained re-fit built 2026-08-22, box stays open**
        (`just fit-sustain-constrained-c4`, i.e. `piano-fit --pass sustain`
        with `--score-constraint legacy-v1:<floor>`: a secondary-profile
        ceiling checked on the same rendered buffer, so the search optimises
        `decay-v1` while `legacy-v1` may not regress).
        First, the renderer moved twice: #23/#26/#29 and then #30's relative
        auto-stop, so every number above the line is stale. Re-measured on the
        post-#30 renderer, the baseline `attack.json` is `decay-v1 0.4948` /
        `legacy-v1 0.5222` and sample #17 is `0.3927` / `0.5183`, and
        `piano-fit`'s `legacy-v1` agrees with `just distance-c4` to four
        decimals on both — so the sweep and the fitter still measure the same
        thing, only from a different origin. The floor moves with #17: it is now
        `0.5183`, not `0.5086`.
        The constraint does its job: every run holds the floor and improves the
        baseline on **both** axes. What it does not do is clear the gate
        reproducibly. Two 180 s runs on the post-#30 renderer, seed 1, seeded
        from #17, floor `0.5183`, every figure measured on the written preset:

        | run   | evals | rejected | `decay-v1` | `legacy-v1` | `just gate-c4`                      |
        | ----- | ----- | -------- | ---------- | ----------- | ----------------------------------- |
        | seed #17 | —  | —        | 0.3927     | 0.5183      | —                                   |
        | run 1 | 2239  | 2137     | 0.3781     | 0.5130      | **FAIL** (`spectral_rmse_db` 63.29)  |
        | run 2 | 2293  | 2186     | 0.3715     | 0.5175      | **FAIL** (`spectral_rmse_db` 65.50)  |

        (cap: `spectral_rmse_db` 62.30. Two pre-#30 runs failed the same way
        against the old 61.00 cap, at 61.88 and 63.77, so this is not a #30
        artifact. The one 180 s run that did pass the gate — `0.3625` / `0.5024`
        at `spectral_rmse_db` 59.49, on the pre-#30 renderer — got only 1364
        evaluations out of its budget.)
        The recipe is wall-clock budgeted and runs parallel workers, so how far
        it converges depends on the machine — and the further the constrained
        search pushes `decay-v1`, the harder `spectral_rmse_db` blows past the
        fence. That single gate PASS was a property of that run's budget, not of
        the method, so the box does not close on it. The next step is to
        constrain what the gate actually measures — a second
        `--score-constraint` on the gated metric, or a gate-aware profile — not
        to re-run the same recipe until one passes.
        **That step is now built and measured, and the box still stays open —
        for a different reason.** `piano-fit` gained `--gate-thresholds` and
        `--metric-constraint`: RAW-metric ceilings enforced during the search,
        resolved by the same `internal/gate` code `just gate-c4` uses. A score
        ceiling could never have fixed this, and that is a mechanism rather than
        a guess: `legacy-v1` SATURATES its spectral component (`clamp01` pins it
        at 1.0 above `analysis.NormSpectral = 30.0`; every preset in the repo
        measures 47.8-68.6 dB), so `spectral_rmse_db` — the one metric that
        fails the gate — contributes a constant to `score` with no gradient and
        is invisible to `decay-v1` and to the `legacy-v1` floor alike.
        `just fit-sustain-constrained-c4` now passes
        `assets/thresholds/c4.json` by default, on top of the unchanged floor.
        Re-measured on the current renderer, the floor is `0.518264`, so the
        recipe's `0.5183` default still holds, and sample #17 itself clears the
        gate with 98% of the `spectral_rmse_db` budget used. Two 180 s runs plus
        one control, every figure measured on the WRITTEN preset:

        | run                       | evals | rejected | `decay-v1` | `legacy-v1` | `spectral_rmse_db` | `just gate-c4` |
        | ------------------------- | ----- | -------- | ---------- | ----------- | ------------------ | -------------- |
        | seed #17                  | —     | —        | 0.3927     | 0.5183      | 60.90              | PASS (98%)     |
        | seed 1, gated             | 2505  | 2433     | **0.3903** | **0.5092**  | 56.46              | **PASS** (91%) |
        | seed 7, gated             | 2521  | 2437     | **0.3788** | **0.5106**  | 61.01              | **PASS** (98%) |
        | seed 1, floor only (twin) | 2512  | 2367     | 0.3689     | 0.5180      | 65.64              | **FAIL**       |

        The twin is the control and it is the whole argument: same seed, same
        budget, same machine, same floor, only the raw fence removed — and it
        lands 5.4% past the cap, the same failure mode as the two runs above.
        With the fence both seeds clear the gate, beat #17 on `decay-v1` and
        hold the floor, so a gate PASS is now a property of the method rather
        than of how many evaluations the wall clock bought.
        Why the box nevertheless stays open. (1) The fence is BINDING, not
        slack — seed 7 finishes at 98% of the spectral budget — so the search is
        now shaping itself against the fence, and the un-gated metrics are where
        the payment shows: seed 1 pays `partial_level_rmse_db` 15.20 → 15.49,
        `attack_centroid_rmse_oct` 0.178 → 0.243 and
        `decay_segment_rmse_db_per_s` 11.51 → 12.49, while seed 7 pays only
        `partial_freq_rmse_cents` 35.64 → 36.40 and improves the other two.
        (2) The `decay-v1` gain over #17 is 0.0024 and 0.0139 — real, but two
        seeds on one machine is not a distribution. (3) Nothing was shipped:
        promoting one of these presets means replacing the tracked gate baseline
        preset and re-baselining `assets/thresholds/c4.json` against it, which
        is a separate change with its own evidence requirements and cannot ride
        along with the commit that builds the fence. No threshold and no floor
        was touched to make any of these runs pass.
        Also honest: neither run beats sample #17 on the primary objective by a
        margin that survives the gate. The sweep never promised otherwise.
        Two findings worth carrying forward. (1) The floor choice is not
        cosmetic: a pre-#30 run with the floor at the pass baseline rather than
        at the seed reached `decay-v1 0.3164` / `legacy-v1 0.5124` and failed
        the gate on `spectral_rmse_db` (64.46 > the then-61.00 cap). Constrain
        against the seed you started from. (2) `--match-output-gain` used to be
        score-affecting through the render length, which is why the constrained
        scores are re-measured on the winner's post-match re-render and a run
        whose winner breaches only after the match exits non-zero. #30 removed
        the root cause by making the auto-stop relative to the render's own
        peak, so under the default `--decay-relative` the re-measurement now
        only confirms what the search saw; it still matters under
        `--decay-relative=false`.)

  - [ ] Inharmonicity pass: fit dispersion/inharmonicity via partial-frequency error
        (re-run 2026-08-22: still no leverage, and now marginally negative rather
        than neutral. From the attack preset, legacy score `0.5214` → `0.5234`,
        partial-frequency RMSE unmoved at `36.6` cents. Its three knobs still
        cannot close a ~37-cent gap at C4, so the knob bound/scaling issue
        recorded in the Phase 8B design notes remains the blocker. Because it
        does not help, the `attack` → `inharmonicity` chain that
        `just fit-c4-passes` produces (`0.5234`) is very slightly worse than the
        attack pass alone (`0.5214`).)

- [x] Strengthen distance metrics for piano realism
      (`Compare` stays **bit-identical**: the new metrics carry weight 0 in the
      default `legacy-v1` profile. Named profiles `balanced-v2`, `attack-v1`,
      `decay-v1` and `inharmonicity-v1` are selectable via
      `CompareWithWeights`/`CompareWithOptions`. `Metrics.Sanitized()` fixes a
      real NaN-in-JSON crash. See `docs/plans/2026-08-21-phase8b-metrics.md`.)
  - [x] Add partial-ratio/tristimulus mismatch metric for harmonic balance
        (`partial_level_rmse_db`, `partial_freq_rmse_cents`, `tristimulus_distance`)
  - [x] Add attack-transient metric (onset rise + first 80 ms spectral centroid trajectory)
        (`attack_rise_diff_ms`, `attack_centroid_rmse_oct`, in octaves so it behaves
        the same across the keyboard)
  - [x] Add segment-wise decay metric (early/mid/late slope instead of single global slope)
        (`decay_segment_rmse_db_per_s`)
- [x] Regression guardrails
  - [x] Add acceptance thresholds for C4: `assets/thresholds/c4.json` enforces
        `score`, `time_rmse`, `envelope_rmse_db`, `spectral_rmse_db` and
        `decay_diff_db_per_s` at ~8-10% over the 2026-08-21 measurements. The six
        newer metrics are listed as `null` (present but not yet calibrated).
        Metric names resolve by reflection over the `analysis.Metrics` JSON tags,
        so a new metric is gateable with no code change and an unknown key is an
        error rather than a silently ignored typo.
  - [x] Check that rejects large regressions in distance metrics:
        `cmd/piano-distance --thresholds` exits 2 on breach, wrapped as
        `just gate-c4` and appended to the `ci` recipe. It reports worst-headroom
        on a pass so creeping regressions show up before they trip. It is
        deliberately **not** in `.github/workflows/ci.yml` and self-skips with
        exit 0 when `reference/c4.wav` is missing, because reference WAVs are
        gitignored by design — which keeps `just ci` green on a fresh clone.

- [x] Add metaheuristic optimizer integration (`github.com/CWBudde/mayfly`)
  - [x] Single-note integration done: optimization vector and bounds (hammer, loss,
        dispersion, strike position, release controls), `analysis.Compare` wrapped as
        weighted objective, C4 runs with fixed seed and checkpointed best candidate,
        best-fit preset persisted to a configurable path (default
        `assets/presets/fitted-c4.json`, tracked run
        `assets/presets/fitted-c4-mayfly.json`), plus max-eval/time budget controls.
  - [x] Add constrained multi-note run with shared + per-note parameter blocks:
        `--notes`, `--reference-map`, `--aggregate mean|max|mean-max` and
        `--note-weights`. Budget scales with the note count (every eval renders
        every note; the report records `renders_per_eval`). Start with
        `--notes 48,60`: `defaultUnisonForNote` (`piano/utils.go:22-31`) puts
        C3(48) and C4(60) in the same 2-string bucket but C5(72) in the 3-string
        bucket, so a shared `unison_detune_scale` across all three settles on a
        compromise optimal for neither.

**Done when:** C4 distance and sub-metrics improve materially and remain stable across changes.

**Norm calibration — done for the profiles that steer the optimizer.**
`NormSpectral = 30.0` is saturated for every preset in the repo (measured
47.8-68.6 dB on 2026-08-21, plus a degenerate 172 dB outlier from
`modal-calibrated.json`), and so are `NormPartialLevel`, `NormPartialFreq`,
`NormAttackRise` and `NormAttackCentroid`. A saturated component is a constant,
which is why the sustain pass could degrade spectral RMSE by 9 dB with neither
its own profile nor the legacy gate objecting.

`analysis.CalibratedNorms()` fixes this for `balanced-v2`, `attack-v1`,
`decay-v1` and `inharmonicity-v1`, with every value picked from the measured
population spread rather than guessed. `legacy-v1` deliberately keeps
`LegacyNorms()` and keeps saturating: that is what makes every tracked report
and `assets/thresholds/c4.json` mean what it says. The gate checks the raw dB
value, which is a real regression signal either way.

**Re-run done (2026-08-22).** All three passes were re-measured on the post-#14
renderer under the calibrated norms; the numbers in the boxes above are from that
run and are all `legacy-v1`, so they are comparable with each other and with the
`0.5330` C4 baseline. The measurement changed one conclusion and confirmed two:

| Preset                                   | legacy-v1 score | vs baseline |
| ---------------------------------------- | --------------- | ----------- |
| `fitted-c4-mayfly.json` (gate baseline)  | 0.5330          | —           |
| attack pass                              | **0.5214**      | −0.0116     |
| attack → inharmonicity (recipe artifact) | 0.5234          | −0.0096     |
| sustain pass (not chained)               | 0.5436          | +0.0106     |

- **Confirmed:** the sustain pass still regresses, so it stays out of the chain.
  The norm saturation was not the whole story — see the box above.
- **Confirmed:** the inharmonicity pass still has no leverage at C4.
- **Changed:** the attack pass alone now beats `fitted-c4-mayfly.json` by
  0.0116 and clears every threshold in `assets/thresholds/c4.json`. Read that as
  an improvement over the **tracked C4 fitting/gate baseline**, not over what
  users get: `cmd/piano-render` and the README still default to
  `assets/presets/default.json`, and `fitted-c4-mayfly.json` is a single-note C4
  artifact, not a shipped instrument preset. It is direct evidence for the
  re-fit follow-up that `assets/thresholds/c4.json` already names ("the honest
  fix for the two loosened numbers is to re-fit the C4 preset against the
  corrected core"). Re-fitting and re-baselining the gate is deliberately **not**
  part of this measurement, because it moves the CI fence.

---

## Phase 8C — Slow loop: IR-shape optimization with `ir-synth` + Mayfly

- [x] Preparation, tool scope and IO contract locked in, including the
      optimization vector over `irsynth.Config` (`modes`, `brightness`,
      `stereo-width`, `direct`, `early`, `late`, `low-decay`, `high-decay`) and
      the checkpoint/report/resume behaviour for long runs. The design note that
      recorded this has been removed as superseded; `cmd/piano-fit-ir` and
      `docs/optimization-workflow.md` are the current reference.
- [x] Outer-loop IR fitting implemented as `cmd/piano-fit-ir`: candidate IRs via
      `irsynth.GenerateStereo`, scored against `reference/c4.wav` through
      `analysis.Compare`, optimized over the full parameter vector above.
- [x] Mayfly integration for the outer loop: weighted distance objective, fixed
      seed, periodic best-candidate checkpoints, strict budget controls
      (`time-budget`, `max-evals`, round budget, population) and an optional
      joint mode (`--optimize-joint`) mixing in fast-loop knobs.
- [x] Best IRs saved under `assets/ir/fitted/` with score and synth parameters
      in a `.report.json` sidecar.
- [ ] Compare top-K IRs with multi-note validation before selecting default

**Done when:** synthetic IR candidates measurably reduce spectral/envelope distance without destabilizing decay behavior.

---

## Phase 9 — Full-instrument ringing architecture (persistent strings + coupling)

This phase is split into execution subphases to make progress and ownership explicit.

### Phase 9.1–9.4 — Foundation, persistent bank, and coupling ✓

- **9.1 Foundation refactor.** Key/control state, hammer excitation events and
  persistent ringing state are separate components; string lifetime no longer
  belongs to a transient voice; `NoteOn`/`NoteOff`/`SetSustainPedal` unchanged.
- **9.2 Persistent string bank.** Full 1–3-strings-per-note set allocated at
  init independent of active notes, per-string damper state and calibration
  hooks (detune, loss, inharmonicity, gain, strike mapping), no per-sample or
  per-block heap allocations in the bank path.
- **9.3 Baseline sparse coupling (MVP).** Sparse coupling graph with
  unison/near-unison, octave and fifth neighbourhoods, applied at bridge-side
  injection points under stable force limits, with a feature switch and gain
  controls in params/presets.
- **9.4 Physically-informed coupling.** `coupling_mode=physical` weight model
  built from overtone strength, harmonic frequency alignment, inter-string
  distance penalty, detune penalty and unison-count scaling; persisted
  string-distance map; sparse top-K edge precomputation with threshold and
  neighbour cap; per-source and polyphony normalization; user controls
  (`coupling_mode` `off|static|physical`, `coupling_amount`, plus harmonic
  falloff, detune sigma, distance exponent and max-neighbour knobs); hard
  `max_force` clamps retained in the injection path.

### Phase 9.5 — Instrument Semantics + Radiation + Web Migration

- [x] Make sustain/damper semantics instrument-wide:
  - [x] Sustain pedal down undamps relevant strings in the persistent bank (not just recently struck notes).
  - [x] Note release with sustain down stops excitation only; ringing continues until damping changes.
  - [x] Sustain pedal up reapplies damping deterministically to non-held strings.
  - [x] If partial pedal is supported, map to physical damping coefficients (not timer-based release logic).
- [ ] Lock linear radiation path around bank output:
  - [x] enforce `string-bank bridge mix -> body IR -> room IR` (fenced by `piano/radiation_test.go`: pure-delay body/room IRs driven through `Piano.Process`, room contribution must land at the sum of both delays)
  - [x] keep body/room separation first-class in params/presets (legacy single-IR mix normalised once in `resolveRadiationMix`, at construction and in `SetIRMix`, so the per-block path reads dual-IR fields only)
  - [x] keep legacy single-IR path as fallback only (`piano.ApplyDefaultRoomIR` points the `piano-render` / `piano-fit` / `piano-distance` defaults at `RoomIRWavPath`; `IRWavPath` is honoured only when set explicitly)
  - [x] complete WASM runtime IR apply (`wasmLoadIR`)
  - [ ] follow-up: ship a body IR asset and give `assets/presets/default.json` a `body_ir_wav_path` — deferred, there is no shipped body IR and picking one is a separate modelling decision
- [ ] Web/demo compatibility:
  - [ ] keep JS/WASM note + pedal API stable
  - [x] retire sustain timer release behavior in web layer once physical pedal semantics are active
  - [ ] verify no UI/playability regressions

### Phase 9.6 — Validation, Calibration, and Performance

- [ ] Add physics-behavior tests:
  - [x] pedal-down strike excites silent undamped related strings (octave + non-octave checks)
        (`piano/ringing_test.go` octave, `piano/sympathetic_test.go` fifth)
  - [x] pedal-up suppresses sympathetic buildup vs pedal-down
        (`piano/sympathetic_test.go`, DWG and modal cores)
  - [x] hammer contact ends while ringing continues
        (`piano/hammer_ringing_test.go`, both asserted in one render)
  - [x] `coupling_mode` transitions (`off/static/physical`) behave as expected
        (`piano/coupling_behaviour_test.go`, measured target energy per mode plus a
        mid-render `SetCouplingMode` switch, DWG and modal cores)
  - [x] detune and distance penalties measurably reduce coupling according to model
        (`piano/coupling_behaviour_test.go`, monotone sweeps of
        `CouplingDetuneSigmaCents`, `CouplingDistanceExponent` and
        `CouplingMaxNeighbors`)
- [x] Add regression tests for API compatibility and long-render stability (no NaN/Inf).
      (`piano/integration_test.go` `TestLongRenderHasNoNaNOrInf`: table over
      DWG/modal x `off/static/physical` x pedal script (held, mid-render release,
      partial sustain, soft pedal) x resonance at 44.1/48/96 kHz, several seconds
      each, every sample finite plus a peak runaway bound. `piano/api_compat_test.go`
      pins the `*Piano` method set via a compile-time interface assertion, the
      `CouplingMode`/`StringModel` string literals, the `Process(n) -> 2n` contract
      and the `NewDefaultParams` defaults. `cmd/piano-wasm/export_contract_test.go`
      cross-checks the `js.Global().Set` export names against the `wasm*` call sites
      in `web/`. Found one real defect — the modal core with `ResonanceEnabled`
      diverged to NaN after ~0.29 s — which is now fixed by `resonanceForceScale`
      in `piano/modal_group.go`; the `modal-physical-resonance` row asserts
      again, and `piano/modal_resonance_test.go` pins the resonance loop gain on
      both cores.)
- [ ] Add benchmarks:
  - [x] idle full-string-bank cost (`BenchmarkStringBankIdle`)
  - [x] active polyphony with coupling `off/static/physical`
        (`BenchmarkStringBankCouplingModes`, poly-1 and poly-8 low/mid/high/mixed
        registers x pedal up/down x all three modes)
  - [x] coupling graph density/top-K scaling vs CPU
        (`BenchmarkStringBankCouplingGraphDensity`; sweeps `maxNeighbors`
        1..87 including the production default of 10, plus an edge-weight
        floor. Edge count is not the CPU lever, the active-voice count the
        graph recruits is — see BENCHMARKS.md)
- [ ] Define calibration workflow for physical coupling knobs against multi-note recordings.

**Done when:** one struck note with sustain down audibly excites non-struck related strings through the physical coupling model, coupling strength is controllable (`off` to strong) via general parameters, hammer/ringing remain decoupled, and body/room + web compatibility remain intact.

---

## Phase 10 — Web demo (WASM + AudioWorklet) ✓

- [x] Go-only WASM build (`syscall/js`) with a stable exported API for process
      block and note events, keeping web/API behaviour independent of the core.
- [x] Web demo under `web/`: AudioWorklet processor, 2-octave keyboard UI with
      sustain toggle, computer-keyboard bindings, WASM bridge to the Go engine.
- [x] Build and deployment: `scripts/build-wasm.sh`, GitHub Actions deploy to
      GitHub Pages, IR asset loading with graceful fallback.

**Done when:** playable in browser without glitches on a typical machine. ✓

---

## Phase 11 — Spectral fidelity & timbral accuracy

**Goal:** Close the spectral gap between synthesized output and reference recordings. STFT analysis (cmd/spectral-compare) reveals systematic deficiencies that need model improvements.

### Diagnosis (from STFT comparison of Stage 3 vs reference C4)

- **40 dB level gap:** Candidate peak is -47.5 dB, reference peak is -6.5 dB. The output_gain range (0.4-1.8) and IR mix parameters are not compensating enough. The signal chain has an inherent scaling issue.
- **Attack transient is weak:** The broadband hammer impact energy is missing. Real pianos have a short (~2-5ms) noise burst from felt-on-string contact that creates the initial "click/thud." Our hammer exciter produces a smooth force pulse but lacks this impulsive broadband component.
- **High harmonics decay too fast:** In the sustain/decay phase, high-frequency energy (>3kHz) drops 20-60 dB faster than the reference. The per-sample loss model damps all harmonics equally, but real strings have frequency-dependent damping (higher partials should decay faster, but not THIS fast — the current loss is too aggressive across the board).
- **Spectral RMSE dominates the score:** At 20 dB, it contributes 52% of the total distance score (0.201 out of 0.389). Fixing this is the highest-leverage improvement.

### Tasks

#### 11.1–11.4 — Level, attack noise, HF sustain, spectral metrics ✅

- **11.1 Output level calibration.** Root cause was a hardcoded
  `contactForce * 0.002` in `piano/control.go` (−54 dB). Force scaling changed
  to `0.2` and `output_gain` widened from `[0.4,1.8]` to `[0.01,5.0]`; the
  peak level gap against the reference went from −41 dB to −1 dB (stage3).
- **11.2 Hammer attack noise.** Short broadband burst at onset with
  `attack_noise_level`, `attack_noise_duration_ms` and `attack_noise_color`,
  exposed as piano-fit knobs; 0–20 ms attack-window spectral energy improved.
- **11.3 High-frequency sustain.** Added `high_freq_damping` `[0.0, 0.6]` in
  `Params`, preset JSON and the fit knobs — it drives the previously hardcoded
  one-pole in the DWG loop and scales the order/frequency-dependent terms in
  `modalDecay`. A second pole proved unnecessary. Test confirms tail spectral
  centroid moves 1711 Hz → 816 Hz across the damping range.
- **11.4 Spectral distance metrics.** `spectralRMSEDB` now samples 8 windows
  instead of one, phases are detected via envelope peak and −20 dB threshold
  and weighted (attack 40%, sustain 35%, decay 25%), and per-band metrics
  (`SpectralLowRMSEDB` 0–500 Hz, `SpectralMidRMSEDB` 500 Hz–2 kHz,
  `SpectralHighRMSEDB` 2 kHz+) are reported by `piano-distance`,
  `spectral-compare` and `piano-fit`.

#### 11.5 — Body IR Kirchhoff model refinements (deferred)

- [ ] Full tier: algo-pde Helmholtz eigensolve for arbitrary plate geometry with ribs
- [ ] Investigate whether the body IR is contributing to or compensating for the level gap

#### 11.6 — Re-run optimization pipeline after model fixes

- [ ] Stage 1: piano,mix with new hammer noise + level fix
- [ ] Stage 2: body-ir,mix with Kirchhoff plate modes
- [ ] Stage 3: piano,mix re-tune
- [ ] Stage 4: joint optimization
- [ ] Target: score < 0.25 (currently 0.39), spectral RMSE < 10 dB (currently 20 dB)

### Tools created

#### `cmd/spectral-compare`

STFT-based spectral comparison between a reference WAV and a rendered preset. Reports per-band RMSE across time windows.

```bash
go run --tags asm ./cmd/spectral-compare \
    --reference reference/c4.wav \
    --preset out/stages/stage3.json \
    --note 60 \
    --velocity 121 \
    --release-after 3.39 \
    --sample-rate 48000
```

Output: peak/RMS levels, FFT-based lag alignment, per-window RMS gap, then a table per time window (attack 0-20ms, early 20-100ms, sustain 100-500ms, decay 0.5-2s, late 2-4s) with per-band spectral RMSE (sub-bass through air), reference and candidate power levels, and diff. Bands with RMSE > 15 dB are flagged with `<<<`. Includes overall spectral RMSE and optimizer-aligned metrics (score, similarity, 4 sub-components). Uses adaptive FFT size per window (512 for attack, 2048 for early, 4096 for longer windows).

### Current state (as of 2026-02-15)

- Unified `piano-fit` tool with `--optimize` group selection, `--no-resonance`, `--cpuprofile`
- Body IR uses Kirchhoff plate eigenmodes with 2-way frequency-dependent decay
- Optimized body IR defaults from Stage 2 fitting (PlateRatio=2.36, StiffnessRatio=8.33, ModeWarp=1.20, CrossoverHz=1145)
- Stage 2 passthrough fix: 27x speedup (room convolver skipped when room-ir group not active)
- 11.1 fix: hammer force scaling 0.002→0.2, output_gain range [0.01,5.0], peak level gap -41→-1 dB
- Distance metric: multi-window spectral RMSE (5 positions across signal), per-position detail, dominant-factor display in piano-fit output (`spec@3.6s:60%`)
- `piano-distance` shows full component breakdown table with normalized contributions

**Done when:** spectral RMSE < 10 dB, overall score < 0.25, attack transient matches reference character.

---

## Phase 12 — Modal bank core + DWG matching pipeline

**Goal:** add a modal-bank string core for low-CPU operation while preserving timbral compatibility with the DWG reference path.

### 12.1–12.3 — Architecture, modal core, and DWG matching ✓

- **12.1 Architecture + runtime switch.** Shared `StringModel` contract
  (`SetKeyDown`, `SetSustain`, excitation injection, block render, reset, and
  coupling injection with the same external API as DWG), engine-level
  `dwg | modal` selection in params/presets resolved at init, web/API behaviour
  unchanged either way.
- **12.2 Modal bank core.** `StringModalBank` with fundamental-plus-partials
  mode layout and optional inharmonicity, keyboard-wide per-mode damping,
  hammer→mode excitation projection, DWG-equivalent sustain/damper semantics,
  anti-alias/stability handling for partials near Nyquist, and no per-block
  heap allocations.
- **12.3 DWG-to-modal matching.** DWG renders (attack window, early sustain,
  decay envelope) act as fit targets; per-note and register-wise mode
  gain/damping fitting exports versioned modal profiles into
  `assets/presets`. Tooling: `cmd/piano-modal-fit`, whose objective search was
  moved from ad-hoc random mutation to Mayfly (`--mayfly-variant`,
  `--mayfly-pop`) with local post-refinement. The optional shared global priors
  are deferred to **Phase 15**.

### 12.4 — Validation + performance acceptance

- [ ] Add A/B tests and metrics:
  - [x] DWG vs modal distance on selected single notes
        (`piano/core_distance_test.go`. Re-measured 2026-08-21 after the DWG DC
        and injection fixes: 0.83-0.84, bound tightened 0.90 → 0.89. The cores
        still do **not** match. Removing the DC changed the score by less than
        0.01, so the DC was not the cause: what keeps whole analysis windows at
        digital silence is that the DWG excitation fills only 4-25% of the delay
        line, so the bank output is a sparse impulse train. Closing that gap
        needs a distributed excitation or a loop filter with real bandwidth.)
  - [x] DWG vs modal distance on chords
        (`TestDWGModalDistanceIsBoundedOnChords` in `piano/core_distance_test.go`.
        Measured 2026-08-22, 750 blocks, sustain held: C3 major (48/55/60/64)
        0.8434, C4 major (60/67/72/76) 0.8401. Same 0.89 bound as the single-note
        test, so polyphony is required not to make the cores diverge further.)
  - [x] sustain pedal and coupling behavior parity checks
        (`piano/core_parity_test.go`. Mid-render pedal lift, tail RMS over the
        final 80 blocks: DWG held/lifted 2.55x, modal 8.19x, bound 1.50x.
        Coupling-mode ordering in a silent note 72: off exactly 0 in both cores,
        static 1.89e-09 (DWG) / 1.84e-09 (modal), physical 3.35e-10 / 3.10e-10 —
        the static > physical > off ordering is asserted to match across cores.
        These pin relative behaviour only; absolute agreement is impossible while
        the distance sits at 0.83-0.84.)
  - [x] long-render stability (NaN/Inf free)
        (`TestLongRenderHasNoNaNOrInf` in `piano/integration_test.go`, see
        Phase 9.6. The modal + `ResonanceEnabled` row is live again: the modal
        core injected the per-sample bridge force straight into mode state
        without the force-to-state conversion the waveguide gets for free from
        its delay line, which put the open-loop resonance gain at 12.16 at A0
        (DWG: 0.41) and made the linear loop in `Piano.Process` diverge.
        `resonanceForceScale = f0/fs` in `piano/modal_group.go` fixes it —
        post-fix loop gain 0.007 at A0, 0.0015 at C4 — and the row now peaks at
        149.63, the same as `modal-physical-pedal-release`, well under the 1024
        runaway limit. `TestResonanceLoopGainIsBoundedAcrossCores` and
        `TestModalResonanceEnergyStaysBounded` in
        `piano/modal_resonance_test.go` guard the loop gain and the decay.
        Review of that change asked whether a per-note bound can bound the loop
        at all, since `InjectFromBridge` drives _every_ undamped group and
        `StringBank.Process` sums all of their responses back into the same
        bridge signal. Measured with the whole bank sustained
        (`TestAggregateResonanceLoopIsBounded`): the modal aggregate is bounded
        with margin — 0.0164 at defaults and 0.1208 under
        `assets/presets/modal-calibrated.json`, both with
        `ResonancePerNoteFilter` true, and roughly 20x lower with it false — and
        the closed loop decays to zero in every modal row, so the fix holds for
        the summed loop and not only per note.)
  - [x] **the DWG core's aggregate resonance loop grows with the sustain pedal
        held.** Found while answering the review above; it predated the modal
        fix, which touches no DWG file. With all 88 groups undamped the
        aggregate open-loop gain was 1.43 at defaults and 1.98 under
        `modal-calibrated.json`'s resonance gain (per-note only 0.41), and
        through the public `Piano.Process` a render grew from a peak of 3.2 at
        1 s to 1.2e7 at 40 s. It was invisible because `dwg-off-resonance` in
        `TestLongRenderHasNoNaNOrInf` runs 4 s, over which the growth is only a
        few dB.
        **Resolved 2026-08-22 by the `noteResonator` normalisation below**, and
        the suspicion recorded here was right: the aggregation was not the
        mechanism. `noteResonator` was built with `b0 = 1-r`, which peaks at
        `1/(2*sin(w0))` — a 1/f0 law summing to 183.1 at A0, 20.1 at C4 and 2.6
        at C7 — so the per-note filters, not the summing, put the loop over
        unity. Normalising to unity peak gain drops the hottest DWG aggregate
        60x (1.4276 → 0.0237) with **no change to `ResonanceGain`**, so no
        per-source or polyphony normalisation was needed; the 40 s render now
        decays (peak 0.44 at 40 s against 1.2e7). The DWG rows of
        `TestAggregateResonanceLoopIsBounded` are un-skipped and asserting, and
        `TestDWGResonanceLongRenderDecays` in
        `piano/resonance_normalisation_test.go` closes the loop through
        `Piano.Process` for 45 s. The modal conclusions of the entry above still
        hold — the modal aggregate only moves further from the bound (0.0164 →
        0.0002 at defaults, 0.1208 → 0.0016 under `modal-calibrated.json`).
  - [x] follow-up: **interleave `InjectFromBridge` with rendering instead of
        driving a frozen string state.** Done 2026-08-22. The loop now runs
        inside `StringBank`'s per-sample render loop, driven by the previous
        sample's own bank output, so the delay is one SAMPLE rather than one
        block. `ResonanceEngine.injectSample` is now the ONLY injection entry
        point: `InjectFromBridge` and the `RingingState.ResonanceTargets` /
        `NotifyResonanceInjected` forwarders that were the only way to reach it
        are removed, because closing the loop from outside the bank was the
        defect. Probes use a `processWithBridge` drive seam instead, so they
        measure the loop production actually runs. `Piano` owns the engine and
        re-attaches it after
        every rebuild of the ringing state (`attachResonance`), which
        `TestSetStringModelKeepsResonanceWired` pins because `SetStringModel`
        would otherwise silently drop it. Cost is unchanged — the old code
        already paid the full per-sample cost, it just deposited into frozen
        state — at 0.746 ms / 0.653 ms against 0.77 / 0.61, still 0 allocs.

        **What the defect was, exactly.** For a mode at angular frequency `w`
        the string received `|sum x[i]|`, the block's DC content, instead of
        `|sum x[i]*exp(-jwi)|`, the drive at `w`. That is a 128-tap boxcar at
        block rate: ~+42 dB at DC, first null at `fs/128` = 375 Hz, −13 dB
        sidelobe rejection above it, plus block-rate imaging. The partials the
        `noteResonator` bank is tuned to were being annihilated before injection
        across most of the keyboard.

        **Proof it is fixed:** `TestResonanceLoopIsBlockSizeInvariant` requires a
        resonance-on tail to be **bit-identical** across caller block sizes
        1/16/64/128/333 with coupling off. Every element of the loop is a
        per-sample recursion and `prevMix` carries the delay across the call
        boundary, so that holds by construction. On the old code the same
        comparison is off by 1e-3 to 1e-2 absolute against a peak of 2.7 — three
        to five orders above the round-off floor. A negative control (bank with
        no engine) and a `Piano.Process`-level variant bounded at 1e-5 relative
        (the convolvers are not bit-identical off-partition) come with it.

        **Measured: the loop got 13 dB louder at the same gain.** Isolating note
        72's sympathetic output by differencing two renders (held silent vs not
        held, note 60 struck, coupling off): DWG −52.21 dB → **−38.93 dB** under
        the struck note, modal −98.25 → **−85.22**. That is 13.3 / 13.0 dB
        recovered with no change to `ResonanceGain`, against the −26.1 dB at C4
        the resonator normalisation cost. It bears directly on the "recover
        sympathetic resonance level" item below, which stays OPEN.

        **The hot register moved up**, as predicted: the modal per-note open-loop
        gain now rises monotonically with pitch (21: 0.000068 → 84: 0.000308)
        where it used to peak at note 36. The aggregate `perNoteFilter=false`
        rows fell 3.6-4.2x — the old DC-ward gain was applied to all 88 responses
        at once and they added coherently — while the `true` rows rose up to
        1.2x. The 0.5 bound holds with the hottest row at 0.2413. All recorded
        tables in `piano/modal_resonance_test.go` were re-measured.

  - [x] **the C4 gate was buying 14.7 dB of its spectral score from the
        block-deposit artefact.** Found while re-baselining for the interleave.
        `assets/thresholds/c4.json` `spectral_rmse_db` was LOOSENED 62.3 → 78.3,
        by far the largest loosening in that file's history; the other four
        enforced thresholds were TIGHTENED in the same pass.

        The decisive measurement is the same preset with `resonance_enabled:
        false`, a path this change cannot execute a single instruction on:

        | | score | spectral | high(2k+) | frames |
        | - | - | - | - | - |
        | resonance OFF, before | 0.529104 | 72.531788 | 77.215274 | 177792 |
        | resonance OFF, after | 0.529104 | 72.531788 | 77.215274 | 177792 |

        Bit-identical. So the model's own spectral error against `reference/c4.wav`
        was ALWAYS 72.53 dB. What the gate was fenced against — 57.80 dB — was
        that same model plus 14.73 dB of masking from the defect: an impulse
        deposited once every 128 samples is a 375 Hz-spaced impulse train,
        broadband by construction, and it was filling the 2 kHz+ band with
        block-rate imaging that sat closer to the reference than the model's real
        output does. The corrected loop contributes 0.09 dB (72.53 → 72.62), and
        sweeping `resonance_gain` over 0 … 1e-3 moves the metric only between
        72.4 and 72.9. The whole move is the high band: low 27.5 → 27.1, mid
        40.1 → 41.4, high **61.4 → 77.3**.

        The deficiency this EXPOSES is already on the books — Phase 11's
        diagnosis reads "high-frequency energy (>3kHz) drops 20-60 dB faster than
        the reference", and 77.2 dB is that. Masking it was never a fix. The
        standing promise is the same one the DC pedestal and the resonator
        normalisation both paid off: **re-fit the C4 preset against the corrected
        renderer**, selected on `balanced-v2`. Nothing here may be loosened again
        first.

  - [x] **re-fit `assets/presets/fitted-c4-mayfly.json`.** Done 2026-08-23,
        paying off the standing debt from the interleave AND the one the unison
        coupling fix created in the same pass. The preset had been fitted against
        a renderer whose sympathetic path was a block-rate boxcar and whose
        unison coupling added energy for free, and it was leaning on both: with
        the coupling corrected and nothing else changed, the C4 decay slope went
        from −5.8 dB/s (reference −5.8) to −13.9 dB/s and the gate failed three
        of five enforced metrics.

        `just fit-c4-passes time_budget=300`, then
        `just fit-sustain-constrained-c4 preset=out/passes/attack.json
        floor=0.5635 time_budget=600`. The constrained recipe is what this needed:
        the unconstrained sustain pass had the best legacy-v1 score of the three
        (0.5504) but breached `time_rmse` at 0.1261 against the 0.109 fence,
        which is exactly the gate-versus-score split that recipe exists to close.
        3355 of 5000 candidates were rejected.

        | | old preset | re-fit | threshold |
        | - | - | - | - |
        | score | 0.5738 | 0.5332 | 0.568 |
        | time_rmse | 0.1009 | 0.1070 | 0.109 |
        | envelope_rmse_db | 14.68 | 10.83 | 11.62 |
        | spectral_rmse_db | 74.70 | 77.38 | 78.3 |
        | decay_diff_db_per_s | 8.13 | 3.88 | **4.19** |

        **Nothing was loosened.** `decay_diff_db_per_s` was tightened 4.72 → 4.19
        and the other four stayed where they were, even though their
        measurements got worse, because the measured+8% convention would have
        widened all four and the no-loosening rule outranks it. The 78.3 spectral
        fence the interleave asked for on an explicit promise of a re-fit was
        honoured at 77.38 without asking for another inch. Un-enforced metrics
        mostly improved sharply: tristimulus 0.230 → 0.098, attack centroid 0.947
        → 0.252, segmented decay 16.75 → 9.36.

        The re-fit chose `unison_crossfeed` 0.0034, higher than the 0.0025 it
        replaced. That is not the fitter finding the pump again — the corrected
        term takes energy out at any strength — it is buying beating and
        two-stage decay, which is what the term is for.

        Still open and NOT closed by this: `spectral_rmse_db` sits at 99% of its
        budget with the high band at 82.1 dB. That is Phase 11's known HF
        deficiency with nothing masking it, it is a model problem rather than a
        fitting one, and no further re-fit will close it.

  - [x] **the DWG bank grew on its own with the pedal held, with no sympathetic
        path at all — the unison bridge coupling was a positive feedback loop.**
        Found while measuring the interleave, root-caused and fixed 2026-08-23.

        The symptom: six notes struck once, pedal held, coupling off,
        `ResonanceEnabled` false so `Piano` never even constructs an engine, and
        the output still grew **1.21x over 120 s and 5.41x over 300 s**,
        geometrically. Nothing adds energy after the attack.

        The cause was in `RingingStringGroup.processSample`. The unison coupling
        force was `c * mix`, injected into every string of the group **including
        the one that produced the sample**, at strike position 0.92. That is not
        coupling but a bare positive feedback loop wrapped around an already
        resonant string, and it added energy unconditionally: the string loop
        reflection loses 2e-4 per round trip while the crossfeed injected `c` per
        sample. Single-string notes — everything below MIDI 40 — have no
        coupling path and were bit-identical, which is what localised it:

        | note | strings | before | after | `c = 0` |
        | - | - | - | - | - |
        | 33 | 1 | 0.1469 | 0.1469 | 0.1469 |
        | 45 | 2 | 0.5583 | 0.0036 | 0.0256 |
        | 52 | 2 | 1.2545 | 0.0003 | 0.0063 |
        | 60 | 2 | 1.7159 | 0.0000 | 0.0003 |

        The fix is two changes, both load-bearing. The force is now
        `c * g_i * (mix - y_i)` — proportional to the **difference** between the
        bridge motion and the string's own contribution to it, which makes the
        term dissipative: with weights summing to one it adds
        `c*(mix^2 - sum(g_i*y_i^2)) <= 0` of energy per sample, by Jensen. And it
        is injected at `unisonCouplingStrikePos`, the freshest observable slot,
        so it is read back on the next sample. The difference form **alone was
        not enough**: at position 0.92 the force returns nearly a full round trip
        late, at partial _n_ it lags `2*pi*n*0.92`, and once that passes a half
        cycle the same term is anti-damping. Measured on the chord render:

        | `unison_crossfeed` | pos 0.92 | pos 0.01 |
        | - | - | - |
        | 0.0008 (default) | 0.1166 | 0.1275 |
        | 0.0025 (mayfly) | 0.2014 | 0.1315 |
        | 0.0050 (`fitted-c4.json`) | **88.4618** | 0.1328 |
        | 0.0100 | — | 0.1322 |

        Note the third row: at the value `assets/presets/fitted-c4.json` actually
        ships, the difference form on its own still diverged. The coupling stays
        stable to `c = 0.05` and diverges at 0.1, so `NewStringBank` clamps to
        `maxUnisonCrossfeed = 0.02` — 4x above the knob ceiling in
        `cmd/piano-fit/knobs.go` and 5x under the measured cliff.

        Pinned by four tests in `piano/unison_coupling_test.go`, the property one
        being `TestUnisonCouplingRemovesEnergy`: a coupled multi-string note must
        decay **faster** than the same note uncoupled. `TestDWGSustainedBankGrows
        WithoutResonance` became `TestDWGSustainedBankDecaysWithoutResonance` and
        now asserts decay to 0.138x rather than fencing growth at 1.35x.

        The DC half of this same defect had already been patched once, on
        2026-08-21, by putting a DC blocker inside the string loop — the "DC
        runaway" paragraph in `piano/tuning_test.go` names "unison crossfeed
        injects into every string of the group on every sample" as the cause and
        the fix was applied downstream of it. This is the AC half, fixed at the
        source.

  - [ ] follow-up: **the modal core's unison crossfeed has the same
        non-passive shape.** `ModalStringGroup.applyCrossfeed` still adds
        `sample * c * 0.08` into each string's first mode, with no subtraction
        of that string's own contribution — structurally the defect the DWG core
        just had. It is NOT currently observable: the 0.08 factor and the modal
        damping keep it far under unity, and the 120 s pedal-held chord render
        reaches digital silence long before the reference window either way. It
        is left alone here because making it passive needs per-string sub-sums,
        and those live inside three reduce variants in `piano/modal_kernel.go`
        including the SIMD one, so the change would touch the hot path and the
        kernel-parity tests rather than one function. Worth doing for the same
        reason the DWG fix was: the bound is currently an accident of the 0.08,
        not a property of the term.

  - [x] **the interleaved DWG sympathetic loop no longer multiplies that
        growth.** Fixed by the item above, 2026-08-23. The same 120 s render at
        the shipped `resonance_gain` 0.00025 now reads **0.1338x** against the
        0.1275x resonance-off baseline: the sympathetic path costs about 5% of
        the ratio and the render still ends 17 dB below its own reference window.
        It previously read 5.06x against 1.21x.

        The earlier reading of that 5.06x — that the corrected sympathetic loop
        was itself unstable — was wrong, and the evidence was already in hand:
        **no gain avoided it** (over 300 s against a 5.41x baseline, 1e-6 gave
        5.50x and 5e-5 gave 12.7x), which is the signature of a plant above unity
        rather than of a hot loop. The loop was compounding the coupling defect,
        not causing it. Fenced by `TestDWGResonanceSustainedDecayIsFenced`, now a
        decay assertion rather than a fence on a known-bad number.

        **The open-loop probes did not see any of this**, and that limit stays
        written into them: they read 0.174 (default) and 0.241
        (modal-calibrated) against a 0.5 bound for renders that were growing 5x
        in two minutes. They inject a sine at ONE note's fundamental into a plant
        they assume is stable. A passing bound is necessary, never sufficient.

  - [x] normalise the `noteResonator` bank. Done 2026-08-22: `b0` is now
        `(1-r)*sqrt(1 - 2r*cos(2*w0) + r^2)`, which makes the peak exactly one
        at every centre frequency in both cores.
        `TestNoteResonatorHasUnityPeakGain` and
        `TestResonanceDriveBankGainIsRegisterIndependent`
        (`piano/resonance_normalisation_test.go`) pin it analytically and
        against a driven sine. `ResonanceGain` was deliberately **not** re-fitted
        with it; see the three follow-ups below, which that decision opened.
  - [x] re-baselined `assets/thresholds/c4.json` for the resonator
        normalisation. Every shipped preset pins `resonance_gain: 0.00025`, so
        the normalisation quiets their sympathetic resonance and the gate preset
        moves with it. Measured 2026-08-22 on
        `assets/presets/fitted-c4-mayfly.json`: `score` 0.5330 → 0.5249
        (**improved**, and close to the 0.5240 the same preset scores with
        resonance switched off entirely), `time_rmse` 0.1022 → 0.1019,
        `envelope_rmse_db` 10.801 → 10.156 and `decay_diff_db_per_s` 5.428 →
        4.800 all improved, but `spectral_rmse_db` 52.89 → **62.287 against a
        cap of 57.50**. The spectral component saturates under the frozen
        legacy-v1 norms, which is why `score` improves while the raw metric
        breaches. `spectral_rmse_db` was therefore LOOSENED 57.5 → 67.0 (7.7%
        headroom, the convention that file states); the three metrics that
        improved were deliberately left alone, because the re-fit below will
        move them again. Full rationale is in that file's `_comment` and
        `recorded.note`.
        Re-voicing the preset's `resonance_gain` to buy the metric back was
        measured and **rejected**: sweeping it on the corrected renderer gives
        0 → 75.8 dB, 0.00025 → 62.2, 0.0005 → 59.4, 0.001 → 56.5, 0.002 →
        53.8, so 0.001 would clear the old cap — but the stability data in the
        follow-up below puts 0.001 squarely in the marginal band. Do not retry
        it from the 56.5 dB figure alone.
  - [x] follow-up: **re-fit the C4 preset against the corrected renderer.**
        Done 2026-08-22. Superseded on 2026-08-23 by a further re-fit against the
        interleaved sympathetic loop and the corrected unison coupling — the
        knob values quoted below are that of the 2026-08-22 artefact and no
        longer describe the shipped file. `assets/presets/fitted-c4-mayfly.json` was fitted
        against a renderer whose sympathetic path carried a 1/f0 error of up to
        183x at A0, so its spectrum was tuned around a defect. The re-fit closes
        the widened fence: **all five** enforced thresholds in
        `assets/thresholds/c4.json` were TIGHTENED in the same pass, none
        loosened, and `spectral_rmse_db` is back below the 57.5 that stood
        before the resonator normalisation.

        | metric                | before  | after   | cap 67.0-era → new |
        | --------------------- | ------- | ------- | ------------------ |
        | `score`               | 0.5249  | 0.5182  | 0.57 → 0.559       |
        | `time_rmse`           | 0.10189 | 0.10113 | 0.112 → 0.109      |
        | `envelope_rmse_db`    | 10.156  | 9.593   | 11.75 → 10.34      |
        | `spectral_rmse_db`    | 62.287  | 56.572  | 67.0 → **61.0**    |
        | `decay_diff_db_per_s` | 4.800   | 4.503   | 5.9 → 4.85         |

        Every cap is the measured value plus 7.7–8.0% headroom, the convention
        that file states. The measured 56.572 dB is below the 57.5 fence that
        stood before the resonator normalisation, which is the promise this
        re-fit owed. It does **not** beat the 52.89 dB the same preset measured
        on the pre-normalisation renderer — that comparison was against a
        renderer with the 1/f0 sympathetic defect in it and is not a target. Note
        also that the CAP (61.0) sits above 57.5 purely because the headroom
        convention applies on top of the measurement; the fence is wider than it
        was in 2026-08-21 even though the measurement is better.

        The tracked sidecar `assets/presets/fitted-c4-mayfly.json.report.json`
        was **deleted**, not regenerated. `piano-fit` resumes from
        `<output-preset>.report.json` by default, so leaving the old one in place
        meant an in-place re-run of this preset would silently restore the
        pre-re-fit knobs instead of resuming from what shipped. There is no
        honest replacement to write: this preset is the product of a chain of
        deterministic `--sweep` runs, not of one resumable `piano-fit` search.
        With the file gone, resume degrades to a "resume skipped" notice.

        **All five numbers are measured at `output_gain` 7.096.** The re-fit cut
        the room-IR mix hard, which cut the absolute output level with it; at the
        `output_gain` 1.357 the search compared candidates under, the preset
        rendered 7.2x quieter in RMS (−25.6 → −42.7 dBFS) than the one it
        replaces, and the RMS-normalising gate cannot see that. Re-matching
        `output_gain` puts the render at 0.048274 RMS against the reference's
        0.048364 and costs part of the apparent gain: at 1.357 the same knobs
        measure `score` 0.5040 and `spectral_rmse_db` 51.554, which is an
        artifact of the quiet render and must not be quoted.

        **The dominant term was the wet level of the legacy single-IR (room) stage, not
        the string model.** The preset sets `ir_wav_path`, so the engine loads it as
        the room stage and `ir_wet_mix`/`ir_gain` are room controls, not body-IR ones.
        The preset carried `ir_wet_mix` 1.1888 with `ir_gain` 1.7203, an
        effective wet factor of 2.045. A deterministic 2076-sample sweep of the
        three IR-mix knobs (`--sweep --optimize mix`, 9-point OAT scan plus 2048
        Halton points, report `out/sweep/mix-mayfly.json`) found **887 samples
        that improve all four gated raw metrics at once** — a region, not a
        lucky sample. The re-fit sits at `ir_wet_mix` 0.2328 / `ir_gain` 1.0912
        / `ir_dry_mix` 0.2107, a wet factor of 0.254. Three further knobs came
        from pass sweeps around that point: `high_freq_damping` 0 → 0.05 and
        `unison_crossfeed` 0.00236 → 0.0025 (sustain box),
        `per_note.60.strike_position` 0.2945 → 0.45 (inharmonicity box).
        `resonance_gain` was not touched and stays at 0.00025.

        **Selected on `balanced-v2`, deliberately not on `legacy-v1`.**
        `legacy-v1` saturates its spectral component and puts no weight on
        partial level, partial frequency, tristimulus, attack or the segmented
        decay, so a search steered by it pays those six to buy the four it sees.
        Measured, not assumed: continuing the sweep chain reached `legacy-v1`
        0.4654 with `spectral_rmse_db` 48.1 and `decay_diff_db_per_s` 1.54 —
        better on every gated metric than what shipped — while pushing
        `partial_level_rmse_db` 10.88 → 24.20 and `attack_centroid_rmse_oct`
        0.544 → 2.445. Rejected as gate-gaming. The shipped point is the
        `balanced-v2` optimum of the same search (0.47391 → 0.42687), with no
        sampled direction around it improving `balanced-v2` by more than 0.0002.
        Its one real cost is `attack_centroid_rmse_oct` 0.544 → 0.947, a direct
        consequence of removing the body colouration from the onset; it is not
        enforced and is recorded in that file so the trade stays visible.

        **Stochastic `piano-fit` runs were tried first and lost.** Six 300 s
        Mayfly runs from the old preset (`legacy-v1` seeds 1/7/13/23,
        `balanced-v2`, and the `attack` pass) all either regressed `time_rmse`
        past its cap or failed to beat the deterministic sweep; the best,
        `legacy-v1` seed 1, reached `score` 0.4937 with `spectral_rmse_db` 47.5
        but `time_rmse` 0.1158 against a 0.112 fence.

  - [x] **`piano-fit --match-output-gain` is now score-neutral.** It was not:
        the auto-stop was an **absolute** −90 dBFS threshold, so a louder render
        crossed it later, produced a longer candidate and scored differently
        (measured 2026-08-22 on one fitted preset, same knobs otherwise:
        `output_gain` 7.096 scored 0.5208, 1.357 scored 0.5061), and every
        fitted preset in the C4 re-fit round had to be re-measured at the
        tracked preset's `output_gain` because of it. Fixed by making the stop
        threshold N dB below the render's **own running peak**. The detector
        lives in `internal/render` and is shared by `piano-fit`,
        `piano-distance`, `piano-modal-fit` and `piano-render`, which had four
        copies of the same loop; `--decay-relative=false` restores the absolute
        threshold so pre-change numbers stay reproducible, bit for bit. The
        renders got slightly longer, so `assets/thresholds/c4.json` was
        re-baselined in the same PR: four of five caps LOOSENED, the deltas are
        in that file's `recorded.note`. Re-fitting the C4 preset against the new
        (longer) scoring window is the honest follow-up.
  - [x] follow-up: **the Halton sweep caps at 8 dimensions.** _Done:_ the base
        table now holds the first 32 primes and `--sweep-joint-max-dims`
        defaults to 16, so the 9-knob `attack` box runs joint. The cap stays a
        deliberate guard rather than an accident of the table length: this
        Halton implementation does not scramble, so high-base coordinates are
        poorly distributed until the sample count grows large.
        _Evidence._ `--sweep --pass attack` at note 60 with
        `--sweep-samples 9 --sweep-joint-evals 2048` now completes — 2130 evals,
        0 errors, 342 s, 2048 Halton points over the 9-D box — where before it
        aborted with `halton: 9 dimensions exceed the 8-prime base table`.
        Baseline `attack-v1 = 0.3923` / `legacy-v1 = 0.5121`; 14 Pareto points;
        constrained best #1237 `attack-v1 = 0.3416` / `legacy-v1 = 0.5030`.
        That is a measurement of the tool, not a fitted preset — nothing was
        applied or gated, and it was measured on the pre-relative-auto-stop
        renderer.
        _Regression guard._ `just sweep-sustain-c4` (5-D, primes 2/3/5/7/11) was
        run before and after the change: the two 16 860 704-byte reports are
        identical except for the wall-clock `elapsed_seconds` field (164.288 s
        vs 150.887 s), which cannot match by construction. With that field
        removed both reports hash to the same SHA-256, so extending the table
        moved no sample.
  - [ ] follow-up: **recover sympathetic resonance level.** The normalisation is
        correct but it removes the mechanism that made sympathetic resonance
        audible, and a scalar cannot bring it back. The loss is **not** a flat
        figure: it is the old bank's peak gain, so it is register-dependent and
        heaviest exactly where the runaway was — −45.3 dB at A0, −38.0 at MIDI
        36, −26.1 at C4, −14.2 at MIDI 84, −8.3 at MIDI 96. That shape is the
        argument that the change is correct rather than merely quieter. The web
        client is unaffected either way: `cmd/piano-wasm` builds from
        `NewDefaultParams()`, where resonance is off. Measured 2026-08-22, DWG,
        pedal held, six notes struck, peak of the last 5 s of a 120 s render
        through `Piano.Process`: `ResonanceGain` 0.00018 is flat (1.75), 0.0007
        creeps up (2.42), 0.0014 diverges (91.5 and climbing). The loop's
        stability ceiling is therefore around 3x the current default, against
        the 8-20x that restoring mid-register level would need. Getting the
        level back needs a change to the loop itself — wider resonator
        bandwidths, more partials, or per-target injection scaling — not more
        gain.

        **Partly paid, 2026-08-22, by the interleave above — and NOT closed.**
        Interleaving is exactly the "change to the loop itself" this item asked
        for, and it recovered **13.3 dB (DWG) / 13.0 dB (modal)** of sympathetic
        level with no change to `ResonanceGain`, measured by differencing a
        render with a silently-held note 72 against one without it. That is half
        of the −26.1 dB the normalisation cost at C4, taken back without touching
        a scalar, which is what "a scalar cannot bring it back" predicted.

        The rest was then BLOCKED on stability rather than on level, because the
        interleaved DWG loop grew 5.06x over 120 s at the shipped gain on top of
        a bank that already grew 1.21x with no sympathetic path at all. **That
        blocker is gone as of 2026-08-23**: the unison coupling was the source
        of both, and with it corrected the same render decays to 0.1275x
        (resonance off) and 0.1338x (resonance on at 0.00025).

        **The headroom is back, and it is measurable.** Same 120 s render,
        sweeping `resonance_gain`: 0.00025 → 0.1338, 0.0007 → 0.2989, 0.00092 →
        0.7836, 0.0014 → 14.02. The unity crossing lands at roughly 0.00092 —
        which is exactly where the pre-interleave notes had put it — so the
        shipped 0.00025 sits **3.7x under the ceiling**, worth about +11 dB
        before stability becomes the binding constraint again. Together with the
        13.3 dB the interleave already recovered, that covers most of the
        −26.1 dB the normalisation cost at C4.

        **This item still is not done, and must not be closed by turning the
        knob up.** Three things have to be settled first. (1) 0.00092 is a
        measured cliff on ONE render — six notes, one velocity, coupling off —
        and the margin a shipped preset needs is a separate question from where
        the render diverges. (2) `assets/thresholds/c4.json` records an explicit
        REJECTION of re-voicing `resonance_gain` to buy back
        `spectral_rmse_db`, and that rejection was on spectral grounds, not
        stability grounds, so it survives this. (3) Every shipped preset would
        have to be re-fitted afterwards, which is the third such debt in this
        phase. The honest next step is a stability margin study across
        registers and velocities, not a scalar change.

  - [x] follow-up: **the open-loop probes in `piano/modal_resonance_test.go`
        read a transient, not a steady state.** Fixed 2026-08-22 by replacing
        the 0.5 s warmup and the cumulative average with a windowed probe:
        `measureResonanceLoopGain` now reports the mean of the LAST second of
        drive, stops as soon as that mean moves by less than 0.2% against the
        second before it, and returns whether it settled or ran out of its 24 s
        budget. An unsettled reading is labelled a lower bound in the log line
        and in the recorded tables, so the file no longer claims a steady state
        it did not measure.
        **Ground truth.** The diverging row (DWG, `ResonanceGain` 0.0014, all 88
        groups undamped, per-note filter on, note 33) was driven for 400 s: the
        windowed reading is 0.535 at 5 s, 1.027 at 20 s, 1.117 at 24 s, 1.532 at
        100 s and then flat, averaging **1.5299** over the last 50 s. So the
        settled loop gain of a configuration that does diverge is above unity,
        as the small-gain condition requires, and the 24 s budget recovers 0.73
        of it. The loop is linear in `resonance_gain`, so the settled gain is
        1093 × `resonance_gain` and unity falls at **0.00092** — between the
        0.0007 that only creeps up and the 0.0014 that reaches 91.5 in a 120 s
        render. The probe's unity crossing and the renderer's divergence are the
        same event.
        `TestResonanceProbeSeesKnownDivergingLoop` now pins that calibration in
        the suite: it runs the probe on that configuration and fails below 1.0.
        The old probe reported 0.1968 there, which is what made it useless.
        **Every recorded figure re-measured** (48 kHz, hottest note per row,
        0.5 s warmup → 24 s windowed): per-note default/modal 0.000161 →
        0.000161 (settled), default/dwg 0.009118 → 0.165995, calibrated/modal
        0.002082 → 0.002396 (settled); aggregate default/modal 0.0002 →
        0.000190 and 0.0006 → 0.000617, calibrated/modal 0.0016 → 0.001888 and
        0.0059 → 0.005993, default/dwg 0.0237 → 0.143659 and 0.0315 → 0.155013,
        calibrated/dwg 0.0329 → 0.199527 and 0.0438 → 0.215297. The modal rows
        settle inside the budget; the DWG rows do not and stand for settled
        gains of 0.20 to 0.30. The hottest per-note DWG reading also moves from
        note 33 to note 36 once the probe is driven properly.
        **The bound stays at 0.5** and is now justified rather than asserted:
        read through the 0.73 recovery it stands for a settled gain of 0.68,
        under the small-gain limit of 1, and it leaves the hottest covered
        configuration (0.2153) a factor of 2.3.
        **Cost.** The probe subtests were made parallel, which pays for most of
        the longer drive. Back to back on a 12-core machine, `go test ./piano/`
        goes from 107.7 s wall / 104.7 s CPU to 109.5 s wall / 244.2 s CPU:
        **+1.8 s wall, +140 s CPU**. An earlier pair on a quieter machine
        measured 40 s → 75 s wall, so the wall cost is between two and thirty-five
        seconds depending on how many cores are free; the CPU cost is the honest
        figure and it is 2.3x.
        **What did not work.** Aitken extrapolation of the growth curve, the
        cheap alternative, is unusable below about 15 s of drive: the residual
        is not a single decaying exponential until the fast modes have gone, so
        triples taken at 1-10 s extrapolate to -0.61, 0.90 and 14.39 against a
        true 1.53. Deriving the gain analytically was not attempted - the loop
        runs through `StringBank.Process` and the transfer function is not
        reachable from the coefficients.
  - [x] follow-up: make `cmd/piano-modal-fit` disable resonance during fitting
        the way `cmd/piano-fit/main.go:212-214` does.
        `assets/presets/modal-calibrated.json` was very likely fitted against
        diverging renders, which would explain why `analysis/norms.go:55`
        excludes it as a degenerate outlier.
        `cmd/piano-modal-fit` now takes the same `--no-resonance` flag with the
        same save/restore semantics as `cmd/piano-fit`: it silences resonance on
        both sides of the match (the DWG references and the modal candidates are
        rendered from the same base params) while `finalizeOutputParams`
        restores the input preset's own `ResonanceEnabled` before the preset is
        written, so a staged run cannot leak resonance-off into the output. The
        default is `false`, matching `cmd/piano-fit`: with the divergence
        defects fixed (#23, #26) a resonance-on fit is sound again, and a
        differing default between the two fitting tools is exactly the drift
        this follow-up was about. Pinned by
        `TestFinalizeOutputParamsRestoresPresetResonance` and
        `TestWritePresetResonanceRoundTrip` in
        `cmd/piano-modal-fit/main_test.go`.
  - [ ] follow-up: **re-fit `assets/presets/modal-calibrated.json`.** The tool is
        now capable of a clean fit, but the shipped preset is still the one
        produced against diverging resonance renders, and `analysis/norms.go:55`
        still has to exclude it as a degenerate outlier. Re-fitting it is its
        own piece of work: the new preset changes the norm corpus, so the
        exclusion and the normalisation constants have to be re-derived together
        with it. The exclusion stays exactly as it is until then.

- [ ] Add benchmarks:
  - [x] DWG vs modal CPU at fixed block size/sample rate
        (`BenchmarkStringBankStringModels` in `piano/modal_bench_test.go` runs
        both cores over the shared benchmark cases at 48 kHz and a 128-frame
        block with coupling on. The DWG-vs-modal figures it produces are
        tabulated in BENCHMARKS.md "Polyphony scaling" and "Voice cost per block
        and polyphony sweep", which measure the same pair at the same block size
        and rate; the coupled variant itself has no separate table.)
  - [x] polyphony scaling comparison
        (covered twice: `BenchmarkModalPolyphonyScaling` in
        `piano/modal_bench_test.go`, tabulated in BENCHMARKS.md "Polyphony
        scaling"; and `BenchmarkStringBankVoiceCostPerBlock` in
        `piano/polyphony_bench_test.go`, tabulated in BENCHMARKS.md "Voice cost
        per block and polyphony sweep". Both cores, 1-130 voices — they are
        within noise of each other.)
  - [x] memory footprint comparison
        (`BenchmarkStringBankRetainedHeap` in `piano/memory_bench_test.go`
        measures the retained **live Go heap** of a constructed 88-key bank via
        `runtime.ReadMemStats`. **A full bank retains 324 KiB (DWG) against
        281 KiB (modal at the default 8 partials) — modal is 13% smaller**, and
        the modal figure is linear in `modal_partials` at ~11.7 kB per partial,
        crossing DWG at ~13 partials. This is `HeapAlloc` after a forced GC, not
        RSS: it excludes heap-span slack and fragmentation, allocator metadata,
        stacks and other non-heap memory, so it compares the two cores' data
        structures and does not serve as a resident or plugin memory budget.
        Recorded in BENCHMARKS.md "Retained heap".)
- [ ] Define shipping rule:
  - [ ] “low CPU” profile defaults to modal core
  - [ ] “high accuracy” profile defaults to DWG core
  - Still open, and `PIANO-406` below holds the current state of the evidence.
    One thing about the rule's shape is now settled by measurement: a “low
    CPU” profile must **not** be implemented by lowering `modal_partials` —
    the knob's fixed-cost floor leaves too little CPU on the table for it to
    be worth doing, independent of what the quality cost turns out to be.

### 12.5 — Upstream SIMD integration follow-up (`algo-dsp` + `algo-vecmath`)

Goal: adopt SIMD modal/quadrature oscillator kernels into `algo-piano`, with clear validation gates.

**Upstream reality check (2026-08-16).** The original gate assumed `algo-dsp`
would ship a modal oscillator layer. It has not, and the `DSP-201..DSP-207`
ticket IDs never existed upstream — the real item is `algo-dsp` **Phase 41 —
SIMD Modal Oscillator Bank**, still _Planned_, with no `dsp/osc` or `dsp/modal`
package in v0.7.0. `algo-piano` therefore calls `algo-vecmath` directly.

`algo-vecmath` v0.1.3 state:

- `VEC-301..VEC-305` — done and verified (`RotateDecayComplexF32`,
  `RotateDecayAccumulateF32`, generic + AVX2 + SSE2 backends).
- `VEC-306` (arm64 NEON) — **marked done upstream but not implemented.** arm64
  falls back to the generic scalar kernel, so no SIMD gain applies there.
- `VEC-307` — partial (no `RotateDecay` baseline table).
- `VEC-308` (denormal/long-tail tests) — open.

- [x] `PIANO-401` — Upstream readiness tracked and versions pinned:
      `algo-vecmath` audited (301–305 usable, 306 missing, 307 partial, 308
      open), `algo-dsp` Phase 41 confirmed not started, `go.mod` updated to
      `algo-approx` v0.2.0, `algo-dsp` v0.7.0, `algo-fft` v0.8.0, `algo-pde`
      v0.2.2 and `algo-vecmath` v0.1.3 (now a direct dependency).
- [x] `PIANO-402` — Adapter layer: `ModalStringGroup` mode state converted to
      flat SoA (`piano/modal_group.go`), existing modal knobs preserved, scalar
      fallback kept behind `modalKernelMode` / `modalArenaEnabled`.
- [x] `PIANO-403` — SIMD kernels replace the per-mode scalar update loop,
      batching across **notes** via `piano/modal_arena.go` (one kernel call per
      sample over all active groups), with no extra per-block allocations and
      unchanged coupling/resonance semantics.
- [x] `PIANO-404` — Parity gate: bit-exact across scalar/accum/rotate/arena
      (`piano/modal_parity_test.go`), sustain and damped/undamped transitions
      covered, long renders NaN/Inf free with the denormal spin fixed.
- [x] `PIANO-405` — Performance gate: modal CPU **−26.9%** (8 notes) and
      **−21.4%** (86 notes) vs the pre-refactor baseline, DWG vs modal
      benchmarked (caveat under `PIANO-406`), results recorded in
      `BENCHMARKS.md` with machine, Go version and date.
- [ ] `PIANO-406` — Shipping profile decision refresh.
  - [ ] **Blocked on a real finding, and the finding itself is now in doubt:**
        the recorded polyphony sweep has the modal core _not_ cheaper than DWG
        at the default 8 partials — 6% to 25% more expensive, which is why the
        "low CPU profile defaults to modal" rule in 12.4 could not be adopted.
        Re-measuring that same unmodified benchmark on 2026-08-22 on a quiet
        machine (load average 1.3-4.2, median of seven independent invocations)
        reverses it: modal is **36% to 39% cheaper** than DWG at 16, 32, 64 and
        130 voices. The recorded run was taken at load average 8 to 13, and
        under that much load the benchmark demonstrably cannot resolve a 25%
        effect — twelve busy loops pinned against it produce an 8x spread inside
        a single case. So the recorded conclusion is not supported by the
        conditions it was taken in. What is _not_ explained is why the sign
        flips rather than merely blurring; load alone should inflate both cores
        similarly. Until that is understood, this box stays open: reversing a
        shipping rule on a number whose disagreement with the previous number is
        unexplained is not better than leaving the rule unadopted. See
        BENCHMARKS.md "Modal partials: quality vs CPU", last subsection.
  - [ ] determine how far `modal_partials` can drop before quality suffers, then
        re-evaluate the mapping on measured numbers
        (**Measured 2026-08-22**, `TestModalPartialsQualityCurve` in
        `piano/modal_partials_test.go` and `BenchmarkModalPartialsSweep` in
        `piano/modal_bench_test.go`, both sweeping 1/2/3/4/5/6/8/12/16/24
        partials; tabulated in BENCHMARKS.md "Modal partials: quality vs CPU".
        **The CPU half of this box is answered; the quality half is not, and
        this box was briefly marked closed on the strength of an overclaim that
        PR #34 review caught.** What _is_ established: the CPU curve is not flat
        in `modal_partials` — it is roughly affine, about 28 ns per active
        string per block per partial on a ~280 ns floor, so ~55% of the cost at
        8 partials is fixed and no reduction can reach it. Dropping 8 to 4 buys
        only 19% (uncoupled) or 23% (coupled) of the string-bank block cost and
        8 to 1 buys 38-40%, so the knob cannot deliver a substantial CPU saving
        whatever quality one is willing to pay. What is **not** established: the
        quality curve is smooth and monotone all the way down, with no knee
        below the shipped default of 8 — 8 to 4 costs 13.8 dB of
        `partial_level_rmse_db` at C4 against the same core at 32 partials and
        8 to 1 costs 26.9 dB — but that only shows the renders _differ_ from the
        32-partial reference. Nothing here shows the difference is audible or
        unacceptable: there is no listening test, and `partial_level_rmse_db`
        has no calibrated acceptance threshold in this repo
        (`assets/thresholds/c4.json` lists it as `null`, and its numbers are
        regression fences rather than quality targets). A previous revision of
        this box compared the 13.8 dB against the 0.84 dB of remaining
        _envelope_ headroom in the C4 gate; that is a comparison between two
        different metrics and establishes nothing. It has been removed. Two
        further limits, both real: the "quality" axis is distance from the same
        core at 32 partials, not absolute quality — nothing here says the
        32-partial render is right; and `analysis` tracks at most 16 harmonics
        (`analysis/features.go`), so a difference confined entirely above the
        16th harmonic is invisible to `partial_level_rmse_db` — harmonics 9-16
        and the full-spectrum metrics do remain sensitive. The DWG-referenced
        score was measured too, as 12.4 asks, and is useless for this: it moves
        0.004 across a 24x change in partial count because it saturates
        spectrally, and at C2 it gets _worse_ as partials are added, since a
        richer modal spectrum diverges further from DWG's sparse impulse train.)
    - [ ] follow-up: calibrate an acceptance threshold for
          `partial_level_rmse_db`, or run a listening test, so the quality half
          of this box can be closed on evidence rather than on a difference
          measurement with no scale attached.
  - [ ] keep DWG profile as high-accuracy reference for regression checks
  - **Retained heap is now measured** (2026-08-22,
    `BenchmarkStringBankRetainedHeap`, BENCHMARKS.md "Retained heap"), so the
    missing memory input to this decision is closed: a full bank retains 324 KiB
    of live heap (DWG) vs 281 KiB (modal at 8 partials), and the modal figure is
    linear in `modal_partials`. That is `HeapAlloc` after a forced GC, not RSS,
    so it compares the two cores rather than sizing a plugin budget — but the
    gap between them is small on either reading, so memory does not decide the
    profile. Re-run on 2026-08-22 for the partials sweep and reproduced exactly.
  - **Where the 12.4 shipping rule stands after this.** Not adopted, and the
    reason has changed. It is no longer blocked on "modal might be slower":
    on every measurement taken on a quiet machine modal is the cheaper core, by
    36-39% uncoupled and by 37% with coupling on at 8 partials. It is blocked on
    the fact that this contradicts the figure recorded a day earlier and the
    contradiction is unexplained. The partials sweep settles one thing about the
    rule's _shape_, and it does so on the CPU axis alone: a "low CPU" profile
    must not be implemented by lowering `modal_partials`, because the knob's
    fixed-cost floor means there is not enough CPU there to be had. The quality
    half of that sweep is _not_ closed — the lower-partial renders demonstrably
    differ from the 32-partial reference, but no calibrated threshold or
    listening test says whether the difference matters. If the rule is adopted it is
    adopted as "low CPU ⇒ modal core at the default 8 partials", with the
    partial count left alone.
    - [ ] follow-up: re-measure "Voice cost per block and polyphony sweep" in
          BENCHMARKS.md under the same discipline used for the partials sweep
          (separate invocations, quiet machine, load average recorded) and
          either correct the recorded table or explain the disagreement. That is
          the one remaining input to the profile decision.

**Lesson worth keeping.** Calling `algo-vecmath` once _per note_ was measured
**slower** than the scalar loop it replaced (+8% to +11% at high polyphony): one
note holds only ~24 modes, so per-call overhead beat the vectorization. Batching
all active notes into a single ~1500-mode call is what produced the win. Batch
width is the deciding factor before adding SIMD anywhere else in the voice path.

**Separately fixed:** modal partials decayed into the float32 denormal range and,
because a sustained group never deactivates, stalled there indefinitely (93% of
mode state denormal after 2000 blocks). Flushing inaudible modes at block rate
cut sustained-decay cost from 4.2 ms to 0.59 ms — a larger win than the SIMD work.

**Follow-ups not in scope here:**

- [x] `injectAtPosition` called `math.Sin` per mode per excitation call
      (`piano/modal_group.go`). **Done:** the shape vector is now cached per group
      — one precomputed slot for each of the two fixed strike positions
      (resonance 0.82, coupling 0.9) plus a one-entry cache for the hammer
      position — so the steady-state excitation path evaluates no transcendental
      at all. Output stays bit-exact; see BENCHMARKS.md "Excitation shape cache".
- Revisit if `algo-dsp` Phase 41 ships, or if `VEC-306` lands and arm64 becomes
  a target.

**Done when:** modal core is selectable, calibrated against DWG via an automated matching step, and documented profiles exist for low-CPU vs high-accuracy operation.

---

## Phase 13 — Tests + benchmarks (keep realtime honest)

- [ ] Unit tests
  - [x] Tuning accuracy across a range of notes
        (`piano/tuning_test.go` covers the whole MIDI 21-108 compass with
        per-register tolerances, worst measured 0.64 cents below MIDI 104 and
        3.48 cents in the top five notes.)
  - [x] Convolver correctness bound
        (`piano/convolver_test.go`: direct-convolution comparison at 512 and
        8192 taps for both convolvers, a block-size continuity table, an impulse
        case and a mid-stream `Reset()`, all against a relative bound of 1e-05.
        It closed on a real defect — see below.)
  - [x] Stability tests: long render without NaNs/denorm storms
        (`piano/integration_test.go` NaN/Inf, `piano/denormal_test.go` denormals)
- [ ] Benchmarks
  - [x] Use `go test -bench=.` benchmarks
  - [x] Voice cost per block at 48k/128 frames
        (`BenchmarkStringBankVoiceCostPerBlock`, `ns/voice-block` metric)
  - [x] Convolution cost by IR length/partition size/caller block size
        (`piano/convolver_bench_test.go`; BENCHMARKS.md "Partition size became a
        lever, and callback alignment became the bigger one")
  - [x] Polyphony sweep (e.g. 16/32/64/128 voices)
        (`BenchmarkStringBankVoiceCostPerBlock`; a voice is one sounding string,
        and the sweep stops at MIDI 91 to stay clear of the DWG treble collapse
        below, so the 128-voice case is 130 strings over keys 36-91)

**Resolved — convolver non-`partSize` block handling (2026-08-22).** Writing the
correctness bound turned up a defect rather than confirming a bound. Both
`SoundboardConvolver.Process` and `BodyConvolver.Process` sliced the input into
128-sample partitions and zero-padded a trailing short block up to 128 before
handing it to `dspconv.StreamingOverlapAddT.ProcessBlockTo`, which accepts
exactly `blockSize` samples. The output samples of that block were right — a
zero-padded tail cannot corrupt the outputs before it — but the overlap-add
state advanced by a whole partition, so `partSize - blockLen` zeros were spliced
into the stream and every later sample convolved the wrong signal. Successive
`Process` calls of a non-128 length were therefore not the convolution of the
concatenated input.

Measured by streaming a 1000-sample sine through a 512-tap decaying IR in fixed
chunks and comparing against `directConvolve`, reference peak 15.92:

| chunk | before (max abs) | after (max abs) |
| ----- | ---------------- | --------------- |
| 1     | 1.57e+01         | 5.72e-06        |
| 63    | 1.04e+01         | 5.72e-06        |
| 64    | 9.72e+00         | 7.63e-06        |
| 100   | 2.47e+01         | 5.72e-06        |
| 128   | 7.63e-06         | 7.63e-06        |
| 256   | 7.63e-06         | 7.63e-06        |
| 333   | 6.60e+00         | 7.63e-06        |

The 7.63e-06 floor is float32 round-off through the FFT, 4.8e-07 relative.
Nothing caught this because every renderer in the tree drives the convolvers at
128 (`cmd/piano-render`, the fit render path, the AudioWorklet render quantum)
and emits a short block only as the very last one, whose own output is still
correct. It was reachable through the pinned public API — `Process(1)` and
`Process(64)` in `piano/api_compat_test.go` — and through
`cmd/piano-fit --render-block-size`, which accepts values down to 16.

The fix (`piano/convolver_stream.go`) feeds the overlap-add stage complete
partitions only. Samples the caller asks for before their partition is complete
are computed the zero-latency way instead, by splitting the convolution into a
direct `h[:128]` head over the retained input history plus a second overlap-add
stage carrying `h[128:]`, which can run a partition ahead because it only reads
committed input. That keeps `Process(n)` returning `n` samples with no added
latency, which `piano/radiation_test.go` pins. The `partSize`-aligned path is
untouched and byte-identical: rendering `assets/presets/fitted-c4-mayfly.json`
(note 60, velocity 118, release-after 3.5, 48 kHz) with `cmd/piano-render` built
from before and after the change produces identical WAVs, so
`assets/thresholds/c4.json` and every tracked report stay valid.

Re-measuring the convolver benchmarks against the corrected code flipped a
documented conclusion: with the stream correct at every partition size, raising
`partSize` above 128 now cuts the one-second room IR from 0.91 ms to 0.40 ms per
128-frame callback at 1024, because a partition larger than the callback is
amortized instead of discarded. The new cost to watch is caller alignment — a
call size that is not a multiple of `partSize` costs 3.05x on that room path.
BENCHMARKS.md carries both tables and retracts the two passages that said the
stream was unfixably wrong above 128 and that cross-callback buffering would cost
a partition of latency.

**Resolved — DWG treble collapse (2026-08-21).** Extending tuning coverage past
MIDI 92 turned up two independent defects in the DWG core. Both are fixed;
`TestTrebleRegisterIsStableInDWGCore` (formerly the skipped
`TestTrebleRegisterCollapsesInDWGCore`) is now the regression test.

- **Bit-exact silence at MIDI 106-108.** The delay line is allocated four slots
  longer than the integer delay and the interpolating taps read at the far end
  of that headroom, so a slot written closer than three slots ahead of the write
  pointer is overwritten before any tap reads it. Those notes have 16, 16 and 15
  slot delay lines and the default strike position of 0.18 lands on offset 2, so
  every bit of hammer energy was discarded. `StringWaveguide.injectionOffset`
  now clamps injections into the observable range. MIDI 100-105 were affected
  too, less visibly: offset 3 is read by the fractional tap only, so those notes
  received a `frac`-weighted fraction of the hammer force.
- **DC runaway from ~MIDI 96.** The loop filter has unity DC gain, so the loop
  is a leaky integrator for DC, and unison crossfeed injects into every string
  every sample while a top-octave loop only loses `1-reflection` per round trip
  about 4000 times a second. `StringWaveguide.processDCBlock` now removes DC
  inside the loop, which is what a real string does. Its phase lead sharpens
  every string by a flat `fc/(2*pi)` Hz — +19.6 cents at A0 — so
  `dcBlockPhaseDelay` adds the blocker's own phase delay back into the delay
  line; measured residual after compensation is 0.64 cents worst case.

Two things found on the way that contradict the earlier reading of this finding:

- The DC was not what made the DWG-vs-modal distance bad (see 12.4).
- The C4 regression gate was calibrated against a render carrying that DC as a
  first-order component. At the source — raw string-bank output, before
  `output_gain` and the body IR — the offset was mean +0.606 against an AC RMS
  of 0.376, a ratio of 1.61; on the rendered candidate that `analysis.Compare`
  actually scores it was +0.0325 against 0.0652, a ratio of 0.50 and 44.6% of
  total RMS, against 0.15% in the reference. Removing it
  improves `time_rmse` and `spectral_rmse_db` and worsens `envelope_rmse_db` and
  `decay_diff_db_per_s`, because a non-decaying offset had been propping the
  envelope up. `assets/thresholds/c4.json` records the whole before/after and
  says which two thresholds were loosened and why. Re-fitting the C4 preset
  against the corrected core is the outstanding follow-up.

**Done when:** you have a baseline performance budget and regression alarms.

---

## Phase 14 — Polish (only after the core is solid)

- [ ] Add key-off / pedal noise (small synthesized bursts or tiny samples)
- [ ] Add output limiter/safety clipper
- [ ] Improve dispersion/loss mapping across the keyboard

---

## Phase 15 — Deferred refinements (moved from earlier phases)

These items were open inside otherwise-complete phases. They are unchanged in
scope, only split into smaller steps so they can be picked up independently.

### 15.1 — Weak bridge coupling for double decay (from Phase 4)

- [ ] Add weak inter-string coupling at the bridge inside a unison group in the
      DWG core (beyond the existing output crossfeed)
- [ ] Add the equivalent coupling in the modal core so both cores agree
- [ ] Expose coupling strength as a preset parameter with a safe default
- [ ] Add a test measuring the two-stage (prompt/aftersound) decay envelope

### 15.2 — Non-uniform partitioned convolution (from Phase 5)

- [ ] Small early partitions to cut convolver latency
- [ ] Larger late partitions for throughput on long IRs
- [ ] Correctness test against direct convolution at the existing error bound
- [ ] Benchmark against the uniform scheme at equal IR length and block size

### 15.3 — Complete the `Params` schema (from Phase 8)

- [ ] Unison block: per-note detune map and per-string gains
- [ ] Global block: IR set selection and output gain
- [ ] Global block: optional limiter settings
- [ ] Preset round-trip test (load → serialize → load) covering the new fields

### 15.4 — Offline fitting helpers (optional, from Phase 8)

- [ ] Helper that fits decay times from recordings to loss parameters
- [ ] Helper that fits inharmonicity targets from recordings to dispersion
- [ ] CLI entrypoint wiring both helpers with reproducible output artifacts
- [ ] Document the workflow alongside the existing fitting tools

### 15.5 — Shared global priors for modal fitting (from Phase 12.3)

- [ ] Define a register-wise prior parameterization for mode gain/damping
- [ ] Fit per-note deviations against the priors to cut parameter count
- [ ] Verify fit quality does not regress against the current per-note profiles

---

## Open decisions (resolve early)

- [ ] Decide: primary string core for v1
  - [ ] DWG (matches `goal.md`)
  - [ ] Modal bank (supported by `research.md` for stability/alias control)
