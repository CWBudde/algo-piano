//go:build !js

package piano

import (
	"os"
	"testing"
)

const testIRPath = "../assets/ir/default_96k.wav"

// impulse returns a unit impulse followed by silence.
func impulse(n int) []float32 {
	in := make([]float32, n)
	in[0] = 1.0
	return in
}

func readTestIR(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(testIRPath)
	if err != nil {
		t.Fatalf("read %s: %v", testIRPath, err)
	}
	return data
}

func TestSoundboardConvolverSetIRFromBytesMatchesWAV(t *testing.T) {
	const rate = 48000
	fromFile := NewSoundboardConvolver(rate)
	if err := fromFile.SetIRFromWAV(testIRPath); err != nil {
		t.Fatalf("SetIRFromWAV: %v", err)
	}

	fromBytes := NewSoundboardConvolver(rate)
	if err := fromBytes.SetIRFromBytes(readTestIR(t)); err != nil {
		t.Fatalf("SetIRFromBytes: %v", err)
	}

	if fromFile.irLen != fromBytes.irLen {
		t.Fatalf("irLen mismatch: file=%d bytes=%d", fromFile.irLen, fromBytes.irLen)
	}

	in := impulse(4096)
	a := fromFile.Process(in)
	b := fromBytes.Process(in)
	if len(a) != len(b) {
		t.Fatalf("output length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sample %d differs: file=%v bytes=%v", i, a[i], b[i])
		}
	}
}

func TestBodyConvolverSetIRFromBytesMatchesWAV(t *testing.T) {
	const rate = 44100
	fromFile := NewBodyConvolver(rate)
	if err := fromFile.SetIRFromWAV(testIRPath, rate); err != nil {
		t.Fatalf("SetIRFromWAV: %v", err)
	}

	fromBytes := NewBodyConvolver(rate)
	if err := fromBytes.SetIRFromBytes(readTestIR(t), rate); err != nil {
		t.Fatalf("SetIRFromBytes: %v", err)
	}

	in := impulse(4096)
	a := fromFile.Process(in)
	b := fromBytes.Process(in)
	if len(a) != len(b) {
		t.Fatalf("output length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sample %d differs: file=%v bytes=%v", i, a[i], b[i])
		}
	}
}

func TestPianoSetRoomIRFromBytesAndMixChangesOutput(t *testing.T) {
	const rate = 48000
	data := readTestIR(t)

	render := func(roomWet float32) []float32 {
		p := NewPiano(rate, 16, NewDefaultParams())
		if err := p.SetRoomIRFromBytes(data); err != nil {
			t.Fatalf("SetRoomIRFromBytes: %v", err)
		}
		p.SetIRMix(1.0, 1.0, roomWet, 1.0)
		p.NoteOn(60, 100)
		out := make([]float32, 0, 48000*2)
		for range 100 {
			out = append(out, p.Process(128)...)
		}
		return out
	}

	dry := render(0)
	wet := render(1)
	if len(dry) != len(wet) {
		t.Fatalf("length mismatch: %d vs %d", len(dry), len(wet))
	}

	var maxDiff float32
	for i := range dry {
		d := dry[i] - wet[i]
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff <= 1e-6 {
		t.Fatalf("roomWet=0 and roomWet=1 produced (near) identical output, maxDiff=%v", maxDiff)
	}
}

func TestPianoSetBodyIRFromBytes(t *testing.T) {
	const rate = 48000
	p := NewPiano(rate, 16, NewDefaultParams())
	if err := p.SetBodyIRFromBytes(readTestIR(t)); err != nil {
		t.Fatalf("SetBodyIRFromBytes: %v", err)
	}
	if err := p.SetBodyIRFromBytes([]byte("not a wav")); err == nil {
		t.Fatal("expected error for invalid WAV bytes")
	}
}

// TestPianoSetIRMixHonorsBodyGainWithLegacyIRPath guards against the legacy
// remap in Process discarding an explicitly configured body gain.
func TestPianoSetIRMixHonorsBodyGainWithLegacyIRPath(t *testing.T) {
	const rate = 48000

	render := func(bodyGain float32) []float32 {
		params := NewDefaultParams()
		// Legacy configuration: only the deprecated IRWavPath is populated.
		params.IRWavPath = testIRPath
		p := NewPiano(rate, 16, params)
		p.SetIRMix(1.0, bodyGain, 0.0, 1.0)
		p.NoteOn(60, 100)
		out := make([]float32, 0, 128*100*2)
		for range 100 {
			out = append(out, p.Process(128)...)
		}
		return out
	}

	full := render(1.0)
	half := render(0.5)
	if len(full) != len(half) {
		t.Fatalf("length mismatch: %d vs %d", len(full), len(half))
	}

	var maxDiff float32
	for i := range full {
		d := full[i] - half[i]
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff <= 1e-6 {
		t.Fatalf("bodyGain ignored: bodyGain=1 and bodyGain=0.5 produced (near) identical output, maxDiff=%v", maxDiff)
	}
}
