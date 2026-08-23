# Resonance and unison coupling — the 2026-08-21…23 investigation record

**What this is.** The measurement record that used to live inline in PLAN.md 12.4
("Validation + performance acceptance"). It was moved here on 2026-08-23 because
the section had grown to roughly 600 lines of findings about work that is done,
which made the remaining open tasks impossible to find.

Everything below is the record as written at the time, in the order the work
happened. It is kept verbatim: several entries correct an earlier entry, and the
correction is only meaningful with the claim it replaces still readable. Where an
entry says "still open", the open task itself now lives in **PLAN.md Phase 18**,
not here.

**The through-line, in one paragraph.** Three separate defects made the string
bank gain energy or measure wrong, and each one masked the next. The sympathetic
loop deposited a whole block of drive into frozen string state, which delivered
the block's DC content instead of the drive at each partial (fixed by
interleaving it with rendering). `noteResonator` was built with a 1/f0 peak gain,
which put the aggregate loop over unity in the bass (fixed by normalising to
unity peak gain). And the unison bridge crossfeed injected a string's own output
back into itself, a bare positive feedback loop that grew the bank with no
sympathetic path connected at all (fixed by making the term dissipative and
writing it at the freshest delay slot). The open-loop probes saw none of it,
because they drive one note's fundamental into a plant they assume is stable —
a passing bound is necessary, never sufficient.

## Contents

