# Phase 8B — Distance metrics, weighting profiles and calibration status

**Date:** 2026-08-21
**Scope:** `analysis/` (metrics, weights, attack, features), consumed by
`cmd/piano-distance` and `cmd/piano-fit`.

This document records what the distance metrics now measure, which weighting
profile to reach for, and — the part that matters most in practice — how well
calibrated each normalisation constant actually is.

---

## 1. The scoring model

`analysis.Compare` reduces a reference/candidate pair to a single `Score` in
`[0,1]` (0 best). The score is a weighted sum of **components**. Each component
takes one raw metric in its natural unit, divides it by a `Norm*` constant, and
clamps the result to `[0,1]`:

```
norm_i  = clamp01(raw_i / Norm_i)
Score   = Σ weight_i · norm_i        (over available components)
```

Two consequences follow directly from that formula and drive everything below:

- **`Norm_i` sets the resolution of the component.** Too large and the component
  barely moves; too small and it saturates.
- **A saturated component is an invisible component.** Once `raw_i >= Norm_i`,
  `norm_i` is pinned at 1.0. It still contributes its full weight to the score,
  but it no longer varies with the signal, so it hands the optimizer _no
  gradient at all_. `Metrics` carries a `*Saturated` flag per component, and
  `piano-distance` prints a warning, precisely so this stays visible.

Unavailable components (currently only `attack`, when the compared window holds
no rising onset) are dropped from the score and the remaining weights are
renormalised.

### Entry points

| Function                                  | Use                                                                                                                                                                                                                                    |
| ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Compare(ref, cand, sr)`                  | Legacy four-component score. **Bit-identical** to the pre-2026-08 implementation.                                                                                                                                                      |
| `CompareWithWeights(ref, cand, sr, w)`    | Same, with an explicit `Weights` profile.                                                                                                                                                                                              |
| `CompareWithOptions(ref, cand, sr, opts)` | Full control: `Weights`, `F0Hz`, `MIDINote`, `SkipPartials`, `SkipAttack`.                                                                                                                                                             |
| `Components(m, w)`                        | Expands `Metrics` into the individual weighted terms, with `Raw`/`Norm`/`Weight`/`Contribution`/`Saturated`/`Available`.                                                                                                               |
| `Metrics.Sanitized()`                     | Replaces every non-finite float with a finite worst-case fallback and clamps `Score`/`Similarity`. **Required before JSON encoding** — `encoding/json` refuses NaN and ±Inf, and an un-fittable decay slope legitimately produces NaN. |

Passing `MIDINote` is strongly preferred over letting `f0` be estimated from the
spectrum: a mis-estimated fundamental biases every partial ratio derived from it.

---

## 2. Metric definitions

### Legacy four (unchanged)

| Metric            | JSON tag              | Unit                 | What it measures                                                                                                                                                  |
| ----------------- | --------------------- | -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TimeRMSE`        | `time_rmse`           | normalised amplitude | Sample-wise RMS error after silence trim, RMS normalisation and lag alignment.                                                                                    |
| `EnvelopeRMSEDB`  | `envelope_rmse_db`    | dB                   | RMS error between the two amplitude envelopes.                                                                                                                    |
| `SpectralRMSEDB`  | `spectral_rmse_db`    | dB                   | RMS error between magnitude spectra, phase-weighted across attack/sustain/decay. Also broken out per band (`spectral_low/mid/high_rmse_db`) for diagnostics only. |
| `DecayDiffDBPerS` | `decay_diff_db_per_s` | dB/s                 | Difference between the two globally fitted decay slopes.                                                                                                          |

### Partial-domain metrics (new)

Computed from matched partials around the fundamental.

- **`partial_level_rmse_db`** — dB RMSE of matched partial _levels_. Captures
  harmonic balance: a candidate that is right in overall spectrum but wrong in
  how energy is split between partials shows up here.
- **`partial_freq_rmse_cents`** — cents RMSE of matched partial _frequencies_.
  This is the inharmonicity signal: a stiff-string dispersion that is too weak
  or too strong stretches the partial series and lands here directly.
- **`tristimulus_distance`** — Euclidean distance between the two tristimulus
  triples (fundamental / low-mid partials / high partials as fractions of total
  partial energy). A scale-free, three-number summary of timbral balance.

All three are undefined (NaN) when the aligned window is too short, or when
`Options.SkipPartials` is set.

### Attack metrics (new)

Both are measured over `attackWindowSec = 80 ms` from the onset.

- **`attack_rise_diff_ms`** — difference between reference and candidate onset
  _rise times_ (`ref_rise_time_ms`, `cand_rise_time_ms` are reported alongside).
