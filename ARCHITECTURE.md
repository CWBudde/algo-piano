# Architecture

This document describes the current `algo-piano` architecture as implemented in this repository, including both string-core modes:

- `dwg` (digital waveguide)
- `modal` (modal resonator bank)

## 1. System Overview

`algo-piano` is a physically informed piano synthesizer with a hybrid architecture:

1. Event/control layer (note on/off, pedal state, model selection)
2. Excitation layer (nonlinear hammer model + optional attack noise)
3. String-bank layer (`dwg` or `modal`, selectable at runtime), with the
   sympathetic resonance loop closed **inside** its per-sample render loop
4. Linear body/room rendering layer (partitioned convolution IRs)
5. Stereo output mix layer

Core render path per audio block:

```text
MIDI/UI Events
  -> HammerExciter + RingingState(StringBank[dwg|modal])
       per sample: hammer force -> optional ResonanceEngine injection of the
       PREVIOUS sample's bridge mix into undamped notes -> advance strings
  -> mono bridge/string mix
  -> BodyConvolver (mono->mono)
  -> Room/SoundboardConvolver (mono->stereo)
  -> output mix/gain
```

## 2. Main Runtime Components

### 2.1 `piano.Piano` (top-level engine)

Main struct owns:

- `keyStateTracker` (key-down + last velocity per MIDI note)
- `HammerExciter`
- `RingingState` (contains `StringBank`)
- `BodyConvolver` (mono IR)
- `SoundboardConvolver` (stereo IR)
- optional `ResonanceEngine`
- pedal states (`sustainAmount` in `[0,1]`, `softPedal`)

Important behavior:

- `maxPolyphony` in `NewPiano` is currently retained for API compatibility but ignored internally.
- `SetStringModel("dwg"|"modal")` rebuilds key/runtime state and preserves:
  - held keys
  - last velocities
  - sustain/soft pedal state
- Model switch does **not** preserve existing string internal energy; it reinitializes the ringing engine.

Sustain pedal semantics:

- `SetSustainPedalAmount(a)` takes a continuous pedal depth in `[0,1]` and is the
  primitive; `SetSustainPedal(down)` is the `1.0` / `0.0` case of it.
- The depth is applied instrument-wide to **every** group in the persistent
  bank, including notes that have never been struck, so sympathetic resonance
  and coupling see the same damper state as the struck notes.
- Per group, damper contact is `0` while the key is held and `1 - amount`
  otherwise. There is no timer anywhere in the release path: `NoteOff` only
  clears the key, and the string keeps ringing exactly as long as its current
  damping allows.
- A group counts as undamped (and is therefore a valid resonance/coupling
  target) whenever the key is down or the pedal depth is greater than zero, so a
  half-pedal still rings sympathetically.

### 2.2 `RingingState` and `StringBank`

`RingingState` is a thin wrapper around `StringBank`.

`StringBank` contains one persistent group per MIDI note in the configured inclusive range (`min_note..max_note`, default `21..108`) and can host:

- `RingingStringGroup` for `dwg`
- `ModalStringGroup` for `modal`

Each group implements a shared interface with:

- key/sustain damping control (continuous damper amount, not a boolean)
- hammer force injection
- coupling force injection
- per-sample processing
- block-end activity gating

Active-note optimization:

- Bank tracks `activeNotes` and only processes active groups.
- Inactive notes stay allocated but are skipped.

## 3. String-Core Modes

## 3.1 DWG Mode (`string_model = "dwg"`)

Implementation type: `StringWaveguide`.

Per string state:

- fractional delay line (pitch via `delayLength = fs/f0`)
- loop reflection gain (`baseReflection`)
- damper reflection override (`damperReflection`), blended in by `damperAmount`
- one-pole loop lowpass (`lowpassCoeff`, `loopState`)
- simple dispersion allpass chain (`dispersionCoeff`, 2-stage state)

Per-sample update:

1. Read delayed sample with linear interpolation.
2. Apply dispersion allpass stages.
3. Apply loop-loss one-pole lowpass.
4. Multiply by reflection gain (damper-aware).
5. Write back to delay line.
6. Output delayed sample.

