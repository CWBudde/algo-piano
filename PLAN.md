# PLAN.md — Implementation plan (bite-sized TODOs, Go-only)

This is the actionable task list derived from `goal.md` (DWG strings + commuted convolution) and `research.md` (stability/contact + validation). It is written to be executed incrementally, with each phase producing something runnable/testable.

Important constraint:

- **All code in this repo is written in Go (Golang).** No C++/CMake-based core.

Conventions used in this plan:

- **MVP path** follows the waveguide + IR-convolution approach.
- Items marked **(optional)** are clearly skippable without blocking the main path.
- Prefer **block processing** (e.g. 64/128 frames) everywhere except where sample-accurate state is required.

---

## Phase 0 — Repo skeleton + build ("hello audio") ✓

- [x] Create basic Go project layout
  - [x] Initialize module: `go mod init github.com/cwbudde/algo-piano`
  - [x] Add `cmd/` for executables
    - [x] `cmd/piano-render` (renders a short WAV for one note)
    - [x] `cmd/piano-play` (placeholder for realtime playback)
  - [x] Add packages
    - [x] `piano/` (public engine API)
    - [x] `dsp/` (delay lines, filters, interpolators)
    - [x] `conv/` (partitioned convolution - placeholder)
    - [x] `preset/` (parameter schema + loader - placeholder)
  - [x] Add `assets/ir/`, `assets/presets/`, `examples/`
- [x] Add minimal WAV writer for `piano-render`
  - [x] Self-contained WAV writer in `dsp/wav.go` (16-bit PCM, low allocations)
- [x] Define core public API (Go types; stubs allowed initially)
  - [x] `type Piano struct` (global engine, voice allocation)
  - [x] `type Voice struct` (one note, owns 1–3 strings)
  - [x] `type StringWaveguide struct`
  - [x] `type HammerModel interface` (or concrete `Hammer` type)
  - [x] `type SoundboardConvolver struct` (initially pass-through)
  - [x] `type Params struct` (preset structs)
- [x] Add minimal DSP utilities (pure Go)
  - [x] Denormal/flush-to-zero strategy (`FlushDenormals`)
  - [x] `Biquad` filter (float32 path; no heap allocs in Process)
  - [x] `DelayLine` circular buffer
  - [x] Fractional delay interpolator (linear and cubic Lagrange)

**Done when:** `go run ./cmd/piano-render` produces a WAV with a non-zero signal. ✓

---

## Phase 1 — First audible note (1 string, no convolution) ✓

- [x] Implement `StringWaveguide` (lossless first, pure Go)
  - [x] Single delay line with feedback (simplified approach)
  - [x] Reflection coefficient (0.9999 for nearly lossless)
  - [x] Bridge pickoff output as float sample
  - [x] Parameters: `f0`, `sampleRate`, `delayLength`
- [x] Implement tuning
  - [x] Compute delay length `N = fs/f0` for complete loop
  - [x] Add fractional delay with linear interpolation for fine tuning
  - [x] Add unit test (`TestTuningAccuracy`): pitch within ±1-2 Hz tolerance
- [x] Implement a temporary excitation (no hammer yet)
  - [x] Bipolar triangular displacement for pluck/impulse excitation
  - [x] Add `NoteOn` + `NoteOff` flow in `Voice`
  - [x] Velocity scaling for excitation force

**Done when:** `piano-render --note 69` produces a stable pitched tone. ✓

---

## Phase 2 — Make the string “piano-ish” (loss + dispersion)

- [x] Add loop loss filter inside the waveguide
  - [x] Start with simple frequency-independent loss (single gain)
  - [x] Upgrade to frequency-dependent loss (1–2 biquads or a small IIR)
  - [x] Add test: energy decays monotonically for a damped configuration
- [x] Add dispersion (inharmonicity) filter
  - [x] Implement a tunable allpass cascade inside the loop
  - [x] Add parameter mapping: note -> inharmonicity/dispersion settings
  - [x] Add test: partials deviate from harmonic series in the correct direction
- [x] Add strike position (still with temporary excitation)
  - [x] Implement injection at a configurable fractional position along the string
  - [x] Add test: moving strike position changes spectral tilt (qualitative)

**Done when:** decay feels realistic and higher notes are inharmonic.

---

## Phase 3 — Hammer model (nonlinear, short contact window)

- [x] Implement `HammerModel` interface
  - [x] Inputs: hammer velocity (from MIDI velocity mapping), strike position
  - [x] Outputs: an injection signal/force for the string junction
  - [x] Make contact time-limited (stop evaluating once separation happens)
