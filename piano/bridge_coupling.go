package piano

// This file owns the bridge half of the unison coupling: the term that damps a
// unison group's COMMON motion. It lives apart from ringing.go because that file
// is already close to the 1500-line revive bound and because the two coupling
// terms are separate mechanisms that happen to share a loop.
//
// WHAT THE TWO TERMS ARE
//
// A unison group's strings move in normal modes. With unison gains g summing to
// one and per-string outputs y, the bridge sees mix = sum(g_i * y_i).
//
//	unison_crossfeed  F_i = +c * g_i * (mix - y_i)
//	bridge_coupling   F_i = -b * g_i * mix
//
// As matrices on y these are c*(g g^T - diag(g)) and -b * g g^T. The first
// ANNIHILATES the in-phase vector: when the strings move together mix == y_i and
// the force is identically zero at any c. It damps relative motion only. The
// second annihilates every vector that cancels at the bridge and damps the
// in-phase motion alone.
//
// That is the whole double-decay mechanism. The hammer drives the strings in
// phase, so the note starts in the mode bridge_coupling damps hardest - the
// PROMPT sound, fast. Detune rotates that energy into out-of-phase motion, which
// the bridge barely sees, and what is left is the AFTERSOUND, slow. Until this
// term existed the renderer had only the relative-motion damper, which shortens
// the aftersound and leaves the prompt untouched - the opposite ordering.
//
// WHY THE g_i WEIGHT IS LOAD-BEARING
//
// The energy that matters lives in the delay line: adding f to a slot holding v
// changes sum(delayLine^2) by 2vf + f^2, and the slot's value at read time IS
// y_i. So the pairing that decides passivity is sum_i y_i * F_i - the same one
// RingingStringGroup.processSample's older comment uses.
//
//	unweighted -b*mix:    sum y_i F_i = -b * mix * sum(y_i)
//	weighted   -b*g_i*mix: sum y_i F_i = -b * mix * sum(g_i*y_i) = -b * mix^2 <= 0
//
// sum(y_i) and mix are different linear functionals whenever the gains are
// unequal, so the unweighted form is sign-INDEFINITE. With the shipped
// two-string gains (0.52, 0.48), y = (1, -1.0416) gives mix = +0.02 but
// sum(y_i) = -0.0416, so the unweighted term ADDS energy. defaultUnisonForNote
// gives unequal gains at every multi-string note, so that state is reachable,
// not academic.
//
// The weighted form needs no Jensen step and does not need the gains to sum to
// one - a strictly stronger guarantee than the crossfeed's. Physically it is a
// transformer: the bridge sees mix, produces one force -b*mix, and string i
// receives it back through the same ratio g_i that put it into the mix.
//
// WHY SINGLE-STRING GROUPS ARE GATED OUT
//
// A lone string does load the bridge, so physically the term belongs there too.
// It is skipped anyway for two reasons. It is DEGENERATE there: with one string
// the force is -b*g_0^2*y_0, a constant fractional decrement per round trip,
// which is exactly what per-note loss and high_freq_damping already control - a
// second non-orthogonal knob makes the fitter's search worse without adding a
// degree of freedom. And it would cost the bit-exactness controls on notes below
// MIDI 40 that attribute any coupling failure to the coupling and nothing else.
//
// COUPLING AGAINST DETUNE
//
// Rank-one damping b*g g^T has eigenvalues b*|g|^2 and zero; detuning is a
// diagonal imaginary perturbation of size dw. Double decay only appears while
// b*|g|^2*f0 >~ dw. Below that the eigenvectors collapse onto the individual
// strings, both damp at ~b*g_i^2, and the result is a beat with a SINGLE decay
// rate. With defaultUnisonForNote's +-1.8 cents, dw ~= 0.0131*f0 and |g|^2 = 0.5,
// so the effect needs b >~ 0.026 - about 30x DefaultUnisonCrossfeed. The effect
// is a ratio against detune, which is why the envelope test pins
// UnisonDetuneScale rather than leaving it at the preset value.

// maxBridgeCoupling bounds the bridge coupling strength a preset may ask for.
//
// Measured 2026-08-23 (Go 1.26.x, linux/amd64). One note struck under a held
// pedal, resonance and inter-note coupling off, unison_crossfeed = 0 so only b
// is under test; peak of the last 5 s window over the 25-30 s window for the
// DWG core and over the 5-10 s window for the modal core; 4 notes (45, 52, 60,
// 72) x 2 cores x 2 sample rates = 16 renders per row:
//
//	b        non-finite   worst decay ratio over the 16 renders
//	0                0    0.0256
//	0.005            0    2.47e-07
//	0.01             0    4.59e-12
//	0.02             0    8.90e-22
//	0.026            0    1.33e-22   <- deepest; see the detune note above
//	0.05             0    2.13e-10
//	0.1              0    3.33e-06
//	0.25             0    5.40e-04
//	0.5              1    2.19e-03   first divergence: 44.1 kHz, DWG, note 60
//	1.0              4    1.61e+23
//
// Two things in that table are worth keeping. The ratio bottoms out at b ~ 0.026
// - exactly the coupling-against-detune threshold derived above - and then
// climbs again, which is the one-sample injection lag turning the term
// anti-damping at the top partials, not a coincidence. And 44.1 kHz diverges
// BEFORE 48 kHz, because the lag is one SAMPLE and so is a larger fraction of a
// period at the lower rate. maxUnisonCrossfeed's table never checked a second
// sample rate; this one does, and it is the 44.1 kHz row that sets the cliff.
//
// The bound is set at 0.1: 2.5x under the largest value verified stable
// everywhere (0.25), 5x under the first divergence (0.5), and still 3.8x above
// the b >~ 0.026 the effect needs. The 12x margin maxUnisonCrossfeed uses is not
// available here - it would land at 0.02, under the detune threshold, and fence
// out the mechanism this term exists for.
const maxBridgeCoupling = float32(0.1)
