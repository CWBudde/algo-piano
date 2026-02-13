This is a comprehensive implementation plan for this repo, that aims for **ultra-realistic** sound while still being **real-time capable**, using **shortcuts where they buy a lot of speed per perceptual cost**.

The plan is based on well-established real-time piano physical-modeling ideas:

- **Digital waveguide strings** with **loss + dispersion** filters (very efficient, high realism when tuned). ([DAFx][1])
- **Commuted synthesis**: push the expensive, linear soundboard/body part into an **impulse response (IR)** and use fast convolution. ([Auditory][2])
- Reference / validation models exist (finite differences / full physical) but you don’t want those at full resolution in real-time. ([ResearchGate][3])

---

## 1) High-level target architecture

### Core signal path (per note / per string group)

1. **Hammer–string interaction** (nonlinear, short duration, must feel right)
2. **String vibration model** (waveguide loop with dispersion + loss)
3. **Bridge output** (force/velocity proxy)
4. **Soundboard / body radiation** (fast LTI stage)
   - implemented as **partitioned convolution** with measured or designed IR(s) (commuted)

5. **Sympathetic resonance & coupling** (selected shortcuts)
6. **Pedals + dampers**
7. **Stereo radiation** (two IRs, or mid/side IR set, or simple mic model)

This matches the efficiency trick of commuted piano synthesis: the (big) resonator becomes cheap while preserving realism. ([Auditory][2])

---

## 2) Algorithms to implement (math/pseudocode level)

### 2.1 String model: bidirectional digital waveguide loop

Represent transverse wave as right/left traveling components:

- Delay-line lengths approximate half-wavelength propagation:
  - (N \approx \frac{f_s}{2 f_0}) samples (plus fractional delay for tuning)

- Two delay lines (or one circular buffer with two taps), reflection at ends:
  - Termination filters implement impedance, losses, bridge coupling.

**Core loop per sample (conceptual):**

```
yR = delayR.read(frac)
yL = delayL.read(frac)

# Reflection / termination filters at both ends:
r_bridge = Fb(yR)          # bridge-end reflection (includes coupling extraction)
r_nut    = Fn(yL)          # nut-end reflection

# Dispersion + loss in-loop:
wR = D( L(yL_reflected) )
wL = D( L(yR_reflected) )

delayR.write(wR)
delayL.write(wL)

bridge_out = pickoff(yR, yL, maybe force proxy)
```

**Dispersion modeling**: stiff piano strings are inharmonic; in waveguides this is commonly modeled with an **allpass cascade** inside the loop. Rauhala & Välimäki give efficient tunable designs specifically for waveguide piano. ([DAFx][1])

**Loss modeling**: frequency-dependent damping via a small IIR / FIR inside the loop (often 1–3 biquads).

**Tuning**: fractional delay (Lagrange / Thiran) for fine tuning and smooth pitch bend (if needed for effects).

---

### 2.2 Hammer model: nonlinear felt compression force

A common physically-motivated choice is a power-law contact (felt compression):
[
F_h = k , \delta^p + r , \delta^p \dot{\delta}
]
where (\delta = (x*h - u(x_s,t))*+) is compression at strike point, (p\approx 2\ldots 4) (felt nonlinearity), plus damping/hysteresis.

In waveguide form, you typically compute interaction at a **scattering junction** at the strike position.

**Practical real-time shortcut (often used in commuted models):**

- run the nonlinear hammer interaction only during the short contact window
- optionally approximate it as a _linear, commutable filter_ for extra speed (Van Duyne & Smith explored linearizing the hammer for commuted synthesis). ([Auditory][4])

---

### 2.3 Multi-string unison + detuning

Most notes have 1–3 strings. Model each string as its own waveguide with:

- slight detune (cents)
- slightly different inharmonicity (B)
- slightly different damping

Couple them weakly (optional):

- minimal shortcut: **mix their bridge outputs** with tiny crossfeed
- more physical: small coupling filter between string states at bridge

---

### 2.4 Soundboard/body: commuted synthesis via IR convolution

Instead of simulating a 2D soundboard PDE in real time, use measured / designed impulse responses:

[
y(t) = (h * b)(t)
]
where (b(t)) is bridge output proxy (force/velocity) and (h(t)) is the soundboard+body+air IR.