- [x] Implement power-law felt compression contact
  - [x] Model: $F = k\,\delta^p$ plus dissipative term (e.g. Hunt–Crossley style)
  - [x] Add safeguards for numerical stability (clamp, minimum dt assumptions)
  - [x] Add test: increasing velocity increases brightness (qualitative metric)
- [x] Integrate hammer into waveguide scattering at strike point
  - [x] Implement strike junction scattering (simple and stable first)
  - [x] Ensure no NaNs/inf in long renders

**Done when:** loud/soft strikes clearly change timbre and remain stable.

---

## Phase 4 — Unison strings (2–3 strings per note)

- [x] Extend `Voice` to hold 1–3 `StringWaveguide` instances
  - [x] Per-string detune in cents (small randomization or preset map)
  - [x] Per-string gain differences
- [x] Couple/mix strings
  - [x] MVP: sum bridge outputs with tiny crossfeed
  - [ ] (optional) Add weak coupling at bridge for “double decay” realism
- [x] Add tests
  - [x] Beats appear for two detuned strings (measure envelope modulation)

**Done when:** chords and sustained notes have natural beating/bloom.

---

## Phase 5 — Soundboard/body (commuted synthesis via IR convolution)

- [x] Decide IR format and shipping strategy
  - [x] Choose supported sample rates (e.g. 48k only initially)
  - [x] Choose mono/stereo IR layout under `assets/ir/`
- [x] Implement `SoundboardConvolver` (partitioned convolution)
  - [x] MVP: uniform partitioned overlap-add
  - [ ] Small early partitions for latency; larger for efficiency (later)
  - [x] Provide stereo output from one mono bridge signal
  - [x] Add reset/flush behavior for note-off and engine reset
- [x] Pick FFT/convolution backend (pure Go)
  - [x] Use `algo-fft`; let the library select the concrete FFT strategy
  - [x] Use `algo-fft` convolution implementation as the default backend
- [x] Add correctness test
  - [x] Compare partitioned convolution vs direct convolution on small signals
  - [x] Define acceptable error bound (float)
- [x] Wire it into `Piano`
  - [x] Mix all voices’ bridge outputs -> convolver -> stereo out

**Done when:** swapping IRs causes big, plausible body/room changes.

---

## Phase 6 — Pedals, dampers, and releases

- [x] Damper model
  - [x] Implement per-voice damper state
  - [x] When damper engaged: increase loop loss aggressively
  - [x] When sustain pedal down: keep strings in low-loss mode
- [x] Sustain pedal
  - [x] Add CC handling and smooth parameter transitions
- [x] (optional) Una corda / soft pedal
  - [x] Modify strike position and hammer hardness
- [x] Add tests
  - [x] Note release with pedal up decays quickly
  - [x] With sustain down, note continues ringing

**Done when:** pedal behavior matches basic piano expectations.

---

## Phase 7 — Sympathetic resonance (big realism lever)

- [x] Implement `ResonanceEngine`
  - [x] Maintain list of undamped strings (pedal down or key held)
  - [x] Inject filtered bridge/soundboard energy into undamped strings
- [x] Choose MVP injection strategy
  - [x] MVP: inject a band-limited version of the global bridge signal
  - [x] (optional) Per-note filter tuned near each string’s fundamental/partials
- [x] Add tests
  - [x] With sustain down, silent keys cause audible bloom after strikes

**Done when:** sustain pedal produces believable “wash” and bloom.

---

## Phase 8 — Presets + parameterization

- [x] Define `Params` schema
  - [x] Per-note: `f0`, dispersion/inharmonicity, loss coefficients, strike position
  - [ ] Unison: detune map, gains
  - [ ] Global: IR set, output gain, limiter (optional)
- [x] Add preset loader
  - [x] Choose JSON or YAML and implement a minimal parser strategy
  - [x] Add `assets/presets/default.*`
- [ ] Add tooling hooks (optional)
  - [ ] Offline helper to fit decay times / inharmonicity targets from recordings

**Done when:** you can tweak a preset without recompiling.

---

## Phase 8A — Reference distance harness (C4 calibration baseline) ✓

- [x] Add objective distance tooling
  - [x] Add `analysis` package with multi-metric audio distance:
    - [x] time-domain RMSE
    - [x] envelope RMSE (dB)
    - [x] log-spectral RMSE (dB)
    - [x] decay-slope mismatch (dB/s)
  - [x] Add automatic lag estimation/alignment before scoring