- **`attack_centroid_rmse_oct`** — RMSE of the _spectral centroid trajectory_
  over the attack window, expressed in **octaves**. Octaves rather than Hz
  deliberately: a fixed Hz error is perceptually huge at 200 Hz and negligible
  at 5 kHz, and the metric must behave the same across the keyboard.
  `ref_attack_centroid_hz` / `cand_attack_centroid_hz` are reported for reading.

The `attack` component is a **composite** of the two: its norm is computed from
both sub-metrics, and its `Raw` carries the rise-time difference purely for
reporting. `attack_available` is false when the compared window carries no
rising onset at all (a decay-only slice, say), in which case the attack term is
left out of the score and the remaining weights are renormalised. Suppress it
explicitly with `Options.SkipAttack`.

### Segment-wise decay (new)

- **`decay_segment_rmse_db_per_s`** — dB/s RMSE across per-segment decay slopes
  (early / mid / late) rather than one global slope. A piano note does not decay
  at a constant rate; the global `decay_diff_db_per_s` averages a fast early
  decay and a slow tail into a single number that two very different envelopes
  can share. The segment metric distinguishes them.

---

## 3. Weighting profiles

`WeightsForProfile(name)` looks a profile up; `Profiles()` lists them.
Every profile's weights are non-negative and sum to 1.0 (`Weights.Validate`).

| Component       | `legacy-v1` | `balanced-v2` | `attack-v1` | `decay-v1` | `inharmonicity-v1` |
| --------------- | ----------- | ------------- | ----------- | ---------- | ------------------ |
| `time`          | **0.30**    | 0.18          | —           | —          | —                  |
| `envelope`      | **0.25**    | 0.16          | 0.05        | 0.20       | —                  |
| `spectral`      | **0.30**    | 0.20          | 0.20        | 0.10       | 0.10               |
| `decay`         | **0.15**    | 0.05          | —           | 0.15       | —                  |
| `partial_level` | 0           | 0.12          | 0.10        | —          | 0.10               |
| `partial_freq`  | 0           | 0.08          | —           | —          | **0.70**           |
| `tristimulus`   | 0           | 0.06          | 0.10        | —          | 0.10               |
| `attack`        | 0           | 0.08          | **0.55**    | —          | —                  |
| `decay_segment` | 0           | 0.07          | —           | **0.55**   | —                  |

- **`legacy-v1`** (the default, returned by `DefaultWeights()`) — the frozen
  four-component score. The six new metrics carry **weight 0**, which is what
  makes `Compare` bit-identical to its pre-2026-08 behaviour. Use it for anything
  that must stay comparable with a recorded number.
- **`balanced-v2`** — the intended successor: every component contributes.
  It now carries `CalibratedNorms()` (§7), so its 0.20 spectral weight buys a
  real gradient rather than a constant. Still not the _default_: `DefaultWeights()`
  must stay `legacy-v1` for score comparability.
- **`attack-v1`** — for an attack-focused fitting pass. Concentrates on onset
  behaviour and early timbre; deliberately ignores decay entirely.
- **`decay-v1`** — for a sustain/decay pass. Segment slopes dominate, with
  envelope shape as support.
- **`inharmonicity-v1`** — for a dispersion/stiffness pass. Partial frequency
  error dominates; the rest is present only to stop the optimizer trading
  everything else away for a cents match.

The per-aspect profiles **are** wired into `--pass`: `passScorer` in
`cmd/piano-fit/pass.go` resolves the pass's profile and calls
`analysis.CompareWithOptions`. `--profile` overrides it and works with
`--pass none` too.

---

## 4. Calibration status of the `Norm*` constants

Measured 2026-08-21 against `reference/c4.wav`, note 60, velocity 118,
release-after 3.5 s, 48 kHz, across the tracked preset population in
`assets/presets/`.

