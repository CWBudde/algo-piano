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
not settle the profile decision. The retained-heap curve says a low partial
count is cheap, but says nothing about whether it still sounds like a piano —
that question is taken up in "Modal partials: quality vs CPU" below, where the
CPU saving turns out to be small and the quality cost real but uncalibrated.

## Modal partials: quality vs CPU

_Measured 2026-08-22, Go 1.26.5, same machine as the `## Machine` section above
(12th Gen Intel Core i7-1255U, 12 threads, Linux 6.8.0 amd64). CPU figures are
the median of **seven independent `go test` invocations**, not `-count=7` inside
one invocation — with `-count=N` Go runs all N repetitions of a case
back to back, so a case measured while background load happens to be high is
biased as a whole, and the sweep then reads as a curve when it is really a load
trace. Separate invocations interleave the cases over time instead. Load average
during these runs was 1.3 to 4.2. Quality figures are deterministic renders and
carry no run-to-run spread at all._

This section addresses the open input to `PIANO-406`: **how far can
`modal_partials` drop before quality suffers, and what does dropping it buy?**
It answers the second half outright and the first half only as far as the
repository's instruments reach — see "What the two curves say together".
The two halves are `TestModalPartialsQualityCurve`
(`piano/modal_partials_test.go`) and `BenchmarkModalPartialsSweep`
(`piano/modal_bench_test.go`), which sweep the same partial counts. The
retained-heap column is `BenchmarkStringBankRetainedHeap`, re-run for this
section and reproducing its recorded figures exactly (331,424 B DWG;
176,096 / 224,752 / 288,192 / 385,408 / 537,424 B modal at 1/4/8/16/32
partials).

### What "quality" is measured against, and what it cannot be

Each partial count is scored twice by `analysis.Compare` under the default
`legacy-v1` profile.

**Against DWG at the same note**, the cross-core reference PLAN.md 12.4 already
uses. This turned out to answer almost nothing, and that is itself the finding:
the overall score moves by **0.004 across a 24x change in partial count** and is
flat to four decimals above 4 partials. Two reasons, both already documented
elsewhere in the tree. The score saturates spectrally — at these distances the
spectral component is far past `analysis.NormSpectral = 30.0`, `clamp01` pins it
at 1.0, and it contributes a constant with no gradient (the "SPECTRAL CAVEAT" in
`assets/thresholds/c4.json`). And the DWG reference is itself a sparse impulse
train (see `TestDWGModalDistanceIsBounded`), so the cross-core distance is
dominated by that defect and barely notices the modal spectrum changing
underneath it. **The overall score does not show the knee and cannot be used to
locate one.**

**Against the modal core at 32 partials**, the top of the valid `[1,32]` range.
This reference was added because the DWG one cannot answer the question that was
asked. It asks it directly: holding everything else fixed, how much does the
render change when partials are taken away? It is _not_ an absolute quality
measure — nothing here establishes that the 32-partial render is correct — but
it is a well-conditioned difference signal, and it is the column the decision
below is read from.

One measurement ceiling has to be read with the numbers: `analysis` tracks at
most 16 harmonics (`analysis/features.go`, `maxPartials = 16`), so
`partial_level_rmse_db` is blind above the 16th partial. Every reading at 16 and
24 partials sits on that ceiling and must **not** be read as "no difference".
At C6 the 24-partial row is 0.00 dB on every metric for a second reason: 1046 Hz
runs out of Nyquist headroom before 32 partials, so 24 and 32 admit the same
mode count and the two renders are bit-identical.

### Quality, single held note, 750 blocks (2.0 s) at 48 kHz, sustain held, resonance and coupling off

`partial_level` / `spectral` / `spectral_high` are `partial_level_rmse_db`,
`spectral_rmse_db` and the 2 kHz-and-up band `spectral_high_rmse_db`, in dB.

Against the internal reference (modal at 32 partials) — the column with a real
gradient:

