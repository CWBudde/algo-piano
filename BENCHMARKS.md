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

|                |                                                                        |
| -------------- | ---------------------------------------------------------------------- |
| CPU            | 12th Gen Intel Core i7-1255U (12 threads, AVX2, no AVX-512)            |
| OS             | Linux 6.8.0 amd64                                                      |
| Go             | 1.26.5                                                                 |
| Date           | 2026-08-16; convolver and idle 2026-08-21; convolver sweeps 2026-08-22 |
| `algo-vecmath` | v0.1.3                                                                 |

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

### Partition size became a lever, and callback alignment became the bigger one

Measured 2026-08-22 on the machine above (Go 1.26.5), median of seven
`-count=7` runs at load average 1 to 3, after the convolver block-size fix.

`BenchmarkSoundboardConvolverPartitionSize` / `BenchmarkBodyConvolverPartitionSize`
render a fixed 4096 frames per iteration so every partition size does the same
acoustic work. Room IR is 1 s, body IR is 8192 taps. Those 4096 frames are
delivered as 32 separate 128-frame `Process` calls, because that is the shape the
engine actually runs in; the normalized columns divide by 32 to give the cost of
one real 128-frame callback, against a 48 kHz budget of **2.67 ms**.

| Partition | Room / 4096 fr | Room / callback | Room, % of budget | Body / 4096 fr | Body / callback |
| --------- | -------------- | --------------- | ----------------- | -------------- | --------------- |
| 64        | 58.4 ms        | 1.82 ms         | 68%               | 5.73 ms        | 0.179 ms        |
| 128       | 29.0 ms        | 0.91 ms         | 34%               | 2.91 ms        | 0.091 ms        |
| 256       | 38.2 ms        | 1.19 ms         | 45%               | 2.51 ms        | 0.079 ms        |
| 512       | 19.0 ms        | 0.59 ms         | 22%               | 2.06 ms        | 0.064 ms        |
| 1024      | 12.9 ms        | 0.40 ms         | 15%               | 2.72 ms        | 0.085 ms        |

Cross-check against the IR-length table: room at partition 128 is 0.91 ms here
against 1.06 ms there, and body at partition 128 is 91 µs against that table's
8192 row at 95 µs.

**Raising the partition size above 128 now buys something for a long room IR,
which it did not before.** 1024 costs 0.40 ms per callback against 0.91 ms at
128 — 2.25x less, 15% of the block budget instead of 34%. The curve is not
monotonic: 256 is _worse_ than 128, at 1.19 ms.

The shape follows from how a partition larger than the callback is now served.
Each committed partition costs two FFT round-trips, the main overlap-add stage
plus the `h[partSize:]` tail stage the zero-latency split needs, and it covers
`partSize` frames of audio instead of 128. FFT work per unit audio therefore
scales as `2 / partSize` against `1 / 128`: break-even at 256, a win beyond it.
Measured, 256 lands slightly worse than break-even — the head evaluation adds
`partSize` multiply-accumulates per off-grid sample, and two 64k-point FFT
working sets per channel press harder on cache than one — and 512 and 1024 pull
clear. The body path shows the other end of the same trade: at only 8192 taps the
FFT is cheap, so the head term dominates sooner and the curve turns back upward
at 1024.

`partSize = 64` remains the one clear loser, at 2.0x the 128 cost, because a
128-frame callback then contains two inner partitions and exactly twice the FFT
round-trips: 58.4 / 29.0 = 2.01x.

None of this is free real estate. A larger partition means a larger head
evaluation on every off-grid sample and a second FFT stage allocated per channel;
the elevated `B/op` at partitions 256 and up is that stage, allocated once on the
first off-grid call and amortized over the benchmark's iterations, not a
per-callback allocation. Whether 512 is worth taking over 128 is a decision about
a specific IR length, not a general one.

#### Callback size at the production partition of 128

`BenchmarkSoundboardConvolverCallbackSize` / `BenchmarkBodyConvolverCallbackSize`
hold the partition at 128 and vary the size of the caller's `Process` calls over
the same 4096 frames. Percentages are of realtime: 4096 frames at 48 kHz is
85.3 ms of audio.