- [x] Add CLI tool `cmd/piano-distance`
  - [x] Compare `reference/*.wav` against rendered model output
  - [x] Support candidate render controls (`release-after`, decay threshold, min/max duration)
  - [x] Optional JSON output for machine-readable tuning loops
- [x] Establish first baseline against `reference/c4.wav`
  - [x] Baseline (2026-02-13):
    - [x] `Distance score`: `0.6147`
    - [x] `Similarity`: `8.55%`
    - [x] `Envelope RMSE`: `15.708 dB`
    - [x] `Spectral RMSE`: `23.756 dB`
    - [x] `Decay slope diff`: `7.858 dB/s`

**Done when:** we can quantify model-vs-reference mismatch with reproducible numbers. ✓

---

## Phase 8B — Distance-guided timbre matching (C4 first, then scale out)

- [x] Collect and expose first optimization surface (hammer + detuning + IR influence)
  - [x] Add preset-controlled hammer influence scales
  - [x] Add preset-controlled unison detune/crossfeed scales
  - [x] Add preset-controlled IR wet/dry/gain mix
  - [x] Document suggested Mayfly bounds and optimization order in `docs/plans/2026-02-13-phase8b-tweak-surface.md`
- [ ] Add render-control fitting loop (before touching physical params)
  - [x] Add fast inner-loop CLI: `cmd/piano-fit-fast` (time-budgeted iterative optimization with checkpointed best preset/report)
  - [x] Add runnable entrypoint: `just fit-c4-fast ...`
  - [ ] Grid/coordinate search over `velocity`, `release-after`, and output gain to reduce avoidable mismatch
  - [x] Persist best control settings with score snapshot
  - [x] Ensure fitted preset IR path serialization stays loadable from `assets/presets/` (relative IR path handling in fitter output)
  - [x] Use fitted render controls as baseline in `just distance-c4` (`velocity=118`, `release-after=3.5`)
  - [x] Current reproducible fitted checkpoint (2026-02-13):
    - [x] `Distance score`: `0.4107`
    - [x] `Similarity`: `19.35%`
    - [x] Controls from report: `velocity=118`, `release-after=3.5`
  - [ ] Promote post-checkpoint best (`score=0.4073`, `similarity=19.61%`, seen in run log at eval ~540) once persisted to preset/report
- [ ] Add physically-meaningful fitting passes for note parameters
  - [ ] Attack pass: fit hammer hardness/contact settings to reduce early-window spectral error
  - [ ] Sustain/decay pass: fit loss/damper behavior to match decay slope and envelope shape
  - [ ] Inharmonicity pass: fit dispersion/inharmonicity via partial-frequency error
- [ ] Strengthen distance metrics for piano realism
  - [ ] Add partial-ratio/tristimulus mismatch metric for harmonic balance
  - [ ] Add attack-transient metric (onset rise + first 80 ms spectral centroid trajectory)
  - [ ] Add segment-wise decay metric (early/mid/late slope instead of single global slope)
- [ ] Regression guardrails
  - [ ] Add acceptance thresholds for C4 (e.g. target score + per-metric caps)
  - [ ] CI check that rejects large regressions in distance metrics
- [ ] Add metaheuristic optimizer integration (`github.com/CWBudde/mayfly`)
  - [x] Define optimization vector and bounds (hammer, loss, dispersion, strike position, release controls)
  - [x] Wrap `analysis.Compare` as objective function (weighted multi-metric score)
  - [x] Run Mayfly on C4 first with fixed random seed + checkpointed best candidate
  - [ ] Add constrained multi-note run (e.g. C3/C4/C5) with shared + per-note parameter blocks
  - [x] Persist best-fit preset to configurable output path (default `assets/presets/fitted-c4.json`; current tracked run in `assets/presets/fitted-c4-mayfly.json`)
  - [x] Add optimizer budget controls (max evals / time limit) for reproducible tuning sessions

**Done when:** C4 distance and sub-metrics improve materially and remain stable across changes.

---

## Phase 8C — Slow loop: IR-shape optimization with `ir-synth` + Mayfly

- [x] Preparation for IR-parameter fitting tool
  - [x] Define candidate CLI tool scope and IO contract in `docs/plans/2026-02-13-phase8c-ir-fit-tool.md`
  - [x] Lock initial optimization vector to current `irsynth.Config` fields (`modes`, `brightness`, `stereo-width`, `direct`, `early`, `late`, `low-decay`, `high-decay`)
  - [x] Define checkpoint/report artifacts and resume behavior for long outer-loop runs