This is the key “realism per FLOP” win: IRs can be long and detailed yet cheap using **partitioned FFT convolution**. ([Auditory][2])

**Implementation**: uniform or non-uniform partitioned convolution:

- Small early partitions for low latency
- Larger partitions for efficiency

Your **`algo-fft`** should be a natural home for the FFT backend here.

---

### 2.5 Pedals and damping

- Damper pedal: switches from **strong damping** to **weak damping** on all undamped strings.
- Soft pedal (una corda): changes strike position + hammer hardness parameters.
- Sostenuto: selective sustain (more involved; can be simplified).

In waveguide terms: damping is just moving the loop filter coefficients.

---

### 2.6 Sympathetic resonance (big realism lever)

When sustain pedal is down, undamped strings ring from soundboard energy.

**Fast, perceptually good shortcut**:

- Drive each undamped string model with a filtered version of the soundboard/bridge signal (or with band-limited energy in its vicinity)
- Or inject energy at bridge end (common)

This yields “bloom” and realism without full coupling matrices.

---

## 3) How `algo-dsp`, `algo-fft`, `algo-pde` fit in

Because I couldn’t fetch the contents of your GitHub repos from the web tool in this environment (GitHub returned fetch errors), I’ll describe integration in a way that should align with typical “algo-\*” library splits, while keeping coupling clean.

### `algo-dsp` (real-time primitives)

Use it for:

- Delay lines + fractional delay interpolators
- Biquads / small IIRs (loop filters, EQ, hammer linearization filters)
- Envelope/ADSR utilities (key noise, release noise)
- Denormals handling, SIMD helpers (if present)
- Audio block processing helpers

### `algo-fft` (partitioned convolution)

Use it for:

- FFT blocks, overlap-add/overlap-save
- Partitioned convolution engine
- Optional: STFT utilities for analysis/tuning tools

### `algo-pde` (offline reference + model generation tools)

Use it for:

- Offline **FDTD stiff string** reference to validate waveguide dispersion/loss tuning (Chaigne/Askenfelt-style finite difference is a known reference path). ([ResearchGate][3])
- Optional: generate calibration targets (decay times per partial, inharmonicity curves, etc.)
- Potential future: soundboard modal extraction / fitting (but not required for v1)

---

## 4) Proposed repo: `algo-piano` structure

### 4.1 Top-level layout

```
algo-piano/
  CMakeLists.txt
  README.md
  LICENSE
  /include/algo_piano/
    piano.hpp
    voice.hpp
    string_waveguide.hpp
    hammer.hpp
    soundboard_convolver.hpp
    params.hpp
    tuning.hpp
    resonance.hpp
  /src/
    piano.cpp
    voice.cpp
    string_waveguide.cpp
    hammer.cpp
    soundboard_convolver.cpp
    tuning.cpp
    resonance.cpp
  /assets/
    ir/                 # soundboard/body IRs (stereo, multiple sets)
    presets/            # piano models (YAML/JSON)
  /tests/
    test_string_tuning.cpp
    test_dispersion_fit.cpp
    test_convolver_bitexact.cpp
  /bench/
    bench_voice.cpp
    bench_convolution.cpp
  /demo-web/
    package.json
    vite.config.*
    src/
      main.ts
      audio-worklet.ts
      ui.ts
    public/
```

### 4.2 Core classes (API sketch)

- `PianoModel`
  - owns global parameters, IR set, note->string mapping

- `Voice`
  - one active note (handles 1–3 strings, hammer event, damper state)

- `StringWaveguide`
  - delay lines + loop filters + dispersion + pickup/bridge output

- `HammerModel`
  - nonlinear force law; produces injection signal at strike junction

- `SoundboardConvolver`
  - partitioned FFT convolver; stereo output

- `ResonanceEngine`
  - sympathetic resonance injection manager

---

## 5) Real-time constraints and the “good shortcuts”

### Keep (high perceptual payoff)

- Waveguide strings with tuned dispersion + loss ([DAFx][1])
- Multiple strings + slight detune
- Long stereo IR convolution for body (commuted) ([Auditory][2])
- Sympathetic resonance (even simplified)

### Shortcut aggressively (low payoff per CPU)

