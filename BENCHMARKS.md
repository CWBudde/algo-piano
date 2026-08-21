# Benchmarks

Performance baselines for `algo-piano`. Re-run with `just bench`, or a single
family with:

```bash
go test ./piano/ -run='^$' -bench='BenchmarkModalKernels' -benchmem -benchtime=500x -count=5
```

All figures below are `sec/op` for one `StringBank.Process(128, nil)` call at
48 kHz, reported as the median of 10 runs with `benchstat` confidence where a
comparison is drawn. Benchmarks re-strike every note every 64 iterations (with
the timer stopped) so the bank stays realistically loaded instead of ringing
down into silence.

## Machine

|                |                                                             |
| -------------- | ----------------------------------------------------------- |
| CPU            | 12th Gen Intel Core i7-1255U (12 threads, AVX2, no AVX-512) |
| OS             | Linux 6.8.0 amd64                                           |
| Go             | 1.26.5                                                      |
| Date           | 2026-08-16; convolver and idle sections 2026-08-21          |
| `algo-vecmath` | v0.1.3                                                      |

Note that `algo-vecmath` v0.1.3 has **no arm64 NEON `RotateDecay*` kernel**
(upstream VEC-306 is marked done but not implemented), so arm64 falls back to
the generic scalar backend and none of the SIMD gains below apply there. Any
arm64 figures must be measured separately.

## Modal core: kernel selection

`BenchmarkModalKernels`, 8 keys held with sustain and physical coupling, which
settles at ~86 active notes and ~1500 live modes.

| Path              | sec/op | vs arena |
| ----------------- | ------ | -------- |
| `arena` (default) | 390 µs | —        |
| `pergroup_accum`  | 598 µs | +53%     |
| `pergroup_rotate` | 601 µs | +54%     |
| `pergroup_scalar` | 656 µs | +68%     |

Measured against the pre-refactor array-of-structs implementation:

| Config          | AoS (before) | arena (after) | Change                     |
| --------------- | ------------ | ------------- | -------------------------- |
| 8 active notes  | 34.4 µs      | 25.2 µs       | **−26.9%** (p=0.000, n=10) |
| 86 active notes | 525 µs       | 413 µs        | **−21.4%** (p=0.000, n=10) |

### Why per-group SIMD lost

Calling `algo-vecmath` once per note was measured **slower** than the original
scalar loop it replaced — +8.1% to +10.6% at 86 active notes (p<0.005). A single
note holds only ~24 modes, i.e. three AVX2 iterations, so the per-call cost
(seven slice headers, bounds checks, dispatch indirection) outweighs the
vectorization, and the struct-of-arrays layout costs scalar cache locality.

Compacting every active group into one arena and making a single kernel call per
sample amortizes that overhead across ~1500 modes instead of ~24, which is what
turns the regression into the −21% to −27% above. This is worth remembering
before adding SIMD anywhere else in the voice path: at these mode counts, batch
width is the whole game.

## Denormal flush

The largest single win in the modal path, and unrelated to SIMD. High partials
decay into the float32 denormal range while the fundamental is still audible;
a sustained group never deactivates, so they stall there indefinitely.

| Mode state denormal | After 500 blocks | After 2000 blocks |
| ------------------- | ---------------- | ----------------- |
| Before              | 27.5%            | 93.0%             |
| After               | 0%               | 0%                |

| Config                           | Before | After   |
| -------------------------------- | ------ | ------- |
| Sustained decay, 86 active notes | 4.2 ms | 0.59 ms |

Cost was previously a function of how long the run had been going, which also
made every earlier modal benchmark unreliable.

**Open gap: the flush rate is set by the caller.** `flushDenormalModes` runs from
`endBlock`, once per `StringBank.Process` call, not on a fixed sample stride.
`Piano.Process` accepts any block length, and at the slowest per-sample decay a
mode just above the flush threshold enters the denormal range after 197 samples —
fine for a 128-frame callback, not fine for the 4800- and 48000-frame calls the
offline tests use. Measured on the sustained-decay scenario, ~256k frames per
case, four runs:

| Block length   | Cost per frame |
| -------------- | -------------- |
| 128 (callback) | 870-943 ns     |
| 4800           | 813-845 ns     |
| 48000          | 1712-1813 ns   |

A 48000-frame call costs about 2x per frame what the callback-sized call does.
The realtime path is unaffected; offline rendering is not. See the skipped
`TestModalFlushSurvivesLongProcessCalls` in `piano/denormal_test.go`.

## Convolver cost

All convolver figures are the median of five `-count=5` runs at default
benchtime, measured 2026-08-21 at load average 1 to 3.

`BenchmarkSoundboardConvolverIRLength` / `BenchmarkBodyConvolverIRLength`, one
128-frame block per iteration at the production partition size of 128 — the
shape of a single audio callback. The realtime budget for that block at 48 kHz
is **2.67 ms**.

| IR length     | Room (stereo) | Body (mono) | Room, % of budget |
| ------------- | ------------- | ----------- | ----------------- |
| 128 (2.7 ms)  | 4.0 µs        | 2.0 µs      | 0.2%              |
| 1024 (21 ms)  | 22 µs         | 9.5 µs      | 0.8%              |
| 8192 (170 ms) | 0.38 ms       | 95 µs       | 14%               |
| 48000 (1 s)   | 1.06 ms       | 0.45 ms     | **40%**           |
| 192000 (4 s)  | 16.0 ms       | 6.9 ms      | **599%**          |

Cost grows with IR length, as uniform partitioning implies. The practical
consequence: at the default `partSize = 128` a one-second stereo room IR eats
40% of the entire block budget before the string bank has been charged for
anything, and a four-second room IR is 6x over budget on its own. Four-second
room IRs are not viable at this partition size.

### Partition size is not a lever for realtime cost

`BenchmarkSoundboardConvolverPartitionSize` / `BenchmarkBodyConvolverPartitionSize`
render a fixed 4096 frames per iteration so every partition size does the same
acoustic work. Room IR is 1 s, body IR is 8192 taps. Those 4096 frames are
delivered as 32 separate 128-frame `Process` calls, because that is the shape the
engine actually runs in; the normalized columns divide by 32 to give the cost of
one real 128-frame callback.

| Partition | Room / 4096 fr | Room / callback | Room, % of budget | Body / 4096 fr | Body / callback |
| --------- | -------------- | --------------- | ----------------- | -------------- | --------------- |
| 64        | 61.9 ms        | 1.94 ms         | 73%               | 6.05 ms        | 0.19 ms         |
| 128       | 30.6 ms        | 0.96 ms         | 36%               | 3.05 ms        | 0.095 ms        |
| 256       | 29.8 ms        | 0.93 ms         | 35%               | 3.17 ms        | 0.099 ms        |
| 512       | 30.8 ms        | 0.96 ms         | 36%               | 3.20 ms        | 0.100 ms        |
| 1024      | 30.9 ms        | 0.96 ms         | 36%               | 3.30 ms        | 0.103 ms        |

Cross-checks against the IR-length table: room at partition 128 is 0.96 ms here
against 1.06 ms there, and body at partition 128 is 95 µs against that table's
8192 row at 95 µs.

**Raising the partition size above 128 buys nothing.** The room path is flat from
128 to 1024 — 0.93 to 0.96 ms, inside run-to-run noise — and the body path drifts
slightly upwards. Only 64 stands out, at almost exactly 2x the 128 cost.

An earlier revision of this document claimed the opposite: that 128 -> 256 cut the
room convolver by about 3x, from 86% of the block budget to 26%, and that 1024
got it to 12%. That was an artefact of the benchmark, which used to hand the
convolver all 4096 frames in a single `Process` call. `SoundboardConvolver.Process`
consumes its input in `partSize` chunks, so one big call amortizes each partition
over `partSize` frames of output, and dividing by 32 then produced a number no
realtime caller can obtain.