- [x] Add IR-synthesis objective loop (outer loop; slower than preset-only fitting)
  - [x] Implement dedicated CLI `cmd/piano-fit-ir` for outer-loop IR fitting
  - [x] Generate candidate IR per evaluation via `irsynth.GenerateStereo` (same synth core as `cmd/ir-synth`)
  - [x] Evaluate against `reference/c4.wav` by rendering with candidate IR and scoring via `analysis.Compare`
  - [x] Optimize over IR synthesis parameters:
    - [x] `modes` (e.g. `32..256`)
    - [x] `brightness` (e.g. `0.5..2.5`)
    - [x] `stereo-width` (e.g. `0.0..1.0`)
    - [x] `direct` (e.g. `0.1..1.2`)
    - [x] `early` (e.g. `0..48`)
    - [x] `late` (e.g. `0.0..0.12`)
    - [x] `low-decay` (e.g. `0.6..5.0` s)
    - [x] `high-decay` (e.g. `0.1..1.5` s)
- [x] Integrate Mayfly for this outer loop
  - [x] Objective = weighted distance score from `analysis.Compare`
  - [x] Fixed seed + checkpoint best candidate every N evals
  - [x] Use strict budget controls (`time-budget`, `max-evals`, round eval budget, population)
  - [x] Add optional joint optimization mode (`--optimize-joint`) to include selected fast-loop knobs with IR knobs
- [ ] Persist and promote winning IRs
  - [x] Save best IRs under `assets/ir/fitted/` (default output path)
  - [x] Record score + synth parameters in sidecar metadata (`.report.json`)
  - [ ] Compare top-K IRs with multi-note validation before selecting default

**Done when:** synthetic IR candidates measurably reduce spectral/envelope distance without destabilizing decay behavior.

---

## Phase 9 — Full-instrument ringing architecture (persistent strings + coupling)

This phase is split into execution subphases to make progress and ownership explicit.

### Phase 9.1 — Foundation Refactor (completed)

- [x] Split responsibilities into explicit components:
  - [x] key/control state (note on/off, pedal state)
  - [x] hammer excitation events (short nonlinear contact)
  - [x] persistent ringing state
- [x] Refactor away from classical transient voice ownership of string lifetime.
- [x] Keep `Piano.NoteOn/NoteOff/SetSustainPedal` public API unchanged.

### Phase 9.2 — Persistent String Bank Completion

- [x] Allocate full piano string set at init (1-3 strings per note), independent of active notes.
- [x] Maintain per-string damper state independent from note allocation.
- [x] Keep per-string calibration hooks (detune, loss, inharmonicity, gain, strike mapping).
- [x] Ensure no per-sample/per-block heap allocations are introduced by the bank processing path.

### Phase 9.3 — Baseline Sparse Coupling (completed MVP)

- [x] Add sparse coupling graph between strings.
- [x] Implement harmonic neighborhoods:
  - [x] unison/near-unison family
  - [x] octave-related neighbors
  - [x] fifth-related neighbors (coarse consonance proxy)
- [x] Apply coupling at bridge-side injection points with stable force limits.
- [x] Add coupling feature switch and gain controls in params/presets.

### Phase 9.4 — Physically-Informed Coupling (general-parameter model)

- [x] Add physically-informed weight model path (`coupling_mode=physical`) based on general parameters.
- [x] Define coupling coefficient for source string `i` to target string `j` using:
  - [x] overtone strength profile of source string
  - [x] frequency alignment between source/target harmonic frequencies
  - [x] approximate inter-string distance penalty
  - [x] detune penalty (larger detune => weaker coupling)
  - [x] unison-count scaling across low/mid/high note regimes (1/2/3 strings)
- [x] Build and persist an approximate string-distance map across the instrument.
- [x] Precompute sparse top-K coupling edges from the continuous model (threshold + neighbor cap).
- [x] Add normalized coupling scaling (per-source edge normalization + polyphony normalization).
- [x] Add user-facing control extent:
  - [x] `coupling_mode`: `off | static | physical`
  - [x] `coupling_amount`: scalar `0..1` blend/strength control
  - [x] advanced knobs: harmonic falloff, detune sigma, distance exponent, max neighbors
- [x] Keep hard safety clamps (`max_force`) in coupling injection path.

### Phase 9.5 — Instrument Semantics + Radiation + Web Migration