| Partials | C2 (36) partial_level | C2 spectral | C2 spec_high | C4 (60) partial_level | C4 spectral | C4 spec_high | C6 (84) partial_level | C6 spectral | C6 spec_high |
| -------- | --------------------- | ----------- | ------------ | --------------------- | ----------- | ------------ | --------------------- | ----------- | ------------ |
| 1        | 43.69                 | 6.02        | 5.61         | 46.16                 | 5.73        | 5.13         | 35.56                 | 4.72        | 4.52         |
| 2        | 40.51                 | 5.43        | 5.24         | 40.28                 | 5.07        | 4.84         | 29.83                 | 3.72        | 3.65         |
| 3        | 40.25                 | 5.21        | 5.15         | 37.90                 | 4.77        | 4.70         | 23.61                 | 3.23        | 3.22         |
| 4        | 40.52                 | 4.62        | 4.56         | 33.01                 | 4.63        | 4.69         | 16.54                 | 2.90        | 2.91         |
| 5        | 38.98                 | 3.96        | 3.85         | 29.66                 | 4.54        | 4.65         | 12.54                 | 2.85        | 2.89         |
| 6        | 37.35                 | 3.75        | 3.63         | 26.76                 | 4.55        | 4.70         | 10.55                 | 2.77        | 2.82         |
| **8**    | **29.45**             | **2.80**    | **2.63**     | **19.24**             | **4.45**    | **4.67**     | **2.55**              | **2.55**    | **2.64**     |
| 12       | 18.39                 | 2.15        | 1.99         | 9.26                  | 4.21        | 4.45         | 1.68                  | 2.43        | 2.57         |
| 16       | 0.00 ᶜ                | 0.77        | 0.25         | 0.08 ᶜ                | 4.25        | 4.53         | 1.08 ᶜ                | 2.28        | 2.43         |
| 24       | 0.00 ᶜ                | 0.46        | 0.25         | 0.15 ᶜ                | 4.09        | 4.37         | 0.00 ᴺ                | 0.00 ᴺ      | 0.00 ᴺ       |

ᶜ on the 16-harmonic analysis ceiling — blind, not equal. ᴺ Nyquist-limited to
the same mode count as the reference — genuinely bit-identical.

Against DWG, for completeness and to show why it cannot be used (score, then the
same three sub-metrics at C4):

| Partials | C2 score | C4 score | C6 score | C4 partial_level | C4 spectral | C4 spec_high |
| -------- | -------- | -------- | -------- | ---------------- | ----------- | ------------ |
| 1        | 0.860297 | 0.841694 | 0.846816 | 105.96           | 101.22      | 95.67        |
| 2        | 0.857665 | 0.839031 | 0.845962 | 101.98           | 101.03      | 95.49        |
| 4        | 0.856234 | 0.838576 | 0.845842 | 97.45            | 100.89      | 95.35        |
| **8**    | 0.856240 | 0.838576 | 0.845837 | 86.13            | 100.87      | 95.34        |
| 16       | 0.856246 | 0.838578 | 0.845838 | 70.91            | 100.90      | 95.38        |
| 24       | 0.856245 | 0.838578 | 0.845836 | 70.86            | 100.86      | 95.34        |

The score is non-increasing in partial count to four decimals; past saturation it
wobbles upward by at most 3.4e-05, which is what the tolerance in
`TestModalPartialsQualityCurve` allows for. Note the direction of
`spectral_rmse_db` against DWG at C2: it gets **worse** as partials are added
(103.35 dB at 1 partial, 106.53 dB at 8). A richer modal spectrum diverges
further from a sparse impulse train. That is a statement about the DWG
reference, not about modal quality, and it is the clearest single reason the
cross-core score is the wrong instrument here.

### CPU, one 128-frame block at 48 kHz, 64 held strings (MIDI 36-69), sustain held