Driving it callback-by-callback shows the real behaviour, and the flat curve is
what the arithmetic predicts. Per `Process` call the convolver transforms one
partition and multiplies it against all `irLen / partSize` IR partitions, so the
work is `O(irLen * log partSize)` per call — near-constant in `partSize`, with
only the weak log term left, which is the slight upward drift on the body path.
`partSize = 64` is the one real outlier because a 128-frame callback then contains
two inner blocks: exactly twice the FFT round-trips, and the measurement is
61.9 / 30.6 = 2.02x.

There is a second reason not to raise it, independent of cost. Above 128,
`Process` pads each short callback up to `partSize`, advances one full overlap-add
partition, and returns only the first 128 samples — so the rest of that
partition's output is discarded and the stream is wrong, not merely expensive.

Turning partition size into a genuine lever would take cross-callback buffering
inside the convolver: accumulate callbacks until a full partition is available,
then drain that partition's output over the following callbacks, trading one
partition of latency for the amortization. That does not exist today. Until it
does, the only real levers on room-convolver cost are IR length and a non-uniform
partitioning scheme.

Re-measure with:

```bash
go test ./piano/ -run='^$' -bench='Convolver' -benchmem -count=5
```

Room `ir8k` was the noisiest case, spreading 0.32 to 0.64 ms across the five
runs; everything else held within about 10%.

## Idle string bank

`BenchmarkStringBankIdle`: the full persistent 88-key bank, physical coupling
configured, nothing struck. This is PLAN.md 9.6's "idle full-string-bank cost".

Measured between **74 ns and 1.1 µs** per 128-frame block across runs, with zero
allocations — at worst 0.04% of the 2.67 ms budget. The spread is noise, not
signal: `StringBank.Process` short-circuits on an empty active set, so DWG and
modal execute the identical code path and neither the core nor the pedal state
can matter. A held sustain pedal lifts every damper but does not by itself put
groups into the active set.

The answer the benchmark exists to give is therefore: **owning the whole 88-key
persistent bank costs nothing measurable when nothing is sounding.** Any future
regression here would mean the empty-active-set short circuit stopped working.

## Excitation shape cache

_Measured 2026-08-21, Go 1.26.5, same machine as above._

`injectAtPosition` evaluated `math.Sin` once per mode per call to get the mode
shape at the strike position. Every excitation path went through it: the hammer
during contact, each coupling edge, and — by far the heaviest — sympathetic
resonance, which with the sustain pedal down drives all 128 groups once per
sample.

The shape vector depends only on `(order[], strikePos)`, and `order` is fixed at
construction, so it is now cached per group: one precomputed slot for each of the
two compile-time strike positions (resonance 0.82, coupling 0.9) plus a
one-entry cache for the hammer position, keyed on the _clamped_ value. Steady
state evaluates no transcendental at all. The vectors are carved out of the
group's existing `soaBuf`, so the path stays allocation-free, and they are
deliberately group-owned rather than arena-backed: they are static, and the arena
only compacts state that evolves.

`BenchmarkModalInjectAtPosition`, one mid-register group (~24 modes), one
excitation call per iteration, `benchstat` over 12 interleaved runs:

| Path                    | Before | After   | Change                     |
| ----------------------- | ------ | ------- | -------------------------- |
| `resonance` (pos 0.82)  | 303 ns | 47.1 ns | **−84.5%** (p=0.000, n=12) |
| `coupling` (pos 0.9)    | 337 ns | 42.4 ns | **−87.4%** (p=0.000, n=12) |
| `hammer_fixedPos`       | 344 ns | 41.5 ns | **−87.9%** (p=0.000, n=12) |
| `hammer_alternatingPos` | 360 ns | 304 ns  | ~ (p=0.219, n=12)          |

