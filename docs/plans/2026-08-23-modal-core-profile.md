# Modal core: SIMD integration and the shipping-profile decision

**What this is.** The evidence record behind PLAN.md 12.4's "Define shipping
rule" and 12.5's `PIANO-401`…`PIANO-406`, moved out of PLAN.md on 2026-08-23 so
that the one decision still open there is legible. The decision itself — which
core a "low CPU" and a "high accuracy" profile select — is **not made**, and the
open task lives in **PLAN.md Phase 18**.

**Where the decision stands.** It is no longer blocked on "modal might be
slower". On every measurement taken on a quiet machine modal is the cheaper
core, by 36–39% uncoupled and 37% with coupling on at 8 partials, and it retains
13% less heap. It is blocked on the fact that this **contradicts** the figure
recorded a day earlier on a loaded machine, and the contradiction is unexplained:
load should blur a 25% effect, not reverse its sign. Reversing a shipping rule on
a number whose disagreement with the previous number is not understood is not
better than leaving the rule unadopted.

One thing about the rule's _shape_ is settled, on the CPU axis alone: a "low CPU"
profile must **not** be implemented by lowering `modal_partials`. The knob has a
fixed-cost floor — roughly 28 ns per active string per block per partial on a
~280 ns floor, so ~55% of the cost at 8 partials is unreachable — and 8→4 buys
only 19% (uncoupled) or 23% (coupled) of the string-bank block cost. If the rule
is adopted, it is adopted as "low CPU ⇒ modal core at the default 8 partials",
with the partial count left alone.

## Contents