- [Cross-core A/B tests and metrics](#cross-core-ab-tests-and-metrics)
  - [DWG vs modal distance on single notes](#dwg-vs-modal-distance-on-single-notes)
  - [DWG vs modal distance on chords](#dwg-vs-modal-distance-on-chords)
  - [Sustain-pedal and coupling parity across cores](#sustain-pedal-and-coupling-parity-across-cores)
  - [Long-render stability (NaN/Inf free)](#long-render-stability-naninf-free)
- [The resonance and coupling defects, in the order they were found](#the-resonance-and-coupling-defects-in-the-order-they-were-found)
  - [Found: the DWG aggregate resonance loop grew with the pedal held](#found-the-dwg-aggregate-resonance-loop-grew-with-the-pedal-held)
  - [Fixed: interleave `InjectFromBridge` with rendering](#fixed-interleave-injectfrombridge-with-rendering)
  - [Found: the C4 gate was buying 14.7 dB from the block-deposit artefact](#found-the-c4-gate-was-buying-147-db-from-the-block-deposit-artefact)
  - [Re-fit of `fitted-c4-mayfly.json` against the corrected renderer (2026-08-23)](#re-fit-of-fitted-c4-mayflyjson-against-the-corrected-renderer-2026-08-23)
  - [Found: the unison bridge coupling was a positive feedback loop](#found-the-unison-bridge-coupling-was-a-positive-feedback-loop)
  - [STILL OPEN: the modal core's unison crossfeed has the same non-passive shape](#still-open-the-modal-cores-unison-crossfeed-has-the-same-non-passive-shape)
  - [Resolved: the interleaved sympathetic loop no longer multiplies that growth](#resolved-the-interleaved-sympathetic-loop-no-longer-multiplies-that-growth)
  - [Fixed: normalise the `noteResonator` bank to unity peak gain](#fixed-normalise-the-noteresonator-bank-to-unity-peak-gain)
- [Gate re-baselining history](#gate-re-baselining-history)
  - [Re-baselining `c4.json` for the resonator normalisation](#re-baselining-c4json-for-the-resonator-normalisation)
  - [Re-fit of the C4 preset against the corrected renderer (2026-08-22)](#re-fit-of-the-c4-preset-against-the-corrected-renderer-2026-08-22)
  - [`piano-fit --match-output-gain` is now score-neutral](#piano-fit---match-output-gain-is-now-score-neutral)
  - [Lifted: the Halton sweep's 8-dimension cap](#lifted-the-halton-sweeps-8-dimension-cap)
  - [STILL OPEN: recover sympathetic resonance level](#still-open-recover-sympathetic-resonance-level)
- [Probe calibration](#probe-calibration)
  - [Fixed: the open-loop probes read a transient, not a steady state](#fixed-the-open-loop-probes-read-a-transient-not-a-steady-state)
  - [`cmd/piano-modal-fit` gained `--no-resonance`](#cmdpiano-modal-fit-gained---no-resonance)
  - [STILL OPEN: re-fit `assets/presets/modal-calibrated.json`](#still-open-re-fit-assetspresetsmodal-calibratedjson)

## Cross-core A/B tests and metrics

### DWG vs modal distance on single notes

DWG vs modal distance on selected single notes (`piano/core_distance_test.go`. Re-measured 2026-08-21 after the DWG DC
and injection fixes: 0.83-0.84, bound tightened 0.90 → 0.89. The cores
still do **not** match. Removing the DC changed the score by less than
0.01, so the DC was not the cause: what keeps whole analysis windows at
digital silence is that the DWG excitation fills only 4-25% of the delay
line, so the bank output is a sparse impulse train. Closing that gap
needs a distributed excitation or a loop filter with real bandwidth.)

### DWG vs modal distance on chords

DWG vs modal distance on chords (`TestDWGModalDistanceIsBoundedOnChords` in `piano/core_distance_test.go`.
Measured 2026-08-22, 750 blocks, sustain held: C3 major (48/55/60/64)
0.8434, C4 major (60/67/72/76) 0.8401. Same 0.89 bound as the single-note
test, so polyphony is required not to make the cores diverge further.)

### Sustain-pedal and coupling parity across cores

sustain pedal and coupling behavior parity checks (`piano/core_parity_test.go`. Mid-render pedal lift, tail RMS over the
final 80 blocks: DWG held/lifted 2.55x, modal 8.19x, bound 1.50x.
Coupling-mode ordering in a silent note 72: off exactly 0 in both cores,
static 1.89e-09 (DWG) / 1.84e-09 (modal), physical 3.35e-10 / 3.10e-10 —
the static > physical > off ordering is asserted to match across cores.
These pin relative behaviour only; absolute agreement is impossible while
the distance sits at 0.83-0.84.)

### Long-render stability (NaN/Inf free)

long-render stability (NaN/Inf free) (`TestLongRenderHasNoNaNOrInf` in `piano/integration_test.go`, see
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

## The resonance and coupling defects, in the order they were found

### Found: the DWG aggregate resonance loop grew with the pedal held

**the DWG core's aggregate resonance loop grows with the sustain pedal held.** Found while answering the review above; it predated the modal
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

### Fixed: interleave `InjectFromBridge` with rendering

follow-up: **interleave `InjectFromBridge` with rendering instead of driving a frozen string state.** Done 2026-08-22. The loop now runs
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

### Found: the C4 gate was buying 14.7 dB from the block-deposit artefact

**the C4 gate was buying 14.7 dB of its spectral score from the block-deposit artefact.** Found while re-baselining for the interleave.
`assets/thresholds/c4.json` `spectral_rmse_db` was LOOSENED 62.3 → 78.3,
by far the largest loosening in that file's history; the other four
enforced thresholds were TIGHTENED in the same pass.

The decisive measurement is the same preset with `resonance_enabled:
false`, a path this change cannot execute a single instruction on:

|                       | score    | spectral  | high(2k+) | frames |
| --------------------- | -------- | --------- | --------- | ------ |
| resonance OFF, before | 0.529104 | 72.531788 | 77.215274 | 177792 |
| resonance OFF, after  | 0.529104 | 72.531788 | 77.215274 | 177792 |

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

### Re-fit of `fitted-c4-mayfly.json` against the corrected renderer (2026-08-23)

**re-fit `assets/presets/fitted-c4-mayfly.json`.** Done 2026-08-23, paying off the standing debt from the interleave AND the one the unison
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

|                     | old preset | re-fit | threshold |
| ------------------- | ---------- | ------ | --------- |
| score               | 0.5738     | 0.5335 | 0.568     |
| time_rmse           | 0.1009     | 0.1070 | 0.109     |
| envelope_rmse_db    | 14.68      | 10.86  | 11.62     |
| spectral_rmse_db    | 74.70      | 77.35  | 78.3      |
| decay_diff_db_per_s | 8.13       | 3.90   | **4.19**  |

**Nothing was loosened.** `decay_diff_db_per_s` was tightened 4.72 → 4.19
and the other four stayed where they were, even though their
measurements got worse, because the measured+8% convention would have
widened all four and the no-loosening rule outranks it. The 78.3 spectral
fence the interleave asked for on an explicit promise of a re-fit was
honoured at 77.38 without asking for another inch. Un-enforced metrics
mostly improved sharply: tristimulus 0.230 → 0.098, attack centroid 0.947
→ 0.252, segmented decay 16.75 → 9.28.

The re-fit chose `unison_crossfeed` 0.0034, higher than the 0.0025 it
replaced. That is not the fitter finding the pump again — the corrected
term takes energy out at any strength — it is buying beating and
two-stage decay, which is what the term is for.

Still open and NOT closed by this: `spectral_rmse_db` sits at 99% of its
budget with the high band at 82.1 dB. That is Phase 11's known HF
deficiency with nothing masking it, it is a model problem rather than a
fitting one, and no further re-fit will close it.

### Found: the unison bridge coupling was a positive feedback loop

**the DWG bank grew on its own with the pedal held, with no sympathetic path at all — the unison bridge coupling was a positive feedback loop.**
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

| note | strings | before | after    | `c = 0`  |
| ---- | ------- | ------ | -------- | -------- |
| 33   | 1       | 0.1469 | 0.146883 | 0.146883 |
| 45   | 2       | 0.5583 | 0.003556 | 0.025640 |
| 52   | 2       | 1.2545 | 0.000326 | 0.006317 |
| 60   | 2       | 1.7159 | 0.000003 | 0.000349 |

The fix is two changes, both load-bearing. The force is now
`c * g_i * (mix - y_i)` — proportional to the **difference** between the
bridge motion and the string's own contribution to it, which makes the
term dissipative: with weights summing to one it adds
`c*(mix^2 - sum(g_i*y_i^2)) <= 0` of energy per sample, by Jensen. And it
is written with `StringWaveguide.InjectForceNext`, into the slot the
interpolating taps read on the very next `Process` call.

The second half is not cosmetic. The energy argument is
**instantaneous** — it holds only while the force acts on the signal it
was computed from — so a force returning a fraction _p_ of a round trip
later lags `2*pi*n*p` at partial _n_ and is anti-damping once that passes
a half cycle. And a strike position cannot express "next sample":
`injectionOffset` maps `[0,1]` affinely onto the round trip, so even its
clamped minimum of 0.01 is 1% of it — about 1 sample at MIDI 60 but 5 at
MIDI 40 and 17 at MIDI 21, and MIDI 40 is exactly where multi-string
groups begin. Measured on the chord render:

| `unison_crossfeed`        | pos 0.92    | pos 0.01 | `InjectForceNext` |
| ------------------------- | ----------- | -------- | ----------------- |
| 0.0008 (default)          | 0.1166      | 0.1275   | 0.1270            |
| 0.0025 (mayfly)           | 0.2014      | 0.1315   | 0.1306            |
| 0.0050 (`fitted-c4.json`) | **88.4618** | 0.1328   | 0.1325            |
| diverges at               | < 0.005     | 0.1      | 0.5               |

Read the third row and the last together. At the value
`assets/presets/fitted-c4.json` actually ships, the difference form on
its own still diverged; writing the force at the freshest slot widens the
stable range by a further 5x on top of that. `NewStringBank` clamps to
`maxUnisonCrossfeed = 0.02` — 4x above the knob ceiling in
`cmd/piano-fit/knobs.go` and 12x under the measured cliff.

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

### STILL OPEN: the modal core's unison crossfeed has the same non-passive shape

OPEN — follow-up: **the modal core's unison crossfeed has the same non-passive shape.** `ModalStringGroup.applyCrossfeed` still adds
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

### Resolved: the interleaved sympathetic loop no longer multiplies that growth

**the interleaved DWG sympathetic loop no longer multiplies that growth.** Fixed by the item above, 2026-08-23. The same 120 s render at
the shipped `resonance_gain` 0.00025 now reads **0.1338x** against the
0.1270x resonance-off baseline: the sympathetic path costs about 5% of
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

### Fixed: normalise the `noteResonator` bank to unity peak gain

normalise the `noteResonator` bank. Done 2026-08-22: `b0` is now `(1-r)*sqrt(1 - 2r*cos(2*w0) + r^2)`, which makes the peak exactly one
at every centre frequency in both cores.
`TestNoteResonatorHasUnityPeakGain` and
`TestResonanceDriveBankGainIsRegisterIndependent`
(`piano/resonance_normalisation_test.go`) pin it analytically and
against a driven sine. `ResonanceGain` was deliberately **not** re-fitted
with it; see the three follow-ups below, which that decision opened.

## Gate re-baselining history

### Re-baselining `c4.json` for the resonator normalisation

re-baselined `assets/thresholds/c4.json` for the resonator normalisation. Every shipped preset pins `resonance_gain: 0.00025`, so
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

### Re-fit of the C4 preset against the corrected renderer (2026-08-22)

follow-up: **re-fit the C4 preset against the corrected renderer.** Done 2026-08-22. Superseded on 2026-08-23 by a further re-fit against the
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

### `piano-fit --match-output-gain` is now score-neutral

**`piano-fit --match-output-gain` is now score-neutral.** It was not: the auto-stop was an **absolute** −90 dBFS threshold, so a louder render
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

### Lifted: the Halton sweep's 8-dimension cap

follow-up: **the Halton sweep caps at 8 dimensions.** _Done:_ the base table now holds the first 32 primes and `--sweep-joint-max-dims`
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

### STILL OPEN: recover sympathetic resonance level

OPEN — follow-up: **recover sympathetic resonance level.** The normalisation is correct but it removes the mechanism that made sympathetic resonance
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
of both, and with it corrected the same render decays to 0.1270x
(resonance off) and 0.1331x (resonance on at 0.00025).

**The headroom is back, and it is measurable.** Same 120 s render,
sweeping `resonance_gain`: 0.00025 → 0.1331, 0.0007 → 0.2996, 0.00092 →
0.7847, 0.0014 → 14.02. The unity crossing lands at roughly 0.00092 —
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

## Probe calibration

### Fixed: the open-loop probes read a transient, not a steady state

follow-up: **the open-loop probes in `piano/modal_resonance_test.go` read a transient, not a steady state.** Fixed 2026-08-22 by replacing
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

### `cmd/piano-modal-fit` gained `--no-resonance`

follow-up: make `cmd/piano-modal-fit` disable resonance during fitting the way `cmd/piano-fit/main.go:212-214` does.
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

### STILL OPEN: re-fit `assets/presets/modal-calibrated.json`

OPEN — follow-up: **re-fit `assets/presets/modal-calibrated.json`.** The tool is now capable of a clean fit, but the shipped preset is still the one
produced against diverging resonance renders, and `analysis/norms.go:55`
still has to exclude it as a degenerate outlier. Re-fitting it is its
own piece of work: the new preset changes the norm corpus, so the
exclusion and the normalisation constants have to be re-derived together
with it. The exclusion stays exactly as it is until then.
