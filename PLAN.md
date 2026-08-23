# PLAN.md — Implementation plan (bite-sized TODOs, Go-only)

This is the actionable task list derived from `goal.md` (DWG strings + commuted convolution) and `research.md` (stability/contact + validation). It is written to be executed incrementally, with each phase producing something runnable/testable.

Important constraint:

- **All code in this repo is written in Go (Golang).** No C++/CMake-based core.

Conventions used in this plan:

- **MVP path** follows the waveguide + IR-convolution approach.
- Items marked **(optional)** are clearly skippable without blocking the main path.
- Prefer **block processing** (e.g. 64/128 frames) everywhere except where sample-accurate state is required.

---

## Phases 0–10 — Foundation through browser demo ✓

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
- **Phase 8B — Distance-guided timbre matching.** Reproducible C4 fitting via
  `cmd/piano-fit`, with Mayfly search, deterministic coordinate polish,
  analytically matched output gain, aspect-specific profiles/passes,
  calibrated norms, multi-note objectives, raw-metric/score constraints and
  `just gate-c4` regression guardrails. Attack fitting produced the net win;
  sustain and inharmonicity passes moved to **Phase 16**. The workflow and
  measurement history live in `docs/optimization-workflow.md` and
  `docs/plans/2026-08-21-phase8b-metrics.md`.
- **Phase 8C — IR-shape optimization.** `cmd/piano-fit-ir` runs a checkpointed,
  resumable Mayfly outer loop over the full `irsynth.Config`, optionally joined
  with fast piano/mix knobs, and stores fitted IRs plus report sidecars under
  `assets/ir/fitted/`; default-IR selection moved to **Phase 16.3**.
- **Phase 9 — Full-instrument ringing architecture.** Refactored transient key
  and hammer control away from a persistent allocation-free 1–3-string-per-note
  bank; added stable sparse static/physical coupling, instrument-wide physical
  damper and partial-pedal semantics, and the linear
  `string bank -> body IR -> room IR` radiation path with WASM migration.
  Behaviour, API/export compatibility, multi-rate long-render stability and
  coupling cost/density are covered by tests and benchmarks. Body-IR/web checks
  and physical-coupling calibration moved to **Phase 17**.
- **Phase 10 — Web demo.** Go-only WASM engine with stable note/process exports,
  an AudioWorklet bridge, playable two-octave keyboard with sustain and computer
  bindings, scripted WASM build, GitHub Pages deployment, and graceful IR
  fallback.

Remaining non-blocking follow-ups from Phases 4, 5, 8B, 8C and 9 were moved to
**Phases 15–17** so these phases close cleanly.

> **Recorded fitting scores are comparable only within one renderer/metric
> generation.** Use the current `just gate-c4` result against
> `assets/thresholds/c4.json`; historical details and generation boundaries are
> recorded in `docs/plans/2026-08-21-phase8b-metrics.md` §5.

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

#### 11.7 — Optimizer audit ✅ (prerequisite for 11.6)

11.6 spends hours of fitting. Before spending them, the search itself was
measured against trivial controls at equal evaluation budgets, so an optimizer
failure could not be mistaken for a model failure. **Verdict: the optimizer is
fit to run 11.6, and the objective — not the search — is what to fix first.**
Method, matrix and numbers: `docs/optimizer-audit.md`.

- [x] Benchmark harness `cmd/opt-bench`, four cases (5/9/20/39 knobs), five
      seeds, `random` and `halton` controls at equal eval budgets
- [x] Render-free synthetic screening (`OPT_SCREEN=1`, `just opt-screen`)
- [x] A round costs **47.7 evaluations per iteration, not the 20 the derivation
      assumes**, so `--mayfly-round-evals` does not mean what it says and rounds
      truncate around iteration 5 of 12. Fixing the derivation changes search
      behaviour, so it wants a branch that can re-run the matrix.
- [x] The incumbent was never seeded into the swarm. Now `--mayfly-warm-start`;
      largest single win measured on the joint IR fit (0.513 → 0.494)
- [x] `--mayfly-stagnation 15` was bit-identical to baseline — a 12-iteration
      round can never reach a 15-iteration window
- [x] Spectral saturation confirmed on every case: the winner is pinned at the
      clamp under `attack-v1`, `decay-v1` and `legacy-v1` alike, so a fifth to a
      third of the weight carries no gradient. This is model distance, not a
      normalization bug — the calibrated norm is exceeded by about two decibels.
