package piano

import "testing"

func TestVoiceUnisonStringCountByRange(t *testing.T) {
	vLow := NewVoice(48000, 30, 90, NewDefaultParams())
	vMid := NewVoice(48000, 60, 90, NewDefaultParams())
	vHigh := NewVoice(48000, 80, 90, NewDefaultParams())

	if len(vLow.strings) != 1 {
		t.Fatalf("expected low note to allocate 1 string, got %d", len(vLow.strings))
	}
	if len(vMid.strings) != 2 {
		t.Fatalf("expected mid note to allocate 2 strings, got %d", len(vMid.strings))
	}
	if len(vHigh.strings) != 3 {
		t.Fatalf("expected high note to allocate 3 strings, got %d", len(vHigh.strings))
	}
}