Per-note behavior:

- Notes use unison allocation by register:
  - `< 40`: 1 string
  - `40..69`: 2 strings
  - `>= 70`: 3 strings
- Detune/gain defaults are applied per unison string.
- Each note group can apply per-note overrides (`loss`, `inharmonicity`, `strike_position`).
- The strings of a group are coupled to each other through the bridge once per sample. The
  force on string `i` is `unison_crossfeed * g_i * (mix - y_i)`: proportional to the
  **difference** between the common bridge motion and that string's own contribution to it,
  and written via `StringWaveguide.InjectForceNext`, into the slot the interpolating taps
  read on the next `Process` call. Both details are load-bearing. The subtraction makes the
  term dissipative — the energy it adds per sample is `c*(mix^2 - sum(g_i*y_i^2)) <= 0` by
  Jensen's inequality — and that argument is instantaneous, so it survives at the partials
  only if the force acts immediately: delayed by a fraction `p` of a round trip it lags
  `2*pi*n*p` at partial `n` and turns anti-damping once that passes a half cycle. A strike
  position cannot express "next sample" — `injectionOffset` maps `[0,1]` affinely onto the
  round trip, so even its smallest input is ~1% of it (1 sample at MIDI 60, 17 at MIDI 21),
  which is why this has its own entry point. `NewStringBank` clamps the strength to
  `maxUnisonCrossfeed`. This is what produces unison beating and the two-stage decay.

## 3.2 Modal Mode (`string_model = "modal"`)

Implementation type: `ModalStringGroup`.

Each unison string contains multiple damped modes (`modalMode`), each with:

- oscillator rotation terms (`cosW`, `sinW`)
- gain by order (`1 / order^ModalGainExponent`)
- undamped and damped decay coefficients
- complex state (`re`, `im`)

Per-sample update for each mode:

```text
nx = decay * (re*cosW - im*sinW)
ny = decay * (re*sinW + im*cosW)
re, im = nx, ny
sample += nx * modeGain
```

Mode generation:

- Partial frequency from base `f0`, order, and inharmonicity.
- Truncated at Nyquist safety margin (`< 0.95 * Nyquist`).
- Fallback mode is created if no partial survives.

Key knobs:

- `modal_partials`
- `modal_gain_exponent`
- `modal_excitation`
- `modal_undamped_loss`
- `modal_damped_loss`

Unison coupling:

- The strings of a modal group are coupled the same way as in the waveguide core, with the
  force on string `si` proportional to `unison_crossfeed * 0.08 * g_i * (sample -
stringOut[si])` — the difference between the bridge mix and that string's own contribution
  to it. It is written into the first mode of each string only, which the very next
  rotate-decay step reads, so the modal core needs no equivalent of
  `StringWaveguide.InjectForceNext`.
- The subtraction is what makes the sign correct: a string louder than the bridge is pushed
  back, never further. The Jensen argument the waveguide core can make does not transfer,
  because the correction lands on mode 0 instead of being distributed over the string's
  modes, so the modal term is fenced by measurement (`modal_unison_coupling_test.go`) rather
  than by construction. Before 2026-08-23 the force was `sample * c * 0.08` added into every
  string with no subtraction, which added 9–14% of a note's energy at the default crossfeed
  and diverged at `maxUnisonCrossfeed`.

Damper semantics:

- Key up + no sustain -> use damped decay
- Key down or sustain fully down -> use undamped decay
- Partial pedal -> per-mode lerp between the damped and undamped decay tables
  (the `0` and `1` cases stay exact table copies, so full-pedal output is
  bit-identical to the boolean behavior)

## 4. Shared Subsystems (Both Modes)

### 4.1 Hammer / excitation

`HammerExciter` manages short-lived note strike events:

- nonlinear felt hammer contact (`Hammer.Step`)
- per-note strike position
- soft pedal influence:
  - strike position offset
  - reduced hammer hardness
- optional attack-noise burst:
  - short decaying noise injection
  - optional spectral color tilt