- [x] Halton generator extracted to **`github.com/cwbudde/qmc`** and fixed. The
      private copy did not scramble, so over 39 knobs and 600 points its
      adjacent coordinates correlated at 0.81 and the control was not filling
      its box. `--search halton` now scrambles and burns in; the joint sweep
      keeps the plain sequence by default (`--sweep-joint-scramble` opts in) so
      recorded sweep reports still reproduce bit-for-bit.
- [ ] Re-measure saturation once the 11.1–11.4 model fixes land. If the spectral
      term comes back inside its norm, the search has more signal than anything
      measured here, and the round-length and warm-start items are worth
      re-testing against it.

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

### 12.4 — Validation + performance acceptance ✓ (open items → Phase 18)

The measurement record for this subphase is in
**`docs/plans/2026-08-23-resonance-and-coupling.md`** (cross-core A/B results,
the three energy defects and their fixes, the gate re-baselining history and the
probe calibration) and **`docs/plans/2026-08-23-modal-core-profile.md`**
(benchmarks and the shipping-profile evidence).

- [x] **Cross-core A/B tests.** DWG vs modal distance on single notes (0.83-0.84
      against a 0.89 bound) and on chords (0.8434 / 0.8401 at the same bound, so
      polyphony does not make the cores diverge further); sustain-pedal and
      coupling-mode parity checks (`piano/core_parity_test.go`, relative
      behaviour only — absolute agreement is impossible while the distance sits
      at 0.83-0.84); long-render stability. **The cores still do not match**, and
      the cause is known: the DWG excitation fills only 4-25% of the delay line,
      so the bank output is a sparse impulse train. Closing that needs a
      distributed excitation or a loop filter with real bandwidth.
- [x] **Three energy defects found and fixed** (2026-08-21…23), each of which had
      been masking the next:
  - The sympathetic loop **deposited a whole block into frozen string state**,
    delivering the block's DC content (`|sum x[i]|`) instead of the drive at each
    partial. It is now interleaved with rendering at one-sample delay;
    `injectSample` is the only injection entry point and the outside-the-bank
    forwarders were removed, because closing the loop from outside was the
    defect. Pinned bit-exactly by `TestResonanceLoopIsBlockSizeInvariant`, at
    unchanged CPU cost, and it recovered 13.3 dB (DWG) / 13.0 dB (modal) of
    sympathetic level with no change to `ResonanceGain`.
  - `noteResonator` was built with **a 1/f0 peak gain** (183x at A0, 20.1 at C4),
    which put the aggregate loop over unity in the bass. Normalised to unity peak
    gain; the hottest DWG aggregate drops 60x, again with no change to
    `ResonanceGain`, so no per-source or polyphony normalisation was needed.
  - The **unison bridge crossfeed was a positive feedback loop**: it injected
    each string's own output back into itself at strike position 0.92, growing
    the bank 5.41x over 300 s with no sympathetic path connected at all. The
    force is now `c * g_i * (mix - y_i)` — dissipative by Jensen — and is written
    at the freshest delay slot via `InjectForceNext`, which widens the stable
    range a further 5x. Pinned by `TestUnisonCouplingRemovesEnergy`.
- [x] **The C4 gate was buying 14.7 dB of its spectral score from the
      block-deposit artefact**, and the preset was re-fitted against the
      corrected renderer twice (2026-08-22, then 2026-08-23 for the coupling
      fix). Nothing was loosened in the second pass: `decay_diff_db_per_s` was
      tightened 4.72 → 4.19 and the other four held even though their
      measurements got worse, because the no-loosening rule outranks the
      measured+8% convention. What this **exposed** is Phase 11's known HF
      deficiency with nothing masking it — `spectral_rmse_db` sits at 99% of
      budget with the high band at 82.1 dB. That is a model problem, not a
      fitting one, and no further re-fit will close it.
- [x] **Probe calibration.** `measureResonanceLoopGain` reports the mean of the
      last second of drive and stops once it settles, labelling an unsettled
      reading as a lower bound rather than claiming a steady state it did not
      measure. `TestResonanceProbeSeesKnownDivergingLoop` pins it against a
      configuration that does diverge; the old probe read 0.1968 there, which is
      what made it useless. **The open-loop probes remain necessary, never
      sufficient**: they drive one note's fundamental into a plant they assume is
      stable, and they read 0.174 against a 0.5 bound for renders that were
      growing 5x in two minutes.
- [x] **`cmd/piano-modal-fit` gained `--no-resonance`** with the same
      save/restore semantics as `cmd/piano-fit`, closing the drift between the
      two fitting tools.