`hammer_alternatingPos` is the worst case for the single hammer slot: a normal
and a soft-pedal-shifted strike position in strict alternation, so the cache
refills on every call. It is not a regression — the refill costs what the old
inline computation cost — and it only occurs while two strikes with different
positions overlap on the same note.

At block level, `BenchmarkModalResonanceInjection` (8 keys, sustain held, all 128
groups undamped targets, physical coupling), the same interleaved comparison
over 10 runs:

| Config                    | Before  | After   | Change                     |
| ------------------------- | ------- | ------- | -------------------------- |
| `perNoteFilter` (default) | 6.12 ms | 1.78 ms | **−70.9%** (p=0.000, n=10) |
| `flatDrive`               | 5.77 ms | 1.48 ms | **−74.4%** (p=0.000, n=10) |

With resonance **disabled** the change is not measurable above this machine's
noise. `BenchmarkModalPolyphonyScaling` (all 10 sub-benchmarks) and
`BenchmarkStringBankCouplingModes` (all 30) report `~`.

`BenchmarkModalKernels` should be read as **no result**, not as a small win. Two
independent paired runs disagree with each other about which variant moved:

| Variant           | Run 1 (loaded) | Run 2 (quiet)    |
| ----------------- | -------------- | ---------------- |
| `arena`           | ~ (p=0.052)    | ~ (p=0.219)      |
| `pergroup_scalar` | ~ (p=0.052)    | ~ (p=0.977)      |
| `pergroup_accum`  | ~ (p=0.219)    | −21.2% (p=0.045) |
| `pergroup_rotate` | ~ (p=0.068)    | −47.4% (p=0.002) |

Run-to-run spreads are ±31–69%, and a −47% swing in `pergroup_rotate` is not
physically plausible for a change that only touches the excitation path. These
are sampling artefacts of a noisy machine, and the p-values that cross 0.05 are
not evidence of anything. Do not quote them.

The flat result is expected: coupling injection runs once per edge rather than
once per note per sample, and `BenchmarkStringBankCouplingModes` uses the DWG
core, which never touches this code. Resonance is where the cost was.

Output is **bit-exact** against the previous implementation, verified by hashing
full renders (three configurations × scalar and arena kernels) before and after.
The division by the partial order is deliberately _not_ folded into the table:
folding it reassociates `force*sg*modeScale*shape/order` and was measured to
change the float32 rounding of the render. Only the `math.Sin` and the
inaudible-mode branch move into the table.

### A note on measuring this

The figures above were taken by building two test binaries (baseline and
change), then alternating them round by round rather than running all of one and
then all of the other. The machine was under heavy concurrent load, and a
straight before-then-after run produced obvious artefacts — including a −49%
"improvement" in a DWG benchmark this change cannot touch. Interleaving cancels
the drift; the per-run spread stays wide, but the paired comparison is sound.

The whole comparison was then repeated once the machine went quiet. The
microbenchmark reproduced closely (−86.3%, −87.5%, −85.5%, and `~` for the
alternating case), which is what makes those figures trustworthy.
`BenchmarkModalKernels` did _not_ reproduce — see above. Repeating a paired run
end to end, and requiring it to agree with itself, is worth more here than any
single p-value.

## Polyphony scaling

`BenchmarkModalPolyphonyScaling`, sustain held, **coupling disabled**. Coupling
is off here on purpose: sympathetic resonance excites most of the keyboard
within a few blocks, so the active set saturates at ~86 notes regardless of how
many keys are held and the sweep stops measuring polyphony at all.

| Keys | Strings | DWG     | Modal   |
| ---- | ------- | ------- | ------- |
| 1    | 1       | 2.6 µs  | 3.1 µs  |
| 8    | 12      | 18.6 µs | 18.6 µs |
| 16   | 28      | 43.3 µs | 45.4 µs |
| 32   | 60      | 127 µs  | 89.9 µs |
| 64   | 154     | 237 µs  | 241 µs  |