Injected force enters the currently active string model through common injection methods.

### 4.2 Coupling (inter-note energy transfer)

`StringBank` supports sparse coupling modes:

- `off`
- `static`: fixed octave/fifth edges
- `physical`: graph from partial alignment + detune penalty + keyboard distance penalty

Coupling is applied blockwise:

- source drive computed from block-level note output stats
- edges inject bounded force into target notes
- scaled by active polyphony to avoid overload

### 4.3 Sympathetic resonance

`ResonanceEngine` processes the bridge mono signal, one sample at a time:

- DC removal + lowpass band-limiting
- optional per-note resonance filter before injection
- scaled injection into undamped note targets

Targets are the currently selected string groups (DWG or modal), so resonance works with both modes.

**The loop is closed inside `StringBank`, not around it.** `Piano` owns the
engine and attaches it to the ringing state (`attachResonance`, re-run after
every rebuild, including `SetStringModel`); `StringBank.processWithBridge` then
calls `injectSample` once per sample from within its render loop, driven by the
previous sample's own bank output. The loop delay is therefore one SAMPLE.

Until 2026-08-22 it was closed once per block in `Piano.Process`: a finished
block was handed to `InjectFromBridge`, which deposited every sample of it into
string state that never advanced in between. That summed the block coherently —
for a mode at angular frequency `w` the string received `|sum x[i]|`, the block's
DC content, instead of `|sum x[i]*exp(-jwi)|`, the drive at `w` — which is a
128-tap boxcar at block rate. `InjectFromBridge` has been **removed** along with
the `RingingState.ResonanceTargets` and `RingingState.NotifyResonanceInjected`
forwarders that were the only way to reach it: closing the loop from outside the
bank was the defect itself, so there is deliberately no second injection path
left. Probes that need to drive the bank with a known signal pass a `drive`
slice to `processWithBridge`.

One thing deliberately stayed at block rate: `syncResonatingNotes` enrolls
newly energized groups once per block, because `activeNotes` and the modal arena
layout are fixed for a block's duration. That costs one block per group at first
energization and nothing thereafter.

### 4.4 Body and room convolution

Two-stage linear rendering:

1. `BodyConvolver`: mono input -> mono output
2. `SoundboardConvolver` (room stage): mono input -> stereo output

Both are partitioned overlap-add convolution using `algo-dsp`.

IR loading behavior:

- Body IR can load from `BodyIRWavPath`
- Room IR uses `RoomIRWavPath`, fallback to legacy `IRWavPath`
- WAV IRs are resampled to runtime sample rate if needed

Offline structural body modelling is separated from realtime convolution.
The released `github.com/cwbudde/algo-pde/cmd/plate-modes@v0.3.0` command solves
an orthotropic ribbed plate once and exports a strict
`body-modal-transfer-v1` JSON transfer from distributed bridge force to
area-averaged normal velocity. The producer is pinned by `just
generate-body-transfer` through the repository's `algo-pde v0.3.0` module
requirement, which also supplies the boundary-condition integration tests.
`irsynth.GenerateModalBody` converts that cached transfer into a mono IR with
explicit gain and loss scaling; it adds no random phase, random modal amplitude,
or implicit normalization. `piano-fit
--body-transfer` loads the artifact once before optimization and varies only
the cheap IR-rendering controls.

### 4.5 Final output mix

Final stereo sample uses:

- body-dry contribution
- room-wet contribution
- per-stage gains
- final output gain

Legacy single-IR fields are mapped for backward compatibility when dual-IR paths are not set. The
mapping is resolved once — at `NewPiano` and again in `SetIRMix` (`resolveRadiationMix`) — so the
per-block render path only ever reads dual-IR semantics and the legacy fields cannot override
`BodyIRGain` at render time.

The stage order `string-bank bridge mix -> body IR -> room IR` is serial and fenced by
`piano/radiation_test.go`, which drives pure-delay IRs through `Piano.Process` and asserts that the
room contribution lands at the sum of both delays.

