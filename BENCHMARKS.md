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

## Reproducing the before/after comparison

The pre-refactor numbers were taken from a worktree at the parent commit of the
SoA change, running the same benchmark shape:

```bash
git worktree add /tmp/baseline <commit-before-soa>
# copy the benchmark in, then:
go test ./piano/ -run='^$' -bench=... -benchtime=1000x -count=10 > old.txt
benchstat old=old.txt new=new.txt
```