| Call size | Room / 4096 fr | Room, % of realtime | Body / 4096 fr | Body, % of realtime |
| --------- | -------------- | ------------------- | -------------- | ------------------- |
| 64        | 89.1 ms        | **104%**            | 4.58 ms        | 5.4%                |
| 100       | 88.6 ms        | **104%**            | 4.60 ms        | 5.4%                |
| 128       | 29.2 ms        | 34%                 | 2.85 ms        | 3.3%                |
| 256       | 29.1 ms        | 34%                 | 2.86 ms        | 3.4%                |
| 512       | 28.4 ms        | 33%                 | 2.89 ms        | 3.4%                |

Whole multiples of the partition are free — 128, 256 and 512 are identical
inside noise, because they take the aligned path straight through the
overlap-add stage. Anything else costs 3.05x on the room path and 1.61x on the
body path, for the same reason a partition above 128 costs two FFTs: an off-grid
callback commits its partition through the main stage _and_ the tail stage, and
pays `partSize` multiply-accumulates per sample for the head on top.

The room row is the one to act on. With a one-second IR, a host that hands the
engine 64-frame buffers puts the room convolver alone at 104% of realtime, where
128-frame buffers put it at 34%. **Align the host block size to a multiple of the
partition size.** If a host insists on 64, raising `partSize` is not the fix
either — 64-frame callbacks at `partSize = 128` and 128-frame callbacks at
`partSize = 64` are two ways of paying the same doubled-FFT bill. Shortening the
room IR is.

#### Two corrections to earlier revisions of this section

An earlier revision claimed 128 -> 256 cut the room convolver by about 3x, from
86% of the block budget to 26%, and that 1024 got it to 12%. That was an artefact
of the benchmark, which used to hand the convolver all 4096 frames in a single
`Process` call. `Process` consumes its input in `partSize` chunks, so one big call
amortizes each partition over `partSize` frames of output, and dividing by 32 then
produced a number no realtime caller can obtain. Driving it callback-by-callback
is what the tables above do.

The revision that replaced it — the one headed "Partition size is not a lever for
realtime cost" — was right about the numbers it measured and wrong about why, and
two of its statements are now obsolete:

- It said that above 128 `Process` "pads each short callback up to `partSize`,
  advances one full overlap-add partition, and returns only the first 128 samples
  — so the rest of that partition's output is discarded and the stream is wrong,
  not merely expensive." The stream being wrong was a real defect, not a property
  of partitioned convolution, and it is fixed: the padding is gone and the output
  is now correct at every partition size and every call size (see
  `piano/convolver_stream.go` and `TestConvolverBlockSizeContinuity`). Every
  partition-sweep row above 128 in that revision's table was measuring incorrect
  output and is superseded by the table above.
- It said that making partition size a real lever "would take cross-callback
  buffering inside the convolver: accumulate callbacks until a full partition is
  available, then drain that partition's output over the following callbacks,
  trading one partition of latency for the amortization. That does not exist
  today." The buffering now exists and the predicted latency trade turned out to
  be avoidable. Samples the caller asks for before their partition closes are
  computed by splitting the convolution into a direct `h[:partSize]` head over
  retained input history plus a second overlap-add stage carrying
  `h[partSize:]`, which can run a partition ahead because it reads only committed
  input. `Process(n)` still returns `n` samples at zero added latency, and the
  amortization shows up as the 2.25x at partition 1024 above.

What survives from that revision is the arithmetic for the aligned case — per
aligned `Process` call the convolver transforms one partition and multiplies it
against all `irLen / partSize` IR partitions, so that path is `O(irLen · log
partSize)` and near-constant in `partSize` — and the conclusion that IR length is
the dominant lever on room-convolver cost. A non-uniform partitioning scheme is
still the open structural improvement.

Re-measure with:

```bash
go test ./piano/ -run='^$' -bench='Convolver' -benchmem -count=7
```