Both cores scale roughly linearly in string count. **The modal core is not
currently cheaper than DWG** at the default 8 partials — the two are within
noise of each other across the sweep. PLAN.md 12.4's "low CPU profile defaults
to modal" rule should not be adopted on the assumption that modal is faster;
decide it on measured numbers and on how far `modal_partials` can be reduced
before quality suffers.

## Coupling modes at active polyphony

_Measured 2026-08-21, Go 1.26.5, same machine as above; median of three
`-count=3` runs at default benchtime. **This machine was under heavy concurrent
load** — load average 13 to 24 throughout — so read the columns against each
other, not as absolute budget figures._

`BenchmarkStringBankCouplingModes`, PLAN.md 9.6's "active polyphony with
coupling off/static/physical". DWG core, one 128-frame block at 48 kHz, budget
2.67 ms. Every case is allocation-free.

| Case                    | Pedal | off      | static    | physical  |
| ----------------------- | ----- | -------- | --------- | --------- |
| poly1 mid (2 strings)   | up    | 7.4 µs   | 111 µs    | 280 µs    |
| poly1 mid (2 strings)   | down  | 7.0 µs   | 350 µs    | 590 µs    |
| poly8 low (9 strings)   | up    | 43 µs    | 255 µs    | 1.48 ms\* |
| poly8 low (9 strings)   | down  | 238 µs\* | 3.03 ms\* | 866 µs\*  |
| poly8 mid (16 strings)  | up    | 62 µs    | 573 µs    | 843 µs    |
| poly8 mid (16 strings)  | down  | 58 µs    | 641 µs    | 701 µs    |
| poly8 high (24 strings) | up    | 65 µs    | 479 µs    | 639 µs    |
| poly8 high (24 strings) | down  | 66 µs    | 587 µs    | 713 µs    |
| poly8 mixed (18 str.)   | up    | 53 µs    | 332 µs    | 596 µs    |
| poly8 mixed (18 str.)   | down  | 58 µs    | 694 µs    | 694 µs    |

\* The four `poly8_low` figures landed in the worst of the load spike and are
not trustworthy: their three runs spread 608 µs to 2.30 ms (up/physical),
1.79 ms to 4.04 ms (down/static) and 734 µs to 23.4 ms (down/physical). Every
other row held within about ±15%. Re-measure that register on a quiet machine
before drawing any conclusion from it.

What the stable rows say:

- **Coupling, not polyphony, is the dominant cost.** At poly8 mid, going from
  `off` to `physical` is 58 µs to 701 µs — a 12x increase for the same eight
  held keys. Compare the uncoupled sweep below, where getting from 16 to 130
  voices costs only 8.6x.
- The reason is the one the density section makes explicit: coupling recruits
  voices. Eight struck keys become ~88 sounding groups, so the coupled cases are
  really rendering the whole keyboard.
- `static` is consistently cheaper than `physical` but not by much — roughly
  0.7x to 0.9x on the stable rows — because both recruit the same way. Choosing
  `static` for CPU reasons buys far less than the mode names suggest.
- Holding the sustain pedal raises coupled cost (poly1 mid: 111 µs to 350 µs
  static, 280 µs to 590 µs physical) and leaves uncoupled cost untouched, which
  is the expected shape: undamped targets accept injected energy and then have
  to be rendered.

## Coupling graph density and top-K scaling

_Measured 2026-08-21, Go 1.26.5, same machine as above; median of five
`-count=5` runs at default benchtime._

`BenchmarkStringBankCouplingGraphDensity`, PLAN.md 9.6's "coupling graph
density/top-K scaling vs CPU". Polyphony is held fixed at eight mid-register
keys with the sustain pedal down and physical coupling on; the only thing that
moves is how dense the precomputed sparse graph is. Each case reports the graph
it ran on next to the timing: `edges` (directed edges in the whole 88-key
graph), `active-edges` (edges leaving the active set) and `active` (notes in the
active set), all sampled after the measured loop.

