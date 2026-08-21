package analysis

import (
	"math"
	"testing"
)

// makeStruckTone builds a plucked/struck note: a linear rise of riseSec into
// an exponentially decaying tone. Unlike makeDecaySine it has a real onset, so
// the attack metric applies to it.
func makeStruckTone(sr int, freq float64, durationSec float64, decaySec float64, riseSec float64) []float64 {
	return makeStruckToneBright(sr, freq, durationSec, decaySec, riseSec, 0)
}

// makeStruckToneBright adds a decaying upper partial at 8x the fundamental
// with the given amplitude, so the onset starts bright and darkens. The
// partial decays six times faster than the fundamental, which is what makes
// the spectral centroid fall across the attack window.
func makeStruckToneBright(sr int, freq float64, durationSec float64, decaySec float64, riseSec float64, brightAmp float64) []float64 {
	n := int(float64(sr) * durationSec)
	if n < 1 {
		n = 1
	}
	riseFrames := int(riseSec * float64(sr))
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sr)
		env := math.Exp(-t / decaySec)
		if riseFrames > 0 && i < riseFrames {
			env *= float64(i) / float64(riseFrames)
		}
		v := math.Sin(2 * math.Pi * freq * t)
		if brightAmp > 0 {
			v += brightAmp * math.Exp(-t/(decaySec/6)) * math.Sin(2*math.Pi*8*freq*t)
		}
		out[i] = env * v
	}
	return out
}

func TestRiseTimeMSTracksOnsetSpeed(t *testing.T) {
	sr := 48000
	// A 1 kHz carrier keeps the 64-sample envelope frame above one period, so
	// the measurement is not dominated by intra-period RMS ripple.
	fast := riseTimeMS(makeStruckTone(sr, 1000.0, 1.0, 0.8, 0.005), sr)
	slow := riseTimeMS(makeStruckTone(sr, 1000.0, 1.0, 0.8, 0.060), sr)
	if !isFinite(fast) || !isFinite(slow) {
		t.Fatalf("expected both rise times to be measurable, got %f and %f", fast, slow)
	}
	if fast >= slow {
		t.Fatalf("expected the 5 ms onset to rise faster than the 60 ms one, got %f vs %f ms", fast, slow)
	}
	// A linear 10%-90% ramp over 60 ms covers 80% of it, i.e. about 48 ms.
	if math.Abs(slow-48.0) > 8.0 {
		t.Fatalf("expected a 60 ms linear ramp to give a 10-90%% rise near 48.000000 ms, got %f", slow)
	}
	if fast > 10.0 {
		t.Fatalf("expected a 5 ms onset to rise in well under 10.000000 ms, got %f", fast)
	}
}

func TestRiseTimeMSUndefinedWithoutOnset(t *testing.T) {
	sr := 48000
	// A bare decaying sine is at its loudest in the very first frame.
	if v := riseTimeMS(makeDecaySine(sr, 220.0, 1.0, 0.5), sr); !math.IsNaN(v) {
		t.Fatalf("expected NaN for a signal with no rising onset, got %f", v)
	}
	if v := riseTimeMS(nil, sr); !math.IsNaN(v) {
		t.Fatalf("expected NaN for an empty signal, got %f", v)
	}
}

func TestSpectralCentroidHzMatchesASingleTone(t *testing.T) {
	sr := 48000
	x := makeDecaySine(sr, 1000.0, 0.5, 10.0)
	traj := attackCentroidTrajectory(x, sr)
	if len(traj) == 0 {
		t.Fatalf("expected a centroid trajectory for a 0.5 s tone")
	}
	// A pure 1 kHz tone in a 50 Hz - 12 kHz band sits close to 1 kHz; window
	// leakage pulls the magnitude-weighted mean around a little.
	for i, c := range traj {
		if math.Abs(c-1000.0) > 200.0 {
			t.Fatalf("expected frame %d centroid near 1000.000000 Hz, got %f", i, c)
		}
	}
}

func TestAttackCentroidTrajectoryCoversTheAttackWindow(t *testing.T) {
	for _, sr := range []int{44100, 48000} {
		x := makeStruckToneBright(sr, 220.0, 1.0, 0.8, 0.004, 0.6)
		traj := attackCentroidTrajectory(x, sr)
		// attackWindowSec of audio hopped by attackFFTHop, counting frames
		// whose centre lands inside the window.
		want := 0
		for start := 0; start+attackFFTSize <= len(x); start += attackFFTHop {
			if start+attackFFTSize/2 >= int(attackWindowSec*float64(sr)) {
				break
			}
			want++
		}
		if len(traj) != want {
			t.Fatalf("sr=%d: expected %d attack frames, got %d", sr, want, len(traj))
		}
		if len(traj) < 10 || len(traj) > 16 {
			t.Fatalf("sr=%d: expected roughly twelve frames in %0.0f ms, got %d",
				sr, attackWindowSec*1000, len(traj))
		}
	}
}