`BenchmarkModalPartialsSweep`. Both coupling arms are measured because they
answer different questions. **Coupling off** is the regime the "Voice cost per
block and polyphony sweep" section above measures, so the 8-partial row is
directly comparable with its 64-voice row. **Coupling on** enrols the whole
compass through sympathetic injection; the active set settles at 88 notes / 196
strings, measured and identical for both cores and every partial count, so the
columns still compare like with like. In that arm `ns/voice-block` divides by
the 64 _held_ strings while 196 are sounding, so it is a cost per held key; the
`ns/active-string` column below divides by 196 instead.

| Partials | Off: sec/op | Off: % budget | Off: ns/active-string | On: sec/op | On: % budget | On: ns/active-string | Retained heap |
| -------- | ----------- | ------------- | --------------------- | ---------- | ------------ | -------------------- | ------------- |
| DWG      | 155 µs      | 5.8%          | 789                   | 522 µs     | 19.6%        | 2662                 | 324 KiB       |
| 1        | 60.3 µs     | 2.3%          | 308                   | 199 µs     | 7.5%         | 1014                 | 172 KiB       |
| 2        | 64.6 µs     | 2.4%          | 329                   | 213 µs     | 8.0%         | 1089                 | —             |
| 3        | 76.5 µs     | 2.9%          | 390                   | 243 µs     | 9.1%         | 1241                 | —             |
| 4        | 79.2 µs     | 3.0%          | 404                   | 254 µs     | 9.5%         | 1296                 | 219 KiB       |
| 5        | 79.3 µs     | 3.0%          | 404                   | 273 µs     | 10.2%        | 1393                 | —             |
| 6        | 91.8 µs     | 3.4%          | 469                   | 302 µs     | 11.3%        | 1541                 | —             |
| **8**    | **97.3 µs** | **3.6%**      | **497**               | **330 µs** | **12.4%**    | **1684**             | **281 KiB**   |
| 12       | 129 µs      | 4.8%          | 659                   | 401 µs     | 15.0%        | 2044                 | —             |
| 16       | 140 µs      | 5.2%          | 714                   | 447 µs     | 16.7%        | 2278                 | 376 KiB       |
| 24       | 187 µs      | 7.0%          | 956                   | 546 µs     | 20.5%        | 2785                 | —             |

Retained-heap figures are per full 88-key bank from the "Retained heap" section;
they are a property of the constructed bank, not of this 64-key load, and are
repeated here only so the three axes sit in one table. Blank cells are partial
counts that section does not sweep.

**The CPU curve is not flat in `modal_partials`.** It is close to affine in the
partial count with a large fixed base: uncoupled, about 28 ns per active string
per block per partial on a ~280 ns floor, so roughly **55% of the cost at the
default 8 partials is fixed overhead** that no partial reduction can touch.
Concretely, from 8 partials:

| Drop to | CPU saved (off) | CPU saved (on) | `partial_level` cost at C4 |
| ------- | --------------- | -------------- | -------------------------- |
| 6       | −6%             | −8%            | +7.5 dB                    |
| 4       | −19%            | −23%           | +13.8 dB                   |
| 2       | −34%            | −35%           | +21.0 dB                   |
| 1       | −38%            | −40%           | +26.9 dB                   |

### What the two curves say together

- **There is no knee below the default.** The quality curve is smooth and
  monotone all the way down; nothing in it marks a partial count below 8 as
  "free". Every partial removed costs measurable spectral content, and the cost
  is steepest exactly where the CPU saving is largest.
- **The CPU half of the low-CPU question is settled, and the answer is no.**
  Because roughly 55% of the cost at 8 partials is fixed overhead, dropping 8 to
  4 buys only 19% (uncoupled) or 23% (coupled) of the string-bank block cost,
  and even 8 to 1 buys 38-40%. A "low CPU" profile that needs a substantial
  reduction cannot get one from this knob, whatever the quality cost turns out
  to be. **`modal_partials` is not a useful low-CPU knob.**