- Full 2D soundboard PDE in real time (replace by IR)
- Fully physical bridge impedance matrix
- Exact nonlinear hammer scattering beyond contact window
- Full coupling among all strings (use simplified injection)

---

## 6) Parameterization plan (how you’ll make it “sound like a piano”)

### 6.1 Per-note parameter set

For each MIDI note:

- (f_0) (tuning)
- inharmonicity (B) or equivalent dispersion filter params ([DAFx][5])
- loop loss filter coefficients (target decay T60 vs frequency)
- strike position (fraction of string length)
- 1–3 strings: detune map, relative gains

### 6.2 IR sets

Ship multiple IR sets in `/assets/ir/`:

- “close mic”, “player position”, “roomy”
- For realism: include pedal-down IR vs pedal-up IR is _overkill_; better keep pedal in string damping and keep IR fixed.

---

## 7) Web demo (WASM) plan

### Audio architecture (low latency)

- Use **WebAudio AudioWorklet** for real-time callback audio.
- Compile `algo-piano` core to WASM.
- JS thread handles UI and MIDI events; AudioWorklet calls into WASM for blocks.

**Block processing**

- process e.g. 128 frames per callback
- keep convolver partition size aligned with block size for efficiency/latency tradeoff

**MIDI**

- WebMIDI if available; fallback to on-screen keyboard

---

## 8) Milestones (recommended implementation order)

### Milestone A — “First audible note” (1 string, no convolution)

1. Implement `StringWaveguide` (lossless first)
2. Add fractional delay tuning
3. Implement minimal hammer excitation (simple force pulse) to verify pitch/decay

### Milestone B — “Piano-ish string”

4. Add loop loss filter (target decay)
5. Add dispersion allpass cascade (tunable) ([DAFx][1])
6. Validate against offline reference from `algo-pde` stiff-string FDTD (optional but valuable) ([ResearchGate][3])

### Milestone C — “Body + realism jump”

7. Implement `SoundboardConvolver` using `algo-fft` (partitioned OLA)
8. Add stereo IR sets and routing

### Milestone D — “Instrument behavior”

9. Add 2–3 unison strings per note + detune
10. Dampers + sustain pedal behavior
11. Sympathetic resonance engine

### Milestone E — “Web demo”

12. WASM build (CMake + Emscripten, or your preferred toolchain)
13. AudioWorklet integration + UI + WebMIDI

### Milestone F — “Polish”

14. Key-off noise, pedal noise (small samples or synthesized bursts)
15. Preset system + calibration tools
16. Benchmarks + profiling + SIMD optimizations if needed

---

## 9) Testing & validation strategy

- **Tuning tests**: assert frequency within tolerance over range of notes
- **Dispersion tests**: compare partial frequencies to target inharmonicity curve
- **Decay tests**: energy envelope matches intended T60 vs band
- **Convolver tests**: bit-exact or error-bounded against direct convolution for small signals
- **Performance budget**: benchmark voices at target polyphony (e.g., 32–128 voices)

---

[1]: https://www.dafx.de/paper-archive/details/n4Qv1ScD_VbmqShsq0ZVQQ?utm_source=chatgpt.com "DAFx Paper Archive - Dispersion Modeling in Waveguide Piano Synthesis Using Tunable Allpass Filters"
[2]: https://www.auditory.org/asamtgs/asa95wsh/5aMU/5aMU4.html?utm_source=chatgpt.com "5aMU4 An efficient time-domain model for the piano using commuted"
[3]: https://www.researchgate.net/publication/243514591_Numerical_simulations_of_piano_strings_I_A_physical_model_for_a_struck_string_using_finite_difference_methods?utm_source=chatgpt.com "(PDF) Numerical simulations of piano strings. I. A physical model for a struck string using finite difference methods"
[4]: https://www.auditory.org/asamtgs/asa95wsh/5aMU/5aMU5.html?utm_source=chatgpt.com "5aMU5 A linear filter approximation to the hammer/string interaction for"
[5]: https://www.dafx.de/paper-archive/2006/papers/p_071.pdf?utm_source=chatgpt.com "Proc. of the 9th Int. Conference on Digital Audio Effects (DAFx-06), Montreal, Canada, September 18-20, 2006"
