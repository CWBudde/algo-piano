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
  - [x] Initialize module: `go mod init github.com/MeKo-Christian/algo-piano`
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

## Phase 9 — Tests + benchmarks (keep realtime honest)

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

## Phase 11 — Polish (only after the core is solid)

- [ ] Add key-off / pedal noise (small synthesized bursts or tiny samples)
- [ ] Add output limiter/safety clipper
- [ ] Improve dispersion/loss mapping across the keyboard
- [ ] (optional) Alternative string core: modal resonator bank (from research.md)
  - [ ] Implement `StringModalBank` behind the same `StringModel` interface
  - [ ] Compare CPU and realism vs DWG approach

---

## Open decisions (resolve early)

- [ ] Decide: primary string core for v1
  - [ ] DWG (matches `goal.md`)
  - [ ] Modal bank (supported by `research.md` for stability/alias control)
- [ ] Decide: sample rate strategy
  - [x] Variable runtime sample-rates with IR resampling from high-res source WAV
  - [ ] (optional) multi-rate for high notes
- [ ] Decide: IR licensing + source
  - [x] Store IR assets as high-resolution WAV (96 kHz preferred), resample at load time
  - [ ] Use your own measured IRs
  - [ ] Use a permissive IR set (verify license)
- [ ] Decide: realtime audio I/O for native builds
  - [ ] Start with offline WAV render only (`cmd/piano-render`)
  - [ ] (optional) Add realtime playback via a Go audio library (prefer pure Go)
