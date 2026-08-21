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
| Date           | 2026-08-16                                                  |
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
groups undamped targets, physical coupling), the same 12-run interleaved
comparison:

| Config                    | Before  | After   | Change                     |
| ------------------------- | ------- | ------- | -------------------------- |
| `perNoteFilter` (default) | 6.12 ms | 1.78 ms | **−70.9%** (p=0.000, n=10) |
| `flatDrive`               | 5.77 ms | 1.48 ms | **−74.4%** (p=0.000, n=10) |

With resonance **disabled** the change is not measurable above this machine's
noise: `BenchmarkModalKernels` trends −20% to −29% but only reaches p≈0.05,
and `BenchmarkModalPolyphonyScaling` and `BenchmarkStringBankCouplingModes` all
report `~`. That is expected — coupling injection runs once per edge rather than
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