func TestAttackCentroidRMSEOctIsRegisterNeutral(t *testing.T) {
	// The same relative brightness difference must score the same at any
	// register: that is the whole reason for comparing in octaves.
	low := attackCentroidRMSEOct([]float64{200, 210, 220}, []float64{400, 420, 440})
	high := attackCentroidRMSEOct([]float64{1600, 1680, 1760}, []float64{3200, 3360, 3520})
	if math.Abs(low-1.0) > 1e-12 {
		t.Fatalf("expected a doubled centroid to read as 1.000000 octave, got %f", low)
	}
	if math.Abs(low-high) > 1e-12 {
		t.Fatalf("expected the same octave error in both registers, got %f vs %f", low, high)
	}
	if v := attackCentroidRMSEOct([]float64{440, 450}, []float64{440, 450}); v != 0 {
		t.Fatalf("expected 0.000000 for identical trajectories, got %f", v)
	}
	if v := attackCentroidRMSEOct(nil, nil); !math.IsNaN(v) {
		t.Fatalf("expected NaN for empty trajectories, got %f", v)
	}
}

func TestAttackMetricDetectsSlowerRise(t *testing.T) {
	sr := 48000
	ref := makeStruckTone(sr, 220.0, 1.5, 0.9, 0.005)
	same := makeStruckTone(sr, 220.0, 1.5, 0.9, 0.005)
	slower := makeStruckTone(sr, 220.0, 1.5, 0.9, 0.050)

	mSame := Compare(ref, same, sr)
	mSlow := Compare(ref, slower, sr)

	if !mSame.AttackAvailable || !mSlow.AttackAvailable {
		t.Fatalf("expected the attack metric to be available for both pairs, got %t and %t",
			mSame.AttackAvailable, mSlow.AttackAvailable)
	}
	if mSame.AttackRiseDiffMS > 1e-9 {
		t.Fatalf("expected an identical onset to give a 0.000000 ms rise difference, got %f", mSame.AttackRiseDiffMS)
	}
	if mSlow.AttackRiseDiffMS <= mSame.AttackRiseDiffMS {
		t.Fatalf("expected the slower onset to show a larger rise difference, got %f vs %f",
			mSlow.AttackRiseDiffMS, mSame.AttackRiseDiffMS)
	}
	if mSlow.CandRiseTimeMS <= mSlow.RefRiseTimeMS {
		t.Fatalf("expected the candidate to rise more slowly than the reference, got %f vs %f ms",
			mSlow.CandRiseTimeMS, mSlow.RefRiseTimeMS)
	}
	if mSlow.AttackNorm <= mSame.AttackNorm {
		t.Fatalf("expected the slower onset to normalize worse, got %f vs %f",
			mSlow.AttackNorm, mSame.AttackNorm)
	}
}

func TestAttackMetricDetectsBrighterOnset(t *testing.T) {
	sr := 48000
	ref := makeStruckToneBright(sr, 220.0, 1.5, 0.9, 0.005, 0.15)
	same := makeStruckToneBright(sr, 220.0, 1.5, 0.9, 0.005, 0.15)
	brighter := makeStruckToneBright(sr, 220.0, 1.5, 0.9, 0.005, 3.0)

	mSame := Compare(ref, same, sr)
	mBright := Compare(ref, brighter, sr)

	if !mSame.AttackAvailable || !mBright.AttackAvailable {
		t.Fatalf("expected the attack metric to be available for both pairs, got %t and %t",
			mSame.AttackAvailable, mBright.AttackAvailable)
	}
	if mSame.AttackCentroidRMSEOct > 1e-9 {
		t.Fatalf("expected an identical onset to give a 0.000000 octave centroid error, got %f",
			mSame.AttackCentroidRMSEOct)
	}
	if mBright.AttackCentroidRMSEOct < 0.2 {
		t.Fatalf("expected a clearly brighter onset to move the centroid by at least 0.200000 octaves, got %f",
			mBright.AttackCentroidRMSEOct)
	}
	if mBright.CandAttackCentroidHz <= mBright.RefAttackCentroidHz {
		t.Fatalf("expected the brighter candidate to have the higher attack centroid, got %f vs %f Hz",
			mBright.CandAttackCentroidHz, mBright.RefAttackCentroidHz)
	}
	// Both onsets ramp in over the same 5 ms, so the rise half of the metric
	// stays small and the centroid half carries the difference. (It is not
	// exactly zero: a much louder upper partial reshapes the RMS envelope a
	// little, which is a real difference in the signal, not an artifact.)
	if mBright.AttackRiseDiffMS > 0.25*NormAttackRise {
		t.Fatalf("expected the rise difference to stay small next to the colour change, got %f ms",
			mBright.AttackRiseDiffMS)
	}
	if mBright.AttackCentroidRMSEOct/NormAttackCentroid <= mBright.AttackRiseDiffMS/NormAttackRise {
		t.Fatalf("expected the centroid half to dominate, got %f oct vs %f ms",
			mBright.AttackCentroidRMSEOct, mBright.AttackRiseDiffMS)
	}
	if mBright.AttackNorm <= mSame.AttackNorm {
		t.Fatalf("expected the brighter onset to normalize worse, got %f vs %f",
			mBright.AttackNorm, mSame.AttackNorm)
	}
}