| Constant             | Value      | Where         | Observed range                                                                       | Status                                                                                                                                                                                                                                    |
| -------------------- | ---------- | ------------- | ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `NormTime`           | 0.25       | `distance.go` | ~0.11 (44% of norm)                                                                  | **OK.** Frozen (legacy). Comfortably unsaturated.                                                                                                                                                                                         |
| `NormEnvelope`       | 30.0 dB    | `distance.go` | ~9.0 dB (30%)                                                                        | **OK.** Frozen (legacy). Arguably generous, but it varies.                                                                                                                                                                                |
| `NormSpectral`       | 30.0 dB    | `distance.go` | 51.5–63.7 dB across real presets (172 dB for the degenerate `modal-calibrated.json`) | **BROKEN — saturated for every preset in the repo.** See below.                                                                                                                                                                           |
| `NormDecay`          | 40.0 dB/s  | `distance.go` | ~3.2 dB/s (8%)                                                                       | **Too large.** Not saturated, but the component barely moves; effectively contributes ~0.01 of the 0.15 weight it is given.                                                                                                               |
| `NormPartialLevel`   | 12.0 dB    | `features.go` | 10.6 dB (88%); `default.json` saturates at 13.3 dB                                   | **Marginal.** Right order of magnitude, saturates on the worse half of the population. Candidate for ~20 dB.                                                                                                                              |
| `NormPartialFreq`    | 50.0 cents | `features.go` | 34.8 cents (70%)                                                                     | **OK.**                                                                                                                                                                                                                                   |
| `NormTristimulus`    | 0.5        | `features.go` | 0.277 (55%)                                                                          | **OK.**                                                                                                                                                                                                                                   |
| `NormDecaySegment`   | 30.0 dB/s  | `features.go` | 15.3 dB/s (51%)                                                                      | **OK.**                                                                                                                                                                                                                                   |
| `NormAttackRise`     | 20.0 ms    | `attack.go`   | 24.5 ms — **rise half saturated**                                                    | **Too small.** A 25 ms rise-time miss is ordinary for an un-fitted attack, so `clamp01` pins the rise half of the composite at 1.0; roughly 50 ms would leave headroom. The composite still varies (0.94) because the centroid half does. |
| `NormAttackCentroid` | 0.5 oct    | `attack.go`   | 0.44 oct (88%)                                                                       | **Marginal.** Close to saturation on real material.                                                                                                                                                                                       |

The `attack` component averages the two halves (`0.5·rise + 0.5·centroid`), so
`attack_saturated` fires only when _both_ halves saturate. One saturated half is
already half a lost gradient and is not flagged — check the raw sub-metrics.

Note also that the `NormAttackRise` doc comment claims "the tracked C4 reference
rises in roughly 58 ms"; the measured `ref_rise_time_ms` is **25.5 ms**. The
comment is stale and should be corrected alongside the re-calibration.

### The `NormSpectral` problem

`spectral` carries the largest single weight in `legacy-v1` (0.30) and is
reported as the dominant component — yet at `NormSpectral = 30.0` **every**
preset in the repo exceeds it, so `clamp01` pins the component at exactly 1.0
for all of them. `default.json` (62.2 dB) and `fitted-c4-mayfly.json` (58.2 dB)
are materially different renders that score _identically_ in the term the score
leans on hardest. The optimizer has been flying blind on its own largest
component.

**Resolved (see §7).** The norms are now **per-profile** rather than global, so
the frozen legacy scale and a calibrated one coexist: `legacy-v1` keeps
`LegacyNorms()` and keeps saturating, every other profile carries
`CalibratedNorms()`. Nothing was re-baselined, because nothing had to be.

**Rule for any new profile:** a non-legacy profile must ship _re-calibrated_
norms rather than inheriting the frozen ones. Pick a scale the observed
population actually spans. When the `*Saturated` flags fire across a preset
population, the norm is wrong — not the presets.

---

## 5. Recorded scores are not comparable across 2026-08

`analysis/distance.go` changed **six times** between 2026-02-14 and 2026-08-16,
including phase-detection and normalisation changes. Any score written into a
report before that window was produced by a different metric implementation.

Concretely: `assets/presets/fitted-c4-mayfly.json.report.json` records
`best_score 0.3839` / `spectral_rmse_db 20.77` from 2026-02-14. Re-measured
today at the canonical settings, the same preset scores **`0.5194`** with
`spectral_rmse_db 58.18`. Pristine HEAD and the working tree agree to the last
digit (`0.5194351732385747`), so this is the current definition of the metric —
not a regression introduced by the metric work.

**Always re-measure. Never compare across that boundary.**

---

## 6. Regression gate

The current numbers are pinned by `assets/thresholds/c4.json` and checked by
`just gate-c4`. See `docs/optimization-workflow.md` for the threshold-file
format, the `null`-means-unenforced convention, and the skip-on-missing-
reference behaviour.

The gate deliberately checks the **raw** `spectral_rmse_db`, which is still a
real regression signal, even though the _normalised_ spectral component is
saturated and contributes nothing to the score's gradient.

---

## 7. Per-profile norms (2026-08-21)

The saturation problem in §4 had two requirements that look contradictory:
`legacy-v1` must keep reproducing the numbers recorded in every tracked
`*.report.json` and in `assets/thresholds/c4.json`, and a profile that steers an
optimizer must use a scale the observed population actually spans. They are only
contradictory while there is a single global scale.

