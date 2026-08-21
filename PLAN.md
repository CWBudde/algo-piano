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

> **Recorded scores from before 2026-08 are NOT comparable.**
> `analysis/distance.go` changed six times between 2026-02-14 and 2026-08-16
> (phase detection, normalisation), so every score written into a report before
> that window was produced by a different metric implementation. This applies to
> the `0.6147` Phase 8A baseline above and to the `best_score 0.3839` recorded in
> `assets/presets/fitted-c4-mayfly.json.report.json` alike — re-measure, never
> compare across that boundary. The canonical current number is below, and
> `docs/plans/2026-08-21-phase8b-metrics.md` has the detail.
>
> **Current C4 baseline (re-measured 2026-08-21):**
> `assets/presets/fitted-c4-mayfly.json` vs `reference/c4.wav`, note 60,
> velocity 118, release-after 3.5 s, 48 kHz, decay-dbfs -90, decay-hold-blocks 6,
> min-duration 2.0, max-duration 30 — score `0.5194`, similarity `12.52%`,
> time `0.1104`, envelope `9.008 dB`, spectral `58.18 dB`, decay slope
> `3.177 dB/s`. Pristine HEAD and the working tree agree to the last digit
> (`0.5194351732385747`), so this is the metric's current definition and not a
> regression.

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
      `just fit-c4-passes` runs all three and ends with `legacy-v1` distance
      reports, the only numbers comparable across passes; its final artifact chains
      `attack` → `inharmonicity` and leaves the regressing `sustain` pass out.
      Measurements below.)
  - [x] Attack pass: fit hammer hardness/contact settings to reduce early-window spectral error
        (180 s from `fitted-c4-mayfly.json`, `--pass-window 0:0.35`: legacy score
        `0.5194` → `0.5117`, attack centroid error `0.440` → `0.084` octaves. The
        one pass that is a net win today.)
  - [ ] Sustain/decay pass: fit loss/damper behavior to match decay slope and envelope shape
        (**runs and converges, but regresses the comparable score — do not chain
        it into a shipping preset yet.** It improves exactly what `decay-v1`
        weights, segmented decay RMSE `14.58` → `12.84` dB/s, and pays with
        spectral RMSE `58.0` → `66.9` dB and partial-level RMSE `14.0` → `27.4` dB,
        for a legacy score of `0.5581`. Cause is the saturated `NormSpectral`
        below, not the pass machinery: at both 58 dB and 67 dB the spectral term
        normalises to exactly 1.0, so it is a constant in `decay-v1` and exerts no
        restoring force, while partial level carries weight 0 there. Blocked on
        the `NormSpectral` recalibration.)
  - [ ] Inharmonicity pass: fit dispersion/inharmonicity via partial-frequency error
        (runs; score-neutral from the attack-pass preset, `0.5117` → `0.5121`,
        partial-frequency RMSE `34.9` → `34.5` cents. Its three knobs have too
        little leverage at C4 to close a 35-cent gap — the knob bound/scaling
        issue recorded in the Phase 8B design notes still needs addressing before
        this can be ticked.)
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

**Not yet calibrated:** `NormSpectral = 30.0` is saturated for every preset in
the repo (measured 51.5-63.7 dB, plus a degenerate 172 dB outlier), so the
largest-weight component of `legacy-v1` provides the optimizer no gradient at
all. The gate checks the raw dB value, which is still a real regression signal.
Re-calibrating `NormSpectral` to ~70-80 rewrites every recorded score and so
belongs in a separate, deliberately re-baselined change.

This is no longer only a missed opportunity: the sustain pass above degraded
spectral RMSE by 9 dB and neither its own profile nor the legacy gate could see
it, because a saturated component is a constant. **Recalibrating `NormSpectral`
is now a prerequisite for the sustain pass, not a nice-to-have.**

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
  - [ ] enforce `string-bank bridge mix -> body IR -> room IR`
  - [ ] keep body/room separation first-class in params/presets
  - [ ] keep legacy single-IR path as fallback only
  - [x] complete WASM runtime IR apply (`wasmLoadIR`)
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
- [ ] Add regression tests for API compatibility and long-render stability (no NaN/Inf).
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
        (`piano/core_distance_test.go`. The measured scores are 0.71-0.85 — the
        cores do **not** currently match, see the treble finding under Phase 13)
  - [ ] DWG vs modal distance on chords
  - [ ] sustain pedal and coupling behavior parity checks
  - [ ] long-render stability (NaN/Inf free)
- [ ] Add benchmarks:
  - [ ] DWG vs modal CPU at fixed block size/sample rate
  - [ ] polyphony scaling comparison
  - [ ] memory footprint comparison
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
  - [ ] Tuning accuracy across a range of notes
        (`piano/tuning_test.go` covers MIDI 21-92 with per-register tolerances.
        Left unticked: MIDI 93-108 has no measurable pitch — see the finding
        below.)
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

**Open finding — DWG treble collapse (2026-08-21).** Extending tuning coverage
past MIDI 92 turned up two defects in the DWG core, documented and reproduced by
the skipped `TestTrebleRegisterCollapsesInDWGCore`:

- from roughly MIDI 96 up, the bank output is essentially pure DC and the offset
  grows without converging (note 96 held for 800 blocks goes from +4.8 to +59.2,
  ~50x full scale); zeroing `UnisonCrossfeed` turns growth into decay, pointing
  at the crossfeed path as a positive-feedback route for DC through a loop with
  unity DC gain;
- MIDI 106, 107 and 108 render bit-exact silence.

The modal core shows neither symptom. This also dominates the DWG-vs-modal
distance measured in 12.4, so that bound cannot be tightened until it is fixed.

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