- [x] **Benchmarks.** DWG vs modal CPU at fixed block size/sample rate, polyphony
      scaling over 1-130 voices on both cores, and retained heap (324 KiB DWG vs
      281 KiB modal at 8 partials, modal linear in `modal_partials` at ~11.7 kB
      per partial). Memory does not decide the core profile.

Four items are open — the modal crossfeed's non-passive shape, recovering
sympathetic resonance level, re-fitting `modal-calibrated.json`, and the shipping
rule — all moved to **Phase 18**.

### 12.5 — Upstream SIMD integration (`algo-dsp` + `algo-vecmath`) ✓ (`PIANO-406` → Phase 18)

SIMD modal kernels are adopted. The upstream audit, the full benchmark set and
the shipping-profile evidence are in
**`docs/plans/2026-08-23-modal-core-profile.md`**.

- [x] `PIANO-401` — upstream readiness audited and versions pinned. `algo-dsp`
      never shipped its modal oscillator layer (Phase 41 is still _Planned_, and
      the `DSP-201..DSP-207` ticket IDs never existed), so `algo-piano` calls
      `algo-vecmath` v0.1.3 directly. `VEC-306` (arm64 NEON) is marked done
      upstream but is not implemented, so arm64 falls back to the generic scalar
      kernel and gets no SIMD gain.
- [x] `PIANO-402` — `ModalStringGroup` mode state converted to flat SoA
      (`piano/modal_group.go`), modal knobs preserved, scalar fallback kept
      behind `modalKernelMode` / `modalArenaEnabled`.
- [x] `PIANO-403` — SIMD kernels batch across **notes** via
      `piano/modal_arena.go` (one kernel call per sample over all active groups),
      with no extra per-block allocations and unchanged coupling/resonance
      semantics.
- [x] `PIANO-404` — parity gate: bit-exact across scalar/accum/rotate/arena
      (`piano/modal_parity_test.go`), sustain and damped/undamped transitions
      covered, long renders NaN/Inf free with the denormal spin fixed.
- [x] `PIANO-405` — performance gate: modal CPU **−26.9%** (8 notes) and
      **−21.4%** (86 notes) vs the pre-refactor baseline, recorded in
      BENCHMARKS.md with machine, Go version and date.
- [x] Excitation shape cache: `injectAtPosition` no longer calls `math.Sin` per
      mode per excitation, so the steady-state excitation path evaluates no
      transcendental at all. Output stays bit-exact.

**Two lessons worth keeping.** Calling `algo-vecmath` once _per note_ was
measured **slower** than the scalar loop it replaced (+8% to +11% at high
polyphony): one note holds only ~24 modes, so per-call overhead beat the
vectorization. Batching all active notes into a single ~1500-mode call is what
produced the win, and batch width is the deciding factor before adding SIMD
anywhere else in the voice path. Separately: modal partials decayed into the
float32 denormal range and, because a sustained group never deactivates, stalled
there indefinitely (93% of mode state denormal after 2000 blocks). Flushing
inaudible modes at block rate cut sustained-decay cost from 4.2 ms to 0.59 ms — a
larger win than the SIMD work.

Revisit the upstream dependency if `algo-dsp` Phase 41 ships, or if `VEC-306`
lands and arm64 becomes a target.

**Done when:** modal core is selectable and calibrated against DWG via an automated matching step. ✓ — documenting the low-CPU vs high-accuracy profiles is the remaining half and is tracked as **Phase 18.4**.

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

## Phase 16 — Fitting pipeline: remaining passes and IR selection

Open work carried out of Phases 8B and 8C. Nothing here is blocked on Phase 18.

### 16.1 — Sustain/decay pass (from 8B)