`Weights.Norms` (`analysis/norms.go`) makes the scales per-profile. A profile
names the scales it recalibrates and inherits the rest; zero, negative and
non-finite fields count as unset and fall back to `LegacyNorms()`, so there is
no way to divide by zero.

- `legacy-v1` → `LegacyNorms()`. Unchanged, still saturated on spectral, still
  scoring `0.5194351732385747` on the tracked C4 render.
- every other profile → `CalibratedNorms()`.

### Measured population

Note 60, velocity 118, release-after 3.5 s, 48 kHz, against `reference/c4.wav`.
Seven real presets from `assets/presets` plus the three pass outputs in
`out/passes`. `modal-calibrated.json` is excluded as degenerate (172 dB spectral,
883 dB/s decay, 204 dB envelope — a broken preset, not a point on the
distribution).

| component         | observed          | legacy | calibrated | normalized span |
| ----------------- | ----------------- | ------ | ---------- | --------------- |
| `spectral`        | 51.5 – 71.3 dB    | 30.0   | **80.0**   | 0.64 – 0.89     |
| `decay`           | 0.13 – 14.7 dB/s  | 40.0   | **15.0**   | 0.01 – 0.98     |
| `partial_level`   | 10.6 – 27.4 dB    | 12.0   | **35.0**   | 0.30 – 0.78     |
| `partial_freq`    | 34.8 – 48.9 cents | 50.0   | **70.0**   | 0.50 – 0.70     |
| `attack_rise`     | 24.1 – 24.5 ms    | 20.0   | **50.0**   | ~0.49           |
| `attack_centroid` | 0.08 – 1.40 oct   | 0.5    | **1.5**    | 0.06 – 0.93     |
| `time`            | 0.11 – 0.17       | 0.25   | 0.25       | 0.43 – 0.67     |
| `envelope`        | 8.4 – 24.3 dB     | 30.0   | 30.0       | 0.28 – 0.81     |
| `tristimulus`     | 0.15 – 0.40       | 0.5    | 0.5        | 0.30 – 0.80     |
| `decay_segment`   | 12.8 – 23.2 dB/s  | 30.0   | 30.0       | 0.43 – 0.77     |

`TestCalibratedNormsCoverObservedPopulation` replays the worst observed value of
every metric and fails if any weighted component of any non-legacy profile
saturates, so the next metric added cannot quietly repeat the mistake.

### What it bought, measured

The `sustain` pass re-run from `fitted-c4-mayfly.json`, 180 s, `decay-v1`, judged
by re-running the full `legacy-v1` compare on its output preset:

| metric                        | baseline | sustain, legacy norms | sustain, calibrated norms |
| ----------------------------- | -------- | --------------------- | ------------------------- |
| legacy score                  | 0.5194   | 0.5581                | **0.5327**                |
| `spectral_rmse_db`            | 58.18    | 66.88                 | **58.19**                 |
| `partial_level_rmse_db`       | 10.60    | 27.39                 | **13.08**                 |
| `decay_segment_rmse_db_per_s` | 15.32    | 12.84                 | 12.94                     |
| `decay_diff_db_per_s`         | 3.18     | 4.47                  | 3.62                      |
| `envelope_rmse_db`            | 9.01     | 8.97                  | 8.67                      |
| `time_rmse`                   | 0.1104   | 0.1388                | 0.1224                    |

The predicted effect is confirmed exactly. The 9 dB spectral degradation is gone
— 58.18 dB in, 58.19 dB out, held to a hundredth of a dB — and the 13 dB
partial-level degradation shrank to 2.5 dB, while the pass still captured
essentially all of the segmented-decay improvement it was after (15.32 → 12.94,
against 12.84 before).

### The sustain pass still regresses, for a different reason

`0.5194 → 0.5327`. Decomposing the legacy score:

| component  | baseline | after  | delta       |
| ---------- | -------- | ------ | ----------- |
| `time`     | 0.1325   | 0.1469 | **+0.0144** |
| `envelope` | 0.0751   | 0.0722 | −0.0028     |
| `spectral` | 0.3000   | 0.3000 | 0.0000      |
| `decay`    | 0.0119   | 0.0136 | +0.0017     |

The entire remaining regression is the `time` component. `decay-v1` weights
`time` at **0**, while `legacy-v1` weights it at **0.30** — its largest single
term after spectral. The pass is free to trade sample-wise waveform agreement
away, and it does.

So `NormSpectral` is no longer what blocks the sustain pass; the `decay-v1`
weight vector is. The next step is to give `decay-v1` a non-zero `time` weight
(or an explicit anti-regression term) and re-measure — a weight change, which by
the rule in §4 is a separate, measured change. This is progress of the useful
kind: the failure moved from an invisible saturated constant to a visible,
attributable weight.