- [ ] Make sustain/damper semantics instrument-wide:
  - [ ] Sustain pedal down undamps relevant strings in the persistent bank (not just recently struck notes).
  - [ ] Note release with sustain down stops excitation only; ringing continues until damping changes.
  - [ ] Sustain pedal up reapplies damping deterministically to non-held strings.
  - [ ] If partial pedal is supported, map to physical damping coefficients (not timer-based release logic).
- [ ] Lock linear radiation path around bank output:
  - [ ] enforce `string-bank bridge mix -> body IR -> room IR`
  - [ ] keep body/room separation first-class in params/presets
  - [ ] keep legacy single-IR path as fallback only
  - [ ] complete WASM runtime IR apply (`wasmLoadIR`)
- [ ] Web/demo compatibility:
  - [ ] keep JS/WASM note + pedal API stable
  - [ ] retire sustain timer release behavior in web layer once physical pedal semantics are active
  - [ ] verify no UI/playability regressions

### Phase 9.6 — Validation, Calibration, and Performance

- [ ] Add physics-behavior tests:
  - [ ] pedal-down strike excites silent undamped related strings (octave + non-octave checks)
  - [ ] pedal-up suppresses sympathetic buildup vs pedal-down
  - [ ] hammer contact ends while ringing continues
  - [ ] `coupling_mode` transitions (`off/static/physical`) behave as expected
  - [ ] detune and distance penalties measurably reduce coupling according to model
- [ ] Add regression tests for API compatibility and long-render stability (no NaN/Inf).
- [ ] Add benchmarks:
  - [ ] idle full-string-bank cost
  - [ ] active polyphony with coupling `off/static/physical`
  - [ ] coupling graph density/top-K scaling vs CPU
- [ ] Define calibration workflow for physical coupling knobs against multi-note recordings.

**Done when:** one struck note with sustain down audibly excites non-struck related strings through the physical coupling model, coupling strength is controllable (`off` to strong) via general parameters, hammer/ringing remain decoupled, and body/room + web compatibility remain intact.

---

## Phase 10 — Web demo (WASM + AudioWorklet) ✓

- [x] Choose build approach (Go-only)
  - [x] Standard Go WASM (using syscall/js for bridge between Go and JS)
  - [x] Define a stable exported API for WASM calls (process block, note events)
- [x] Create web demo (`web/`)
  - [x] AudioWorklet processor for real-time audio
  - [x] UI: minimal keyboard (2 octaves) + sustain pedal toggle
  - [x] Computer keyboard bindings for playability
  - [x] WASM bridge with Go audio engine
- [x] Build and deployment infrastructure
  - [x] Build script (`scripts/build-wasm.sh`) for WASM compilation
  - [x] GitHub Actions workflow for automated deployment to GitHub Pages
  - [x] IR asset loading with graceful fallback

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

#### 11.1 — Fix output level calibration ✅

- [x] Investigate why candidate is 40 dB quieter than reference
  - Root cause: hardcoded `contactForce * 0.002` in `piano/control.go:117` (-54 dB attenuation)
- [x] Check signal chain gain stages: hammer exciter amplitude → string waveguide → body convolver → room convolver → output gain
- [x] Widen output_gain knob range if needed, or find the scaling bug
  - Changed force scaling `0.002 → 0.2` (+40 dB), widened output_gain range `[0.4,1.8] → [0.01,5.0]`
- [x] Verify spectral-compare shows <5 dB level gap after fix
  - Peak level gap: -41 dB → -1 dB with stage3 preset

#### 11.2 — Add hammer attack noise component

- [x] Add a short broadband noise burst at note onset in the hammer exciter
- [x] Parameters: attack_noise_level (amplitude), attack_noise_duration_ms (~1-5ms), attack_noise_color (spectral tilt)
- [x] The noise models felt-on-string impact and gives the initial "click"
- [x] Make parameters optimizable as piano-fit knobs
- [x] Verify attack window (0-20ms) spectral energy improves

#### 11.3 — Improve high-frequency sustain

- [x] Investigate frequency-dependent string loss (current loss is per-sample, uniform across harmonics)
  - DWG model had one-pole lowpass in loop but coefficient hardcoded to 0.05
  - Modal model had negligible order-dependent decay terms
- [x] Option A: Add a loss_frequency_scale parameter — higher harmonics get less additional damping
  - Added `high_freq_damping` parameter [0.0, 0.6] exposed in Params, preset JSON, and piano-fit knobs
  - DWG: controls the existing one-pole lowpass coefficient in the waveguide loop
  - Modal: scales the order and frequency dependent decay terms in modalDecay
