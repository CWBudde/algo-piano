package piano

import "testing"

// pedalTailRMS renders one note, releases it with the sustain pedal at the
// given depth, and reports the RMS of the tail that is left ringing.
func pedalTailRMS(t *testing.T, model StringModel, amount float32) float64 {
	t.Helper()

	params := NewDefaultParams()
	params.StringModel = model
	p := NewPiano(48000, 16, params)
	p.SetSustainPedalAmount(amount)
	p.NoteOn(60, 100)
	_ = p.Process(4800)
	p.NoteOff(60)

	var tail []float32
	for i := 0; i < 20; i++ {
		tail = p.Process(256)
	}
	return stereoRMS(tail)
}

func TestPartialSustainPedalScalesResidualEnergy(t *testing.T) {
	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			none := pedalTailRMS(t, model, 0)
			half := pedalTailRMS(t, model, 0.5)
			full := pedalTailRMS(t, model, 1)

			if half <= none {
				t.Fatalf("expected half pedal to leave more energy than no pedal: half=%g none=%g", half, none)
			}
			if full <= half {
				t.Fatalf("expected full pedal to leave more energy than half pedal: full=%g half=%g", full, half)
			}
		})
	}
}

func TestSustainPedalBoolMatchesAmountExtremes(t *testing.T) {
	renderWith := func(model StringModel, apply func(*Piano)) []float32 {
		params := NewDefaultParams()
		params.StringModel = model
		p := NewPiano(48000, 16, params)
		apply(p)
		p.NoteOn(60, 100)
		out := p.Process(2048)
		p.NoteOff(60)
		return append(out, p.Process(2048)...)
	}

	cases := []struct {
		name   string
		boolFn func(*Piano)
		amtFn  func(*Piano)
	}{
		{"down", func(p *Piano) { p.SetSustainPedal(true) }, func(p *Piano) { p.SetSustainPedalAmount(1) }},
		{"up", func(p *Piano) { p.SetSustainPedal(false) }, func(p *Piano) { p.SetSustainPedalAmount(0) }},
	}

	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		for _, tc := range cases {
			t.Run(string(model)+"/"+tc.name, func(t *testing.T) {
				want := renderWith(model, tc.boolFn)
				got := renderWith(model, tc.amtFn)
				if len(got) != len(want) {
					t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("sample %d differs: got %v want %v", i, got[i], want[i])
					}
				}
			})
		}
	}
}

func TestSustainPedalAmountIsClamped(t *testing.T) {
	p := NewPiano(48000, 16, NewDefaultParams())

	p.SetSustainPedalAmount(5)
	if p.sustainAmount != 1 {
		t.Fatalf("expected amount clamped to 1, got %v", p.sustainAmount)
	}
	if g := p.ringing.bank.Group(60); g == nil || g.sustainAmount != 1 {
		t.Fatalf("expected group sustain amount 1, got %+v", g)
	}

	p.SetSustainPedalAmount(-2)
	if p.sustainAmount != 0 {
		t.Fatalf("expected amount clamped to 0, got %v", p.sustainAmount)
	}
	if g := p.ringing.bank.Group(60); g == nil || g.sustainAmount != 0 {
		t.Fatalf("expected group sustain amount 0, got %+v", g)
	}
}

func TestPartialSustainKeepsStringUndampedForResonance(t *testing.T) {
	p := NewPiano(48000, 16, NewDefaultParams())
	g := p.ringing.bank.Group(67)
	if g == nil {
		t.Fatalf("expected group for note 67")
	}
	if g.isUndamped() {
		t.Fatalf("expected damped group with pedal up")
	}
	p.SetSustainPedalAmount(0.25)
	if !g.isUndamped() {
		t.Fatalf("expected half-pedalled group to count as undamped for resonance targeting")
	}
}