- [Benchmarks behind the decision](#benchmarks-behind-the-decision)
- [Upstream reality check](#upstream-reality-check)
- [`PIANO-401`…`PIANO-405` — what was built](#piano-401piano-405--what-was-built)
- [`PIANO-406` — the open decision and its evidence](#piano-406--the-open-decision-and-its-evidence)
- [Lessons worth keeping](#lessons-worth-keeping)

## Benchmarks behind the decision

**DWG vs modal CPU at fixed block size/sample rate.**
`BenchmarkStringBankStringModels` in `piano/modal_bench_test.go` runs both cores
over the shared benchmark cases at 48 kHz and a 128-frame block with coupling on.
The DWG-vs-modal figures it produces are tabulated in BENCHMARKS.md "Polyphony
scaling" and "Voice cost per block and polyphony sweep", which measure the same
pair at the same block size and rate; the coupled variant itself has no separate
table.

**Polyphony scaling comparison.** Covered twice: `BenchmarkModalPolyphonyScaling`
in `piano/modal_bench_test.go`, tabulated in BENCHMARKS.md "Polyphony scaling";
and `BenchmarkStringBankVoiceCostPerBlock` in `piano/polyphony_bench_test.go`,
tabulated in BENCHMARKS.md "Voice cost per block and polyphony sweep". Both
cores, 1–130 voices — they are within noise of each other.

**Memory footprint comparison.** `BenchmarkStringBankRetainedHeap` in
`piano/memory_bench_test.go` measures the retained **live Go heap** of a
constructed 88-key bank via `runtime.ReadMemStats`. **A full bank retains 324 KiB
(DWG) against 281 KiB (modal at the default 8 partials) — modal is 13% smaller**,
and the modal figure is linear in `modal_partials` at ~11.7 kB per partial,
crossing DWG at ~13 partials. This is `HeapAlloc` after a forced GC, not RSS: it
excludes heap-span slack and fragmentation, allocator metadata, stacks and other
non-heap memory, so it compares the two cores' data structures and does not serve
as a resident or plugin memory budget. Recorded in BENCHMARKS.md "Retained heap".

Re-run on 2026-08-22 for the partials sweep and reproduced exactly. This closes
the missing memory input to the profile decision: the gap between the cores is
small on either reading, so **memory does not decide the profile**.

## Upstream reality check

Recorded 2026-08-16. The original gate assumed `algo-dsp` would ship a modal
oscillator layer. It has not, and the `DSP-201..DSP-207` ticket IDs never existed
upstream — the real item is `algo-dsp` **Phase 41 — SIMD Modal Oscillator Bank**,
still _Planned_, with no `dsp/osc` or `dsp/modal` package in v0.7.0. `algo-piano`
therefore calls `algo-vecmath` directly.

`algo-vecmath` v0.1.3 state:

- `VEC-301..VEC-305` — done and verified (`RotateDecayComplexF32`,
  `RotateDecayAccumulateF32`, generic + AVX2 + SSE2 backends).
- `VEC-306` (arm64 NEON) — **marked done upstream but not implemented.** arm64
  falls back to the generic scalar kernel, so no SIMD gain applies there.
- `VEC-307` — partial (no `RotateDecay` baseline table).
- `VEC-308` (denormal/long-tail tests) — open.

Revisit if `algo-dsp` Phase 41 ships, or if `VEC-306` lands and arm64 becomes a
target.

## `PIANO-401`…`PIANO-405` — what was built

- **`PIANO-401`** — Upstream readiness tracked and versions pinned:
  `algo-vecmath` audited (301–305 usable, 306 missing, 307 partial, 308 open),
  `algo-dsp` Phase 41 confirmed not started, `go.mod` updated to `algo-approx`
  v0.2.0, `algo-dsp` v0.7.0, `algo-fft` v0.8.0, `algo-pde` v0.2.2 and
  `algo-vecmath` v0.1.3 (now a direct dependency).
- **`PIANO-402`** — Adapter layer: `ModalStringGroup` mode state converted to
  flat SoA (`piano/modal_group.go`), existing modal knobs preserved, scalar
  fallback kept behind `modalKernelMode` / `modalArenaEnabled`.
- **`PIANO-403`** — SIMD kernels replace the per-mode scalar update loop,
  batching across **notes** via `piano/modal_arena.go` (one kernel call per
  sample over all active groups), with no extra per-block allocations and
  unchanged coupling/resonance semantics.
- **`PIANO-404`** — Parity gate: bit-exact across scalar/accum/rotate/arena
  (`piano/modal_parity_test.go`), sustain and damped/undamped transitions
  covered, long renders NaN/Inf free with the denormal spin fixed.
- **`PIANO-405`** — Performance gate: modal CPU **−26.9%** (8 notes) and
  **−21.4%** (86 notes) vs the pre-refactor baseline, DWG vs modal benchmarked
  (caveat under `PIANO-406`), results recorded in `BENCHMARKS.md` with machine,
  Go version and date.
- **Excitation shape cache.** `injectAtPosition` called `math.Sin` per mode per
  excitation call (`piano/modal_group.go`). The shape vector is now cached per
  group — one precomputed slot for each of the two fixed strike positions
  (resonance 0.82, coupling 0.9) plus a one-entry cache for the hammer position —
  so the steady-state excitation path evaluates no transcendental at all. Output
  stays bit-exact; see BENCHMARKS.md "Excitation shape cache".

## `PIANO-406` — the open decision and its evidence

### The benchmark contradiction

The recorded polyphony sweep has the modal core _not_ cheaper than DWG at the
default 8 partials — 6% to 25% more expensive, which is why the "low CPU profile
defaults to modal" rule in 12.4 could not be adopted. Re-measuring that same
unmodified benchmark on 2026-08-22 on a quiet machine (load average 1.3–4.2,
median of seven independent invocations) **reverses it**: modal is 36% to 39%
cheaper than DWG at 16, 32, 64 and 130 voices.

The recorded run was taken at load average 8 to 13, and under that much load the
benchmark demonstrably cannot resolve a 25% effect — twelve busy loops pinned
against it produce an 8x spread inside a single case. So the recorded conclusion
is not supported by the conditions it was taken in. What is _not_ explained is
why the sign **flips** rather than merely blurring; load alone should inflate
both cores similarly. Until that is understood, the box stays open. See
BENCHMARKS.md "Modal partials: quality vs CPU", last subsection.

### The `modal_partials` quality-vs-CPU curve

Measured 2026-08-22, `TestModalPartialsQualityCurve` in
`piano/modal_partials_test.go` and `BenchmarkModalPartialsSweep` in
`piano/modal_bench_test.go`, both sweeping 1/2/3/4/5/6/8/12/16/24 partials;
tabulated in BENCHMARKS.md "Modal partials: quality vs CPU".

**The CPU half of this box is answered; the quality half is not, and the box was
briefly marked closed on the strength of an overclaim that PR #34 review caught.**

What _is_ established: the CPU curve is not flat in `modal_partials` — it is
roughly affine, about 28 ns per active string per block per partial on a ~280 ns
floor, so ~55% of the cost at 8 partials is fixed and no reduction can reach it.
Dropping 8 to 4 buys only 19% (uncoupled) or 23% (coupled) of the string-bank
block cost and 8 to 1 buys 38–40%, so the knob cannot deliver a substantial CPU
saving whatever quality one is willing to pay.

What is **not** established: the quality curve is smooth and monotone all the way
down, with no knee below the shipped default of 8 — 8 to 4 costs 13.8 dB of
`partial_level_rmse_db` at C4 against the same core at 32 partials and 8 to 1
costs 26.9 dB — but that only shows the renders _differ_ from the 32-partial
reference. Nothing here shows the difference is audible or unacceptable: there is
no listening test, and `partial_level_rmse_db` has no calibrated acceptance
threshold in this repo (`assets/thresholds/c4.json` lists it as `null`, and its
numbers are regression fences rather than quality targets).

A previous revision of this box compared the 13.8 dB against the 0.84 dB of
remaining _envelope_ headroom in the C4 gate; that is a comparison between two
different metrics and establishes nothing. It has been removed.

Two further limits, both real: the "quality" axis is distance from the same core
at 32 partials, not absolute quality — nothing here says the 32-partial render is
right; and `analysis` tracks at most 16 harmonics (`analysis/features.go`), so a
difference confined entirely above the 16th harmonic is invisible to
`partial_level_rmse_db` — harmonics 9–16 and the full-spectrum metrics do remain
sensitive. The DWG-referenced score was measured too, as 12.4 asks, and is
useless for this: it moves 0.004 across a 24x change in partial count because it
saturates spectrally, and at C2 it gets _worse_ as partials are added, since a
richer modal spectrum diverges further from DWG's sparse impulse train.

## Lessons worth keeping

**Batch width decides whether SIMD pays.** Calling `algo-vecmath` once _per note_
was measured **slower** than the scalar loop it replaced (+8% to +11% at high
polyphony): one note holds only ~24 modes, so per-call overhead beat the
vectorization. Batching all active notes into a single ~1500-mode call is what
produced the win. Batch width is the deciding factor before adding SIMD anywhere
else in the voice path.

**Denormals cost more than the SIMD work saved.** Modal partials decayed into the
float32 denormal range and, because a sustained group never deactivates, stalled
there indefinitely (93% of mode state denormal after 2000 blocks). Flushing
inaudible modes at block rate cut sustained-decay cost from 4.2 ms to 0.59 ms — a
larger win than the SIMD work.