Offline tools (`piano-render`, `piano-fit`, `piano-distance`) fall back to the shipped IR via
`piano.ApplyDefaultRoomIR`, which installs it as the _room_ stage. The legacy `IRWavPath` is honoured
only when a caller or preset sets it explicitly.

## 5. Runtime Mode Selection (`dwg` vs `modal`)

Mode can be chosen by:

1. Preset field: `"string_model": "dwg" | "modal"`
2. Runtime API: `Piano.SetStringModel(...)`
3. Web UI selector (`DWG` / `Modal`) -> WASM `wasmSetStringModel`

Defaults:

- Engine default (`NewDefaultParams`) is `dwg`.
- Web demo initializes with `dwg`, but sets tuned modal knob defaults so switching to `modal` is immediately usable.

## 6. Configuration Surface

Preset loader (`preset/json.go`) validates and applies:

- global gains/mix
- IR paths
- hammer scales
- string model and modal knobs
- coupling mode and parameters
- per-note overrides:
  - `f0`
  - `inharmonicity`
  - `loss`
  - `strike_position`

Invalid `string_model` values are rejected (`must be one of dwg|modal`).

## 7. WebAssembly + Web Frontend Architecture

`cmd/piano-wasm` exports JS-callable functions:

- init and render: `wasmInit`, `wasmProcessBlock`
- note and pedal control: `wasmNoteOn`, `wasmKeyDown`, `wasmNoteOff`, `wasmSetSustain`, `wasmSetSustainAmount`
- model/coupling control: `wasmSetStringModel`, `wasmSetCouplingMode`
- output control: `wasmSetMasterGain`, `wasmSetLimiterEnabled`, `wasmSetReverbEnabled`, `wasmSetReverbAmount`

Frontend (`web/main.js`) responsibilities:

- load WASM and initialize engine with browser sample rate
- UI control wiring (keyboard, mouse, pedals, mode selectors)
- audio callback rendering in chunks (128-frame synth chunks)
- runtime calls for coupling/mode and post-output effect changes

The optional post-output chain uses `algo-dsp`: independent left/right
Freeverb-style room processors feed the master gain and independent left/right
peak limiters. Reverb and limiting default to bypass, preserving the piano's
existing output until the user enables them.

Runtime IR loading:

- `wasmLoadIR(kind, buffer)` decodes a WAV from the supplied `ArrayBuffer`/`Uint8Array` and applies it
  to the body (`"body"`) or room (`"room"`) convolver, resampling to the AudioContext rate. The legacy
  single-argument form `wasmLoadIR(buffer)` still targets the room slot. Returns `true` on success.
- `wasmSetIRMix(bodyDry, bodyGain, roomWet, roomGain)` sets the dual-IR mix at runtime; the room IR is
  inaudible without it because `RoomWetMix` defaults to `0`.
- The web demo loads `assets/ir/default_96k.wav` into the room slot and falls back to no convolution
  when the fetch fails.

## 8. Offline Tooling Around the Core Architecture

Key commands:

- `cmd/piano-render`: offline note rendering
- `cmd/piano-distance`: objective reference/candidate comparison (`analysis.Compare`)
- `cmd/piano-modal-fit`: calibrates modal knobs against DWG reference renders and writes modal preset/report (`--no-resonance` silences sympathetic resonance for the calibration renders only; the written preset keeps the input preset's `resonance_enabled`)
- `cmd/piano-fit`: broader optimization workflow
- `cmd/ir-synth`: synthetic IR generation (body/room style IR assets)

This supports a practical workflow:

1. Build a high-quality DWG reference preset.
2. Fit/calibrate modal parameters to match DWG behavior for low-CPU profile.
3. Compare with objective metrics.

## 9. Design Intent

The architecture keeps excitation, resonance, coupling, and linear body/room rendering shared between both cores, while isolating the string-core choice (`dwg` vs `modal`) behind a common group interface. This enables:

- runtime model switching
- shared control surface/preset format
- consistent pedal and resonance behavior across modes
- a DWG-quality reference path and a modal low-CPU path in the same engine