- **The quality half is measured but not calibrated.** The sweep establishes
  that lower-partial renders _differ_ from the 32-partial internal reference,
  and by how much: 13.8 dB of `partial_level_rmse_db` at C4 for 8 to 4, 26.9 dB
  for 8 to 1. It does **not** establish that those differences are audible or
  unacceptable. There is no listening test behind them, and
  `partial_level_rmse_db` has no calibrated acceptance threshold anywhere in
  this repo — `assets/thresholds/c4.json` lists it as `null`, explicitly
  uncalibrated, and that file's numbers are regression fences rather than
  quality targets in any case. Closing this half properly needs one of two
  inputs that do not exist yet: a calibrated `partial_level_rmse_db` acceptance
  threshold, or a listening test.
- **The largest quality cliffs are at the bottom.** 1 to 2 partials is worth 6 dB
  at C4 and 2 partials is not a piano note by any reading; the sweep includes 1
  and 2 to bound the axis, not because they are candidates.
- **Above 8 partials, part of the difference is measurable and part is not.**
  Harmonics 9-16 are inside the analyzer's range: the table above shows
  `partial_level_rmse_db` moving substantially between 8, 12 and 16 partials in
  every register, and the full-spectrum metrics (`spectral_rmse_db`,
  `spectral_high_rmse_db`) stay sensitive above harmonic 16 as well. What is
  hidden is a difference confined **entirely** above the 16th harmonic:
  `analysis` tracks at most 16 harmonics, so the ᶜ rows at 16 and 24 partials
  sit on that ceiling and their near-zero partial-level readings are blind, not
  equal. Whether the shipped default of 8 is too _low_ is therefore a question
  these tools can partly address — through the full-spectrum metrics and through
  the partial-level metric up to harmonic 16 — but cannot settle on the
  partial-level metric alone, and cannot address against DWG at all, since that
  score saturates.

### An unexplained disagreement with the recorded polyphony numbers

The seven invocations behind the table above also re-ran
`BenchmarkStringBankVoiceCostPerBlock` unmodified, in the same processes, so the
8-partial coupling-off row has a direct anchor. It agrees with itself
exactly — 97.3 µs in the sweep against 97.0 µs in the polyphony benchmark at 64
voices — and **disagrees with the figures recorded in "Voice cost per block and
polyphony sweep" above**, in magnitude and in sign:

| Voices | DWG recorded 2026-08-21 | DWG re-measured | Modal recorded 2026-08-21 | Modal re-measured |
| ------ | ----------------------- | --------------- | ------------------------- | ----------------- |
| 16     | 63.7 µs                 | 37.9 µs         | 72.0 µs                   | 23.2 µs           |
| 32     | 121 µs                  | 78.8 µs         | 133 µs                    | 48.5 µs           |
| 64     | 265 µs                  | 153 µs          | 330 µs                    | 97.0 µs           |
| 130    | 546 µs                  | 302 µs          | 614 µs                    | 194 µs            |

The recorded run has modal 6% to 25% _more_ expensive than DWG. The re-measured
run has it 36% to 39% _less_ expensive, at every polyphony point, with the
repository's own unmodified benchmark.

What can be said about the difference is that the recorded run was taken "at load
average 8 to 13" and that this benchmark cannot resolve a 25% effect under that
load. Re-running `BenchmarkStringBankVoiceCostPerBlock` at `-count=3` with twelve
busy-loop processes pinned against it (load average 12) inflated every case by
3x to 20x and produced readings spanning **8x within a single case**: the
64-voice modal case read 264 µs, 510 µs and 2109 µs on its three consecutive
repetitions, against 97 µs quiet. A ±25% conclusion drawn from that regime is
not supported by it.

What **cannot** be said is why the sign flips rather than merely blurring. Load
alone should inflate both cores similarly. This section does not explain the
discrepancy and does not edit the earlier table; it records that the two
disagree, that the newer one was taken on a quiet machine and self-anchored, and
that resolving it is the remaining step before the 12.4 shipping rule can be
adopted. See `PIANO-406` in PLAN.md.

Re-measure with:

```bash
go test ./piano/ -run='TestModalPartials' -v
go test ./piano/ -run='^$' -bench='BenchmarkModalPartialsSweep|BenchmarkStringBankRetainedHeap' -benchmem
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