Room `ir8k` was the noisiest case in the IR-length family, spreading 0.32 to
0.64 ms across runs. In the partition and callback families every case above held
within about 8% of its median across the seven runs.

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
resonance, which with the sustain pedal down drives all 88 groups of the
full-compass bank (MIDI 21-108) once per sample.

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

At block level, `BenchmarkModalResonanceInjection` (8 keys, sustain held, all 88
groups of the full-compass bank undamped targets, physical coupling), the same
interleaved comparison
over 10 runs:

| Config                    | Before  | After   | Change                     |
| ------------------------- | ------- | ------- | -------------------------- |
| `perNoteFilter` (default) | 6.12 ms | 1.78 ms | **−70.9%** (p=0.000, n=10) |
| `flatDrive`               | 5.77 ms | 1.48 ms | **−74.4%** (p=0.000, n=10) |

Re-measured 2026-08-22 (Go 1.26.5, same machine, `-benchtime 200x -count 10`)
after the modal resonance divergence fix (`resonanceForceScale` in
`piano/modal_group.go`). The figures above were taken while the modal bank in
this benchmark was in fact diverging, so its mode state ran far outside the
normal float32 range; the fix keeps it in range, and the cost is unchanged:
median 0.68 ms (`perNoteFilter`) and 0.62 ms (`flatDrive`) with the fix against
0.73 ms and 0.66 ms with the scale reverted on the same run — within this
machine's noise, and both still allocation-free. The absolute numbers differ
from the table above because of the shorter `-benchtime`; only the paired
comparison is meaningful.

Re-checked 2026-08-22 after the `noteResonator` unity-peak normalisation, which
`filterResonanceDrive` runs on this path in both cores: 0.77 ms
(`perNoteFilter`) and 0.61 ms (`flatDrive`) at `-benchtime 2s`, unchanged within
noise against the figures in the paragraph above. The normalisation only changes
`b0`, which `newNoteResonator` computes once at construction, so the per-sample
work is identical and nothing here needed re-baselining.

Re-measured 2026-08-22 after the sympathetic loop was **interleaved with
rendering** — it now runs inside `StringBank`'s per-sample loop rather than as a
whole-block deposit in `Piano.Process`, so the benchmark drives `sb.Process`
directly instead of feeding its output back through `InjectFromBridge`. Median of
five `-count=5` runs at default benchtime: **0.746 ms (`perNoteFilter`) and
0.653 ms (`flatDrive`)**, both still **0 B/op, 0 allocs/op**.

That is within noise of the 0.77 / 0.61 ms above, and it is the expected result
rather than a lucky one: the old code was **already** paying the full per-sample
cost. `InjectFromBridge` looped over the block's samples and ran `bandLimit`, the
three-filter `noteResonator` bank and `injectResonance` once per sample per
undamped target — it simply did so against string state that never advanced in
between. Interleaving moves that identical work into the render loop and adds one
predictable branch and one float32 store per sample. Nothing about the cost model
in this section changes; what changed is that the deposits now land on advancing
state.

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
`-count=5` runs at default benchtime. **This machine was under concurrent load
for this sweep**, so every row here sits roughly 2x above what the same
benchmark reports on a quiet machine. The whole section was re-measured in one
back-to-back run when the production default (`coupling_max_neighbors = 10`)
was added to the sweep, so the rows are comparable with each other but not with
the absolute figures in other sections._

`BenchmarkStringBankCouplingGraphDensity`, PLAN.md 9.6's "coupling graph
density/top-K scaling vs CPU". The struck keys are held fixed at eight
mid-register keys with the sustain pedal down and physical coupling on; the
only thing that moves is how dense the precomputed sparse graph is. The number
of _active_ notes is not fixed — that is the effect being measured. Each case
reports the graph it ran on next to the timing: `edges` (directed edges in the
whole 88-key graph), `active-edges` (edges leaving the active set) and `active`
(notes in the active set), all sampled after the measured loop.