Sweeping the `coupling_max_neighbors` top-K cap:

| maxNeighbors | edges | active | active-edges | sec/op | % of budget |
| ------------ | ----- | ------ | ------------ | ------ | ----------- |
| 1            | 88    | 8      | 8            | 25 µs  | 0.9%        |
| 2            | 176   | 62     | 124          | 169 µs | 6.3%        |
| 4            | 352   | 88     | 352          | 348 µs | 13%         |
| 8 (default)  | 704   | 88     | 704          | 339 µs | 13%         |
| 16           | 1408  | 88     | 1408         | 383 µs | 14%         |
| 32           | 2597  | 88     | 2597         | 405 µs | 15%         |
| 64           | 2792  | 88     | 2792         | 541 µs | 20%         |
| 87           | 2792  | 88     | 2792         | 420 µs | 16%         |

64 and 87 build the identical graph: the compile-time weight floor
`couplingPhysicalMinScore` already rejects everything past ~32 candidates per
source on average, so no source has 64 survivors and the cap stops binding
somewhere between 32 and 64. Their 541 µs against 420 µs is therefore a direct
readout of this machine's run-to-run noise on this benchmark, not a density
effect. Treat
differences below about 30% here as noise.

Sweeping an edge-weight floor instead, at a fixed top-K of 32. The production
floor is a compile-time constant, so the benchmark prunes the built graph:
every edge carrying less than `minShare` of its source's total outgoing gain is
dropped, without renormalising what survives.

| minShare | edges | active | active-edges | sec/op | % of budget |
| -------- | ----- | ------ | ------------ | ------ | ----------- |
| 0.00     | 2597  | 88     | 2597         | 416 µs | 16%         |
| 0.02     | 897   | 88     | 897          | 378 µs | 14%         |
| 0.05     | 566   | 88     | 566          | 419 µs | 16%         |
| 0.10     | 228   | 62-80  | 160-207      | 415 µs | 16%         |
| 0.20     | 100   | 10     | 11           | 70 µs  | 2.6%        |

**Edge count is not the CPU lever.** Cutting the graph from 2597 edges to 566 —
a 4.6x reduction, and the `active-edges` column confirms the per-sample edge work
fell by the same factor — moves the block cost from 416 µs to 419 µs, which is
to say not at all. The same holds on the top-K side: 4 to 87 neighbours is an 8x
edge increase for roughly 20%, inside the noise band above.

What actually costs is the active-voice count the graph _recruits_.
`InjectCouplingForce` enrols any target it drives into the active set, so a graph
dense enough to reach the whole keyboard turns 8 struck keys into 88 sounding
groups, and rendering those 88 groups dominates everything the coupling loop
itself does. The two rows where cost collapses are exactly the two rows where
recruitment fails: `maxNeighbors=1` (8 active, 25 µs) and `minShare=0.20`
(10 active, 70 µs). Between them, the transition is abrupt — `maxNeighbors=2`
already recruits 62 voices and costs 169 µs.

Two consequences for tuning:

- Lowering `coupling_max_neighbors` to save CPU only works if it drops far
  enough to stop the cascade, and that point (1 to 2 neighbours) is far below
  any setting that sounds like a piano. As a performance knob it is close to
  useless; as a voicing knob it is nearly free.
- Any future budget work on the coupled case should go at the cost of a
  sounding group, or at capping how many groups coupling may recruit, not at
  making the graph sparser.

The `active` column for `minShare=0.10` varies between runs (62 to 80) because
recruitment is still in progress when the measured loop ends; it is sampled
after the loop, so a longer run recruits more. The saturated rows (88) and the
collapsed rows are stable.

## Voice cost per block and polyphony sweep