- [x] Option B: Add a separate high-frequency loss term (2-pole lowpass on the waveguide delay line, already common in Karplus-Strong models)
  - The existing one-pole was sufficient once properly parameterized; 2-pole not needed at this stage
- [x] Verify hi-mid and high band energy in sustain/decay phases improves
  - Test confirms: tail spectral centroid drops from 1711 Hz (low damp) to 816 Hz (high damp)

#### 11.4 — Spectral distance improvements

- [ ] Consider making spectralRMSEDB use multiple time windows (STFT-based) instead of just the first 4096 samples
- [ ] Weight early/sustain/decay phases differently in the score
- [ ] Add per-band spectral distance to the analysis.Metrics for diagnostics

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

### 12.1 — Architecture + runtime switch

- [x] Define a shared `StringModel` contract for per-note ringing:
  - [x] `SetKeyDown`, `SetSustain`, excitation injection, block render, reset
  - [x] coupling injection compatibility (same external API as DWG path)
- [x] Implement engine-level core selection:
  - [x] `dwg | modal` mode in params/presets
  - [x] runtime-safe selection at init (and optional live switch if stable)
- [x] Keep existing web/API behavior unchanged regardless of selected core.

### 12.2 — Implement modal bank core

- [x] Add `StringModalBank` (per-note resonator/mode set):
  - [x] mode frequency layout (fundamental + partials with inharmonicity option)
  - [x] per-mode damping/loss mapping across keyboard
  - [x] excitation projection from hammer to mode amplitudes
  - [x] sustain/damper semantics equivalent to DWG path
- [x] Add anti-alias/stability controls for high partials near Nyquist.
- [x] Ensure no per-block heap allocations in modal render path.

### 12.3 — DWG-to-modal matching/calibration

- [x] Define DWG reference renders as modal fit targets:
  - [x] attack window
  - [x] early sustain
  - [x] decay envelope
- [x] Build a fitter to match modal parameters to a high-quality DWG preset:
  - [x] per-note or register-wise mode gain/damping fitting
  - [ ] optional shared global priors to reduce parameter count
- [x] Add an export flow:
  - [x] generate modal parameter tables from DWG reference preset
  - [x] version and store fitted modal profiles in assets/presets

Tooling:

- [x] Added `cmd/piano-modal-fit` to calibrate modal knobs against DWG renders and export a calibrated modal preset + report.
- [x] Switched `cmd/piano-modal-fit` objective search from ad-hoc random mutations to Mayfly (`--mayfly-variant`, `--mayfly-pop`) with local post-refinement.

### 12.4 — Validation + performance acceptance

- [ ] Add A/B tests and metrics:
  - [ ] DWG vs modal distance on selected notes/chords
  - [ ] sustain pedal and coupling behavior parity checks
  - [ ] long-render stability (NaN/Inf free)
- [ ] Add benchmarks:
  - [ ] DWG vs modal CPU at fixed block size/sample rate
  - [ ] polyphony scaling comparison
  - [ ] memory footprint comparison
- [ ] Define shipping rule:
  - [ ] “low CPU” profile defaults to modal core
  - [ ] “high accuracy” profile defaults to DWG core

**Done when:** modal core is selectable, calibrated against DWG via an automated matching step, and documented profiles exist for low-CPU vs high-accuracy operation.

---

## Phase 13 — Tests + benchmarks (keep realtime honest)

- [ ] Unit tests
  - [ ] Tuning accuracy across a range of notes
  - [ ] Convolver correctness bound
  - [ ] Stability tests: long render without NaNs/denorm storms
- [ ] Benchmarks
  - [ ] Use `go test -bench=.` benchmarks
  - [ ] Voice cost per block at 48k/128 frames
  - [ ] Convolution cost by IR length/partition size
  - [ ] Polyphony sweep (e.g. 16/32/64/128 voices)

**Done when:** you have a baseline performance budget and regression alarms.

---

## Phase 14 — Polish (only after the core is solid)

- [ ] Add key-off / pedal noise (small synthesized bursts or tiny samples)
- [ ] Add output limiter/safety clipper
- [ ] Improve dispersion/loss mapping across the keyboard

---

## Open decisions (resolve early)

- [ ] Decide: primary string core for v1
  - [ ] DWG (matches `goal.md`)
  - [ ] Modal bank (supported by `research.md` for stability/alias control)