Sweeping the `coupling_max_neighbors` top-K cap. 10 is the production default,
in both `NewDefaultParams` and `assets/presets/default.json`:

| maxNeighbors | edges | active | active-edges | sec/op | % of budget |
| ------------ | ----- | ------ | ------------ | ------ | ----------- |
| 1            | 88    | 8      | 8            | 48 µs  | 1.8%        |
| 2            | 176   | 50-62  | 100-124      | 308 µs | 12%         |
| 4            | 352   | 84     | 336          | 574 µs | 22%         |
| 8            | 704   | 88     | 704          | 746 µs | 28%         |
| 10 (default) | 880   | 88     | 880          | 680 µs | 26%         |
| 16           | 1408  | 88     | 1408         | 686 µs | 26%         |
| 32           | 2597  | 88     | 2597         | 719 µs | 27%         |
| 64           | 2792  | 88     | 2792         | 736 µs | 28%         |
| 87           | 2792  | 88     | 2792         | 843 µs | 32%         |

64 and 87 build the identical graph: the compile-time weight floor
`couplingPhysicalMinScore` already rejects everything past ~32 candidates per
source on average, so no source has 64 survivors and the cap stops binding
somewhere between 32 and 64. Their 736 µs against 843 µs is therefore a direct
readout of this machine's run-to-run noise on this benchmark, not a density
effect — as is the default (10) landing slightly _below_ 8. Treat differences
below about 30% here as noise.

Sweeping an edge-weight floor instead, at a fixed top-K of 32. The production
floor is a compile-time constant, so the benchmark prunes the built graph:
every edge carrying less than `minShare` of its source's total outgoing gain is
dropped, without renormalising what survives.

| minShare | edges | active | active-edges | sec/op | % of budget |
| -------- | ----- | ------ | ------------ | ------ | ----------- |
| 0.00     | 2597  | 88     | 2597         | 733 µs | 27%         |
| 0.02     | 897   | 88     | 897          | 603 µs | 23%         |
| 0.05     | 566   | 88     | 566          | 563 µs | 21%         |
| 0.10     | 228   | 73-80  | 187-207      | 371 µs | 14%         |
| 0.20     | 100   | 10     | 11           | 54 µs  | 2.0%        |

**Edge count is not the CPU lever.** Cutting the graph from 2597 edges to 566 —
a 4.6x reduction, and the `active-edges` column confirms the per-sample edge work
fell by the same factor — moves the block cost from 733 µs to 563 µs, a 23%
change against a noise band of about the same size, while the active set stays
saturated at 88. The same holds on the top-K side: 8 to 87 neighbours is a 4x
edge increase for roughly 13%, inside that band.

What actually costs is the active-voice count the graph _recruits_.
`InjectCouplingForce` enrols any target it drives into the active set, so a graph
dense enough to reach the whole keyboard turns 8 struck keys into 88 sounding
groups, and rendering those 88 groups dominates everything the coupling loop
itself does. The two rows where cost collapses are exactly the two rows where
recruitment fails: `maxNeighbors=1` (8 active, 48 µs) and `minShare=0.20`
(10 active, 54 µs). Between them, the transition is abrupt — `maxNeighbors=2`
already recruits 50 to 62 voices and costs 308 µs.

Two consequences for tuning:

- Lowering `coupling_max_neighbors` from its default of 10 to save CPU only
  works if it drops far enough to stop the cascade, and that point (1 to 2
  neighbours) is far below any setting that sounds like a piano. As a
  performance knob it is close to useless; as a voicing knob it is nearly free.
- Any future budget work on the coupled case should go at the cost of a
  sounding group, or at capping how many groups coupling may recruit, not at
  making the graph sparser.

The `active` column for `maxNeighbors=2` (50 to 62) and `minShare=0.10` (73 to 80) varies between runs because recruitment is still in progress when the
measured loop ends; it is sampled after the loop, so a longer run recruits more.
The saturated rows (88) and the collapsed rows are stable.

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

## Retained heap (memory footprint comparison)

