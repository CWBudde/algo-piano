package piano

import (
	"math"
	"testing"
)

func TestReleaseWithPedalUpDecaysQuickly(t *testing.T) {
	p := NewPiano(48000, 16, NewDefaultParams())
	p.NoteOn(60, 100)
	_ = p.Process(4800)
	p.NoteOff(60)

	var tail []float32
	for i := 0; i < 20; i++ {
		tail = p.Process(256)
	}
	rms := stereoRMS(tail)
	if rms > 0.01 {
		t.Fatalf("expected short release with pedal up, got tail RMS %f", rms)
	}
}

func TestSustainPedalKeepsNoteRinging(t *testing.T) {
	withPedal := NewPiano(48000, 16, NewDefaultParams())
	withPedal.SetSustainPedal(true)
	withPedal.NoteOn(60, 100)
	_ = withPedal.Process(4800)
	withPedal.NoteOff(60)

	withoutPedal := NewPiano(48000, 16, NewDefaultParams())
	withoutPedal.SetSustainPedal(false)
	withoutPedal.NoteOn(60, 100)
	_ = withoutPedal.Process(4800)
	withoutPedal.NoteOff(60)

	var tailWith []float32
	var tailWithout []float32
	for i := 0; i < 20; i++ {
		tailWith = withPedal.Process(256)
		tailWithout = withoutPedal.Process(256)
	}

	rmsWith := stereoRMS(tailWith)
	rmsWithout := stereoRMS(tailWithout)
	if rmsWith <= rmsWithout*1.5 {
		t.Fatalf("expected sustain pedal to keep more energy: with=%f without=%f", rmsWith, rmsWithout)
	}
}

func TestSoftPedalAdjustsVoiceStrikeAndHammerHardness(t *testing.T) {
	v := NewVoice(48000, 60, 100, NewDefaultParams())
	baseStrike := v.strikePos
	baseStiffness := v.hammer.stiffness
	baseExponent := v.hammer.exponent

	v.SetSoftPedal(true)
	if v.strikePos <= baseStrike {
		t.Fatalf("expected soft pedal to move strike away from bridge: base=%f soft=%f", baseStrike, v.strikePos)
	}
	if v.hammer.stiffness >= baseStiffness {
		t.Fatalf("expected soft pedal to reduce hammer stiffness: base=%f soft=%f", baseStiffness, v.hammer.stiffness)
	}
	if v.hammer.exponent >= baseExponent {
		t.Fatalf("expected soft pedal to reduce hammer exponent: base=%f soft=%f", baseExponent, v.hammer.exponent)
	}

	v.SetSoftPedal(false)
	if math.Abs(float64(v.strikePos-baseStrike)) > 1e-6 {
		t.Fatalf("expected strike position reset after soft pedal release: got=%f want=%f", v.strikePos, baseStrike)
	}
	if math.Abs(float64(v.hammer.stiffness-baseStiffness)) > 1e-3 {
		t.Fatalf("expected hammer stiffness reset after soft pedal release: got=%f want=%f", v.hammer.stiffness, baseStiffness)
	}
}

func TestSoftPedalReducesAttackBrightness(t *testing.T) {
	const sampleRate = 48000
	const note = 60
	const velocity = 100
	const frames = 4096

	normal := NewPiano(sampleRate, 16, NewDefaultParams())
	normal.NoteOn(note, velocity)
	normalOut := normal.Process(frames)

	soft := NewPiano(sampleRate, 16, NewDefaultParams())
	soft.SetSoftPedal(true)
	soft.NoteOn(note, velocity)
	softOut := soft.Process(frames)

	normalMono := make([]float32, frames)
	softMono := make([]float32, frames)
	for i := 0; i < frames; i++ {
		normalMono[i] = normalOut[i*2]
		softMono[i] = softOut[i*2]
	}

	normalCentroid := spectralCentroid(normalMono, sampleRate, 2048)
	softCentroid := spectralCentroid(softMono, sampleRate, 2048)
	if softCentroid >= normalCentroid {
		t.Fatalf("expected soft pedal to lower attack brightness: soft=%.2fHz normal=%.2fHz", softCentroid, normalCentroid)
	}
}

func TestIRDryWetMixCanBypassConvolverOutput(t *testing.T) {
	wetOnly := NewDefaultParams()
	wetOnly.IRWetMix = 1.0
	wetOnly.IRDryMix = 0.0
	pw := NewPiano(48000, 16, wetOnly)
	pw.convolver.SetIR([]float32{0.0}, []float32{0.0})
	pw.NoteOn(60, 100)
	wetOut := pw.Process(512)
	if stereoRMS(wetOut) > 1e-6 {
		t.Fatalf("expected near-silence for wet-only with zero IR, got %f", stereoRMS(wetOut))
	}

	dryOnly := NewDefaultParams()
	dryOnly.IRWetMix = 0.0
	dryOnly.IRDryMix = 1.0
	pd := NewPiano(48000, 16, dryOnly)
	pd.convolver.SetIR([]float32{0.0}, []float32{0.0})
	pd.NoteOn(60, 100)
	dryOut := pd.Process(512)
	if stereoRMS(dryOut) <= 1e-4 {
		t.Fatalf("expected audible dry signal with dry-only mix, got %f", stereoRMS(dryOut))
	}
}
