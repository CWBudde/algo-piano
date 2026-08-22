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
        RMS-normalises both signals, so `output_gain` is provably
        score-invariant and searching it would burn budget on a flat dimension.
        `--match-output-gain` (default on) solves it analytically after the
        search instead, and it is deliberately excluded from the polish knobs.
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
        one seed, one search strategy, found no non-regressing candidate. A
        different budget, seed, or a wider knob surface could still find one.
        Before concluding the string model itself cannot match the reference
        decay without breaking its spectrum, the next step is a
        parameter-sensitivity or Pareto sweep over the sustain knobs — that is
        what this box is now waiting on.)
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
      in `web/`. Found and documented one real defect: modal core with
      `ResonanceEnabled` diverges to NaN after ~0.29 s, so that row is skipped.)
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
  - [ ] long-render stability (NaN/Inf free)
        (partially covered by `TestLongRenderHasNoNaNOrInf` in
        `piano/integration_test.go`, see Phase 9.6; still open here because the
        modal core with `ResonanceEnabled` diverges and that row is skipped)
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
  - [ ] **Blocked on a real finding:** the modal core is _not_ currently cheaper
        than DWG at the default 8 partials — the two are within noise across the
        polyphony sweep. The "low CPU profile defaults to modal" rule in 12.4
        cannot be adopted on the assumption that modal is faster.
  - [ ] determine how far `modal_partials` can drop before quality suffers, then
        re-evaluate the mapping on measured numbers
  - [ ] keep DWG profile as high-accuracy reference for regression checks
  - **Retained heap is now measured** (2026-08-22,
    `BenchmarkStringBankRetainedHeap`, BENCHMARKS.md "Retained heap"), so the
    missing memory input to this decision is closed: a full bank retains 324 KiB
    of live heap (DWG) vs 281 KiB (modal at 8 partials), and the modal figure is
    linear in `modal_partials`. That is `HeapAlloc` after a forced GC, not RSS,
    so it compares the two cores rather than sizing a plugin budget — but the
    gap between them is small on either reading, so memory does not decide the
    profile. This item stays open on the quality question above.

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
  - [ ] Convolver correctness bound
  - [x] Stability tests: long render without NaNs/denorm storms
        (`piano/integration_test.go` NaN/Inf, `piano/denormal_test.go` denormals)
- [ ] Benchmarks
  - [x] Use `go test -bench=.` benchmarks
  - [x] Voice cost per block at 48k/128 frames
        (`BenchmarkStringBankVoiceCostPerBlock`, `ns/voice-block` metric)
  - [x] Convolution cost by IR length/partition size
        (`piano/convolver_bench_test.go`)
  - [x] Polyphony sweep (e.g. 16/32/64/128 voices)
        (`BenchmarkStringBankVoiceCostPerBlock`; a voice is one sounding string,
        and the sweep stops at MIDI 91 to stay clear of the DWG treble collapse
        below, so the 128-voice case is 130 strings over keys 36-91)

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