The pass fits loss/damper behaviour to match decay slope and envelope shape. It
is **built, constrained, gated and measured — but nothing has been shipped from
it.** The evidence is in `docs/optimization-workflow.md` ("Measured results",
"Sensitivity and Pareto sweep over the sustain knobs", "Constrained sustain
re-fit", "Constraining what the gate measures"), and it stands as follows:

- The unconstrained pass **regresses** the comparable score and breaches
  `time_rmse`, so it stays out of the shipping chain. It buys exactly what
  `decay-v1` weights and pays with time RMSE, spectral and partial level.
- `just sweep-sustain-c4` (2093 deterministic evals: a 9-point one-at-a-time scan
  per knob plus 2048 Halton points over the 5-D box) proved a **non-regressing
  region exists** — 22 of 2092 sampled points, 12 of which also clear the gate
  cap — so the trade-off is a property of the search, not of the knob set or of
  the string model. Sample #17 reaches it by moving a single knob
  (`unison_detune_scale`). Scope, honestly: 2048 samples in 5-D is ~4.6 effective
  grid levels per axis and the region is ~1% of sampled points, so the sweep
  shows the region is non-empty, not that it is large.
- `just fit-sustain-constrained-c4` (a `legacy-v1` floor plus `--gate-thresholds`)
  makes a gate PASS **a property of the method** rather than of how many
  evaluations the wall clock bought. The floor-only twin control — same seed,
  budget, machine and floor, only the raw fence removed — lands 5.4% past the
  spectral cap. The fence is binding, not slack, and the un-gated metrics are
  where the payment shows.

- [ ] Establish whether the constrained+gated recipe beats sweep sample #17 on
      `decay-v1` by a margin that survives across seeds and machines. Two seeds
      on one machine is not a distribution, and the gains measured so far (0.0024
      and 0.0139) are real but small.
- [ ] Decide whether to promote one of these presets. Promotion means replacing
      the tracked gate baseline preset **and** re-baselining
      `assets/thresholds/c4.json` against it — a separate change with its own
      evidence requirements, which must not ride along with a commit that builds
      tooling.
- [ ] Carry forward when re-running: **constrain against the seed you started
      from**, not against the pass baseline. A pre-#30 run with the floor at the
      pass baseline reached a better `decay-v1` and failed the gate on
      `spectral_rmse_db`.

### 16.2 — Inharmonicity pass (from 8B)

- [ ] Give the pass leverage at C4, or retire it on the evidence. Re-run
      2026-08-22 it is marginally **negative** (legacy score 0.5214 → 0.5234) and
      leaves partial-frequency RMSE unmoved at 36.6 cents. Its three knobs cannot
      close a ~37-cent gap at C4; the knob bound/scaling issue recorded in the
      Phase 8B design notes is the blocker. Because it does not help, the
      `attack` → `inharmonicity` chain that `just fit-c4-passes` produces is
      slightly worse than the attack pass alone.

### 16.3 — IR selection (from 8C)

- [ ] Compare the top-K fitted IRs in `assets/ir/fitted/` under multi-note
      validation before selecting a default. Selecting on C4 alone is what this
      item exists to avoid.

**Done when:** the sustain pass either ships behind the gate or is retired on
evidence, the inharmonicity pass has leverage or is retired, and a default
synthetic IR is selected on multi-note validation — with C4 distance and its
sub-metrics stable across the changes.

---

## Phase 17 — Instrument semantics, radiation and web compatibility

Open work carried out of Phases 9.5 and 9.6.

- [ ] **Ship a body IR asset** and give `assets/presets/default.json` a
      `body_ir_wav_path`. Deferred from 9.5: the dual-IR path exists and is
      fenced by `piano/radiation_test.go`, but there is no shipped body IR and
      picking one is a separate modelling decision. Phase 11.5 asks a related
      question — whether the body IR contributes to or compensates for the level
      gap — and the two are worth settling together.
- [ ] **Web/demo compatibility:**
  - [ ] keep the JS/WASM note + pedal API stable.
        `cmd/piano-wasm/export_contract_test.go` pins the export names against
        the `web/` call sites; what is missing is the behavioural half.
  - [ ] verify no UI/playability regressions after the physical pedal semantics
        replaced the timer-based release.
- [ ] **Define a calibration workflow for the physical coupling knobs** against
      multi-note recordings — `coupling_amount`, harmonic falloff, detune sigma,
      distance exponent and max neighbours. The knobs and their behaviour are
      tested (`piano/coupling_behaviour_test.go`) and benchmarked; what is
      missing is a procedure that sets them from recordings rather than by hand.

**Done when:** the shipped default preset renders through a real body IR, the web
demo is verified against the current engine API with no playability regression,
and coupling strength can be calibrated from multi-note recordings.

---

## Phase 18 — Resonance, coupling and core-profile follow-ups

Open work carried out of Phases 12.4 and 12.5. The evidence for every item is in
`docs/plans/2026-08-23-resonance-and-coupling.md` and
`docs/plans/2026-08-23-modal-core-profile.md`.

### 18.1 — Make the modal unison crossfeed passive

- [ ] `ModalStringGroup.applyCrossfeed` still adds `sample * c * 0.08` into each
      string's first mode, with no subtraction of that string's own contribution
      — structurally the defect the DWG core had. It is **not currently
      observable**: the 0.08 factor and the modal damping keep it far under
      unity, and a 120 s pedal-held chord render reaches digital silence either
      way. It is worth doing for the same reason the DWG fix was — the bound is
      an accident of the 0.08, not a property of the term. Making it passive
      needs per-string sub-sums, which live inside three reduce variants in
      `piano/modal_kernel.go` including the SIMD one, so the change touches the
      hot path and the kernel-parity tests rather than one function.

### 18.2 — Recover sympathetic resonance level

The `noteResonator` normalisation is correct, but it removed the mechanism that
made sympathetic resonance audible, and the loss is register-dependent rather
than flat: −45.3 dB at A0, −38.0 at MIDI 36, −26.1 at C4, −14.2 at MIDI 84,
−8.3 at MIDI 96. Interleaving took back 13.3 dB (DWG) / 13.0 dB (modal) without
touching a scalar, which is what "a scalar cannot bring it back" predicted. With
the unison coupling corrected the headroom is back and measurable: the unity
crossing sits at `resonance_gain` ≈ 0.00092, so the shipped 0.00025 is **3.7x
under the ceiling**, worth about +11 dB before stability binds again.

**This item must not be closed by turning the knob up.** Three things first:

- [ ] Run a **stability margin study across registers and velocities**. The
      0.00092 cliff is one render — six notes, one velocity, coupling off — and
      the margin a shipped preset needs is a separate question from where a
      render diverges.
- [ ] Respect the standing rejection recorded in `assets/thresholds/c4.json`:
      re-voicing `resonance_gain` to buy back `spectral_rmse_db` was rejected on
      **spectral** grounds, not stability grounds, so that rejection survives the
      stability fix.
- [ ] Plan the re-fit that follows. Every shipped preset would have to be
      re-fitted afterwards — the third such debt in this area.

### 18.3 — Re-fit `assets/presets/modal-calibrated.json`

- [ ] The tool is now capable of a clean fit (`--no-resonance` landed in
      `cmd/piano-modal-fit`), but the shipped preset is still the one produced
      against diverging resonance renders, and `analysis/norms.go:55` still has
      to exclude it as a degenerate outlier. This is its own piece of work: the
      new preset changes the norm corpus, so the exclusion and the normalisation
      constants have to be re-derived together with it. **The exclusion stays
      exactly as it is until then.**

### 18.4 — `PIANO-406` — shipping profile decision

- [ ] **Re-measure "Voice cost per block and polyphony sweep"** in BENCHMARKS.md
      under the same discipline used for the partials sweep (separate
      invocations, quiet machine, load average recorded) and either correct the
      recorded table or explain the disagreement. On a quiet machine modal is
      36-39% cheaper than DWG; the recorded table, taken at load average 8-13,
      says 6-25% more expensive. Load should blur a 25% effect, not reverse its
      sign, and that is unexplained. **This is the one remaining input to the
      decision.**
- [ ] **Calibrate an acceptance threshold for `partial_level_rmse_db`, or run a
      listening test**, so the quality half of the `modal_partials` sweep can be
      closed on evidence rather than on a difference measurement with no scale
      attached. The CPU half is answered; the quality half only shows that
      lower-partial renders _differ_ from the same core at 32 partials.
- [ ] **Then adopt the shipping rule:** "low CPU" defaults to the modal core,
      "high accuracy" defaults to DWG, with DWG kept as the high-accuracy
      reference for regression checks. One thing about its shape is already
      settled by measurement, on the CPU axis alone: a "low CPU" profile must
      **not** be implemented by lowering `modal_partials` — ~55% of the cost at 8
      partials is a fixed floor, so 8→4 buys only 19-23% and 8→1 only 38-40%. If
      adopted, it is adopted as "low CPU ⇒ modal core at the default 8 partials",
      with the partial count left alone. Feeds the "primary string core for v1"
      decision at the end of this file.

**Done when:** both cores' coupling terms are passive by construction,
sympathetic resonance level is recovered with a measured stability margin rather
than a louder scalar, `modal-calibrated.json` is re-fitted with the norm corpus
re-derived alongside it, and the low-CPU/high-accuracy profile rule is adopted on
measurements that do not contradict each other.

---

## Open decisions (resolve early)

- [ ] Decide: primary string core for v1
  - [ ] DWG (matches `goal.md`)
  - [ ] Modal bank (supported by `research.md` for stability/alias control)
  - The measured inputs to this decision — CPU, retained heap and the
    `modal_partials` quality-vs-CPU curve — are collected under **Phase 18.4**,
    which has to close first. Note that the cores do not currently agree
    (distance 0.83-0.84, see 12.4), so this is a choice between two audibly
    different instruments, not between two implementations of one.