_Measured 2026-08-21, Go 1.26.5, same machine as above; median of five
`-count=5` runs at default benchtime, at load average 8 to 13. Per-case spreads
are ±10% to ±25%; the linearity below is the trustworthy part, the third digit
is not._

`BenchmarkStringBankVoiceCostPerBlock`, one 128-frame block at 48 kHz — the
shape of a single audio callback, budget 2.67 ms. This closes both of PLAN.md
13's remaining benchmark boxes: "Voice cost per block at 48k/128 frames" and
"Polyphony sweep (16/32/64/128 voices)".

A **voice is one sounding string**, not one key. The bank spans 88 keys, so 128
simultaneous voices is unreachable if a voice is a key, and the per-string cost
is what the DSP actually pays. The sweep holds contiguous key ranges starting at
MIDI 36 and sized to land on each voice target; 128 lands on 130 because the top
of that range is three-string territory and the count steps by three.

Every range stops at or below **MIDI 91, deliberately**. The DWG core has a known
defect from roughly MIDI 96 up — runaway DC that never converges, and bit-exact
silence at MIDI 106-108 — documented under Phase 13 in PLAN.md and reproduced by
the skipped `TestTrebleRegisterCollapsesInDWGCore`. A 128-voice sweep run over the
natural range would spend its top third inside that defect, and the DWG column
would then be measuring a broken filter loop rather than voice cost. Capping at
91 keeps the two cores comparable.

Coupling is **disabled**, matching `BenchmarkModalPolyphonyScaling` and for the
same reason: with coupling on, the active set saturates at ~88 notes regardless
of how many keys are held and the sweep stops measuring polyphony. The coupled
case is the section above.

| Voices | Keys  | DWG sec/op | DWG % budget | DWG ns/voice-block | Modal sec/op | Modal % budget | Modal ns/voice-block |
| ------ | ----- | ---------- | ------------ | ------------------ | ------------ | -------------- | -------------------- |
| 16     | 36-45 | 63.7 µs    | 2.4%         | 3980               | 72.0 µs      | 2.7%           | 4502                 |
| 32     | 36-53 | 121 µs     | 4.5%         | 3785               | 133 µs       | 5.0%           | 4160                 |
| 64     | 36-69 | 265 µs     | 9.9%         | 4138               | 330 µs       | 12.4%          | 5150                 |
| 130    | 36-91 | 546 µs     | 20.5%        | 4199               | 614 µs       | 23.0%          | 4726                 |

**Voice cost per block is flat at roughly 4 µs per voice**, 3.8 to 4.2 µs on
DWG and 4.2 to 5.3 µs on modal, over an 8x range of polyphony. Both cores scale
linearly in string count with no super-linear term: nothing in the uncoupled
voice path is quadratic in polyphony.

The headline budget figure: **130 uncoupled voices fit in about 20% of one
48 kHz / 128-frame callback**, so the string bank alone leaves ample room. Note
what that budget does _not_ include — a one-second stereo room IR is another 40%
on its own (see "Convolver cost"), and turning coupling on costs far more than
the voices do, because it recruits voices that were not struck.

Modal is again not cheaper than DWG at the default 8 partials, consistent with
the "Polyphony scaling" section above; it is 6% to 25% more expensive across
this sweep. The `%budget` and `ns/voice-block` metrics are reported by the
benchmark itself via `b.ReportMetric`, so they need no post-processing.

Re-measure with:

```bash
go test ./piano/ -run='^$' -bench='VoiceCostPerBlock|CouplingGraphDensity' -benchmem -count=5
```

## Reproducing the before/after comparison

The pre-refactor numbers were taken from a worktree at the parent commit of the
SoA change, running the same benchmark shape:

```bash
git worktree add /tmp/baseline <commit-before-soa>
# copy the benchmark in, then:
go test ./piano/ -run='^$' -bench=... -benchtime=1000x -count=10 > old.txt
benchstat old=old.txt new=new.txt
```