// TestAttackMetricUnavailableOnDecayOnlyWindow covers the case that motivates
// the availability flag: piano-modal-fit calibrates against windows cut out of
// the decay, which contain no onset at all. Scoring an attack term there would
// fold a meaningless number into the calibration, so the term must drop out
// and the remaining weights must renormalize.
func TestAttackMetricUnavailableOnDecayOnlyWindow(t *testing.T) {
	sr := 48000
	full := makeStruckTone(sr, 220.0, 3.0, 1.2, 0.005)
	// Cut a window well after the onset: it only ever gets quieter.
	decayOnly := full[sr*2:]
	other := makeStruckTone(sr, 220.0, 3.0, 0.7, 0.005)[sr*2:]

	m := Compare(decayOnly, other, sr)
	if m.AttackAvailable {
		t.Fatalf("expected the attack metric to be unavailable on a decay-only window")
	}
	if !math.IsNaN(m.AttackNorm) {
		t.Fatalf("expected an undefined attack norm on a decay-only window, got %f", m.AttackNorm)
	}

	attackProfile, err := WeightsForProfile(ProfileAttackV1)
	if err != nil {
		t.Fatalf("expected attack-v1 profile to resolve, got error: %v", err)
	}
	mp := CompareWithWeights(decayOnly, other, sr, attackProfile)
	if mp.AttackAvailable {
		t.Fatalf("expected the attack metric to stay unavailable under attack-v1")
	}
	if !isFinite(mp.Score) || mp.Score <= 0 || mp.Score > 1 {
		t.Fatalf("expected a finite score in (0,1] with the attack term dropped, got %f", mp.Score)
	}
	if mp.Dominant == ComponentAttack {
		t.Fatalf("expected an unavailable attack term not to be reported as dominant")
	}

	// The score must equal the surviving weights renormalized to sum to 1.
	surviving := attackProfile.Envelope + attackProfile.Spectral +
		attackProfile.PartialLevel + attackProfile.Tristimulus
	want := clamp01((attackProfile.Envelope*mp.EnvelopeNorm +
		attackProfile.Spectral*mp.SpectralNorm +
		attackProfile.PartialLevel*mp.PartialLevelNorm +
		attackProfile.Tristimulus*mp.TristimulusNorm) / surviving)
	if math.Abs(mp.Score-want) > 1e-12 {
		t.Fatalf("expected the remaining weights to be renormalized to %f, got %f", want, mp.Score)
	}
}

// TestAttackUnavailabilityDoesNotDisturbLegacyScore guards the compatibility
// promise from the other side: legacy-v1 puts no weight on the attack, so
// neither its availability nor the renormalization branch may touch its score.
func TestAttackUnavailabilityDoesNotDisturbLegacyScore(t *testing.T) {
	sr := 48000
	a := makeDecaySine(sr, 261.63, 1.8, 0.8)
	b := makeDecaySine(sr, 277.18, 1.8, 0.6)

	m := Compare(a, b, sr)
	if m.AttackAvailable {
		t.Fatalf("expected no onset in a bare decaying sine")
	}
	legacy := clamp01(WeightTime*m.TimeNorm + WeightEnvelope*m.EnvelopeNorm +
		WeightSpectral*m.SpectralNorm + WeightDecay*m.DecayNorm)
	if m.Score != legacy {
		t.Fatalf("expected the legacy score %.17g, got %.17g", legacy, m.Score)
	}
}

func TestAttackNormCombinesRiseAndCentroid(t *testing.T) {
	m := Metrics{
		AttackAvailable:       true,
		AttackRiseDiffMS:      NormAttackRise, // saturates its half
		AttackCentroidRMSEOct: 0,              // contributes nothing
	}
	if got := attackNormOf(m, LegacyNorms()); math.Abs(got-0.5) > 1e-12 {
		t.Fatalf("expected a saturated rise alone to give 0.500000, got %f", got)
	}

	m.AttackRiseDiffMS = 0
	m.AttackCentroidRMSEOct = NormAttackCentroid
	if got := attackNormOf(m, LegacyNorms()); math.Abs(got-0.5) > 1e-12 {
		t.Fatalf("expected a saturated centroid alone to give 0.500000, got %f", got)
	}

	m.AttackRiseDiffMS = NormAttackRise / 2
	m.AttackCentroidRMSEOct = NormAttackCentroid / 2
	if got := attackNormOf(m, LegacyNorms()); math.Abs(got-0.5) > 1e-12 {
		t.Fatalf("expected two half-scale errors to give 0.500000, got %f", got)
	}

	m.AttackAvailable = false
	if got := attackNormOf(m, LegacyNorms()); !math.IsNaN(got) {
		t.Fatalf("expected NaN when the attack is unavailable, got %f", got)
	}
}