_Measured 2026-08-22, Go 1.26.5, same machine as the `## Machine` section above
(12th Gen Intel Core i7-1255U, Linux 6.8.0 amd64) — this is the same host, so
that description is reused verbatim. The machine was under concurrent load
(load average 9 to 18) while this ran. That affects the `sec/op` column only:
the heap figures come from `runtime.ReadMemStats` after a forced GC and are
not timing-sensitive, and the `-benchtime=1x` and `-benchtime=20x` runs agreed
to within 112 bytes on every case._

`BenchmarkStringBankRetainedHeap`, PLAN.md 12.4's "memory footprint comparison".
This is the **retained live Go heap** of a **constructed** full-compass bank —
`NewStringBank` + `NewHammerExciter` over MIDI 21-108, physical coupling at 10
neighbours — not a per-block allocation count. Every other benchmark here
reports zero allocations per block, which says nothing about how many bytes a
constructed bank keeps alive for the lifetime of the instance, so that is
measured directly: `runtime.GC` + `ReadMemStats` before and after construction,
with the bank kept reachable across the second reading, median of five samples.

**These are not resident-set (RSS) numbers and must not be used as a plugin or
shipping memory budget.** `MemStats.HeapAlloc` counts only bytes in live Go heap
objects; it excludes heap-span slack and fragmentation, allocator and runtime
metadata, goroutine stacks, and all other non-heap resident memory, so the real
resident set of a plugin instance is strictly larger. What the figures do
support — and all PLAN.md 12.4 asks of them — is a stable, attributable
comparison of the two cores' data structures against each other, and of how the
modal core scales with `modal_partials`.

A **voice is one sounding string**, as in the polyphony sweep above. The 88-key
compass holds 196 strings.

| Core               | B/bank  | KiB/bank | B/voice | vs DWG |
| ------------------ | ------- | -------- | ------- | ------ |
| DWG                | 331,536 | 324      | 1,692   | —      |
| Modal, 1 partial   | 176,096 | 172      | 898     | −47%   |
| Modal, 4 partials  | 224,752 | 219      | 1,147   | −32%   |
| Modal, 8 (default) | 288,192 | 281      | 1,470   | −13%   |
| Modal, 16 partials | 385,408 | 376      | 1,966   | +16%   |
| Modal, 32 partials | 537,424 | 525      | 2,742   | +62%   |

Construction cost, incidental but recorded because the benchmark reports it: 72
to 90 ms for one bank pair, with 1266 allocations (DWG) or 1055 (modal). That is
a plugin-instantiation cost, not an audio-thread cost.

What the numbers say:

- **The data structures are small.** A full 88-key bank retains a third of a
  megabyte of live heap at the defaults, either core. Even allowing generously
  for the non-heap memory this metric cannot see, memory is not a constraint on
  this design at any point in the sweep, and it should not be an argument in the
  core choice.
- **Modal is cheaper than DWG at the default 8 partials, by 13%** — 281 KiB
  against 324 KiB. This is the first metric on which modal actually wins;
  the CPU comparison above found the two within noise.
- **The modal retained heap is linear in `modal_partials`**, at about 11.7 kB per
  partial per bank (59 B per string per partial), on a fixed ~176 kB base. The
  break-even against DWG lands at roughly **13 partials**: above that, modal is
  the larger core.
- The DWG figure has no equivalent knob — its per-string cost is a delay line
  sized by pitch, averaging 1.7 kB across the compass.

This closes the last missing input to `PIANO-406` on the memory side. It does
not settle the profile decision, which remains blocked on how far
`modal_partials` can drop before quality suffers: the retained-heap curve says a
low partial count is cheap, but says nothing about whether it still sounds like
a piano.

## Reproducing the before/after comparison

The pre-refactor numbers were taken from a worktree at the parent commit of the
SoA change, running the same benchmark shape:

```bash
git worktree add /tmp/baseline <commit-before-soa>
# copy the benchmark in, then:
go test ./piano/ -run='^$' -bench=... -benchtime=1000x -count=10 > old.txt
benchstat old=old.txt new=new.txt
```
