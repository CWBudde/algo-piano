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

## Convolver cost

`BenchmarkSoundboardConvolverIRLength` / `BenchmarkBodyConvolverIRLength`, one
128-frame block per iteration at the production partition size of 128 — the
shape of a single audio callback. The realtime budget for that block at 48 kHz
is **2.67 ms**.

| IR length     | Room (stereo) | Body (mono) | Room, % of budget |
| ------------- | ------------- | ----------- | ----------------- |
| 128 (2.7 ms)  | 8.3 µs        | 3.7 µs      | 0.3%              |
| 1024 (21 ms)  | 50 µs         | 24 µs       | 1.9%              |
| 8192 (170 ms) | 0.32 ms       | 0.15 ms     | 12%               |
| 48000 (1 s)   | 2.30 ms       | 0.80 ms     | **86%**           |
| 192000 (4 s)  | 23.3 ms       | 13.8 ms     | **873%**          |

Cost grows linearly with IR length, as uniform partitioning implies. The
practical consequence: at the default `partSize = 128` a one-second stereo room
IR eats 86% of the entire block budget before the string bank has been charged
for anything, and a four-second room IR is 8.7x over budget on its own. Long room
IRs are not viable at this partition size.

`BenchmarkSoundboardConvolverPartitionSize` / `BenchmarkBodyConvolverPartitionSize`
render a fixed 4096 frames per iteration so every partition size does the same
acoustic work. Room IR is 1 s, body IR is 8192 taps. The normalized columns
divide by 32 to give the cost of one 128-frame block.

| Partition | Room / 4096 fr | Room / 128 fr | Body / 4096 fr | Body / 128 fr |
| --------- | -------------- | ------------- | -------------- | ------------- |
| 64        | 203 ms         | 6.34 ms       | 10.4 ms        | 0.32 ms       |
| 128       | 71.5 ms        | 2.24 ms       | 8.2 ms         | 0.26 ms       |
| 256       | 21.9 ms        | 0.69 ms       | 4.7 ms         | 0.15 ms       |
| 512       | 26.5 ms        | 0.83 ms       | 2.0 ms         | 0.061 ms      |
| 1024      | 9.9 ms         | 0.31 ms       | 0.77 ms        | 0.024 ms      |

The room figure at partition 128 (2.24 ms) agrees with the 1 s row of the
IR-length table (2.30 ms), which is a useful cross-check that the two benchmarks
measure the same thing.

Partition size, not IR length, is the lever: 128 -> 256 cuts the room convolver
by about 3x, from 86% of the block budget to 26%, and 1024 gets it to 12%. The
room path's 512 entry sits above its 256 entry, which is the FFT size crossing a
radix boundary plus residual measurement noise rather than a real inversion. The
mono body path improves monotonically all the way to 1024. What partitioning
costs is latency — one partition of it.

**Caveat on absolute values.** The measurement window overlapped unrelated work
on this machine; load average ranged from 12 to 25 and repeat runs of the same
case spread by up to 2x. The table above comes from the least-loaded run. The
scaling shape (linear in IR length, sharply falling in partition size) reproduced
across five runs and the two benchmarks cross-check each other, but re-measure on
a quiet machine before adopting the absolute milliseconds as a budget:

```bash
go test ./piano/ -run='^$' -bench='Convolver' -benchmem -benchtime=50x -count=5
```

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
