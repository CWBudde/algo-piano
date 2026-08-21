package piano

import (
	"math"
	"testing"
)

// withModalKernel selects a modal kernel for the duration of a test and
// restores the previous one afterwards. Tests using it must not call
// t.Parallel, since modalKernelMode is process-global.
func withModalKernel(t *testing.T, k modalKernel) {
	t.Helper()
	prevKernel, prevArena := modalKernelMode, modalArenaEnabled
	modalKernelMode = k
	modalArenaEnabled = false
	t.Cleanup(func() {
		modalKernelMode = prevKernel
		modalArenaEnabled = prevArena
	})
}

// withModalArena selects the batched arena path.
func withModalArena(t *testing.T) {
	t.Helper()
	prevKernel, prevArena := modalKernelMode, modalArenaEnabled
	modalArenaEnabled = true
	t.Cleanup(func() {
		modalKernelMode = prevKernel
		modalArenaEnabled = prevArena
	})
}

var modalKernelNames = map[modalKernel]string{
	modalKernelScalar: "scalar",
	modalKernelAccum:  "accum",
	modalKernelRotate: "rotate",
}

// modalRenderConfig describes a modal render scenario used by the parity tests.
type modalRenderConfig struct {
	coupling      bool
	couplingMode  CouplingMode
	resonance     bool
	notes         []int
	blocks        int
	sustainAt     int // block index at which the sustain pedal goes down, -1 to skip
	keyUpAt       int // block index at which all keys are released, -1 to skip
	sustainOffAt  int // block index at which the sustain pedal lifts, -1 to skip
	sampleRate    int
	modalPartials int
}

func defaultModalRenderConfig() modalRenderConfig {
	return modalRenderConfig{
		coupling:     true,
		couplingMode: CouplingModePhysical,
		resonance:    true,
		notes:        []int{36, 48, 55, 60, 64, 67, 72, 84},
		blocks:       64,
		sustainAt:    -1,
		keyUpAt:      -1,
		sustainOffAt: -1,
		sampleRate:   48000,
	}
}

func renderModal(cfg modalRenderConfig) []float32 {
	params := NewDefaultParams()
	params.StringModel = StringModelModal
	params.CouplingEnabled = cfg.coupling
	params.CouplingMode = cfg.couplingMode
	params.ResonanceEnabled = cfg.resonance
	if cfg.modalPartials > 0 {
		params.ModalPartials = cfg.modalPartials
	}

	sb := NewStringBank(cfg.sampleRate, params)
	h := NewHammerExciter(cfg.sampleRate, params)

	for _, n := range cfg.notes {
		sb.SetKeyDown(n, true)
		h.Trigger(n, 100)
	}

	out := make([]float32, 0, cfg.blocks*128)
	for b := range cfg.blocks {
		switch b {
		case cfg.sustainAt:
			sb.SetSustain(true)
		case cfg.keyUpAt:
			for _, n := range cfg.notes {
				sb.SetKeyDown(n, false)
			}
		case cfg.sustainOffAt:
			sb.SetSustain(false)
		}
		out = append(out, sb.Process(128, h)...)
	}
	return out
}

// assertKernelParity renders cfg under every kernel and requires bit-identical
// output. A tolerance would defeat the purpose: the vecmath kernels use the
// same operand order as the scalar reference and its SIMD backends avoid FMA,
// so any difference at all signals a real divergence.
func assertKernelParity(t *testing.T, cfg modalRenderConfig) {
	t.Helper()

	withModalKernel(t, modalKernelScalar)
	want := renderModal(cfg)
	if len(want) == 0 {
		t.Fatalf("reference render produced no samples")
	}

	nonZero := false
	for _, s := range want {
		if s != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatalf("reference render is silent; the parity check would be vacuous")
	}

	// Every per-group kernel, then the batched arena path, must all reproduce
	// the scalar reference exactly.
	variants := []struct {
		name  string
		apply func()
	}{
		{"accum", func() { modalArenaEnabled = false; modalKernelMode = modalKernelAccum }},
		{"rotate", func() { modalArenaEnabled = false; modalKernelMode = modalKernelRotate }},
		{"arena", func() { modalArenaEnabled = true }},
	}

	for _, v := range variants {
		v.apply()
		got := renderModal(cfg)
		if len(got) != len(want) {
			t.Fatalf("%s: length mismatch: got=%d want=%d", v.name, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: sample %d differs: got=%v want=%v", v.name, i, got[i], want[i])
			}
		}
	}
}

func TestModalKernelScalarMatchesVecmathBitExact(t *testing.T) {
	assertKernelParity(t, defaultModalRenderConfig())
}

func TestModalKernelParityAcrossDamperTransitions(t *testing.T) {
	cfg := defaultModalRenderConfig()
	cfg.sustainAt = 8
	cfg.keyUpAt = 20
	cfg.sustainOffAt = 33
	assertKernelParity(t, cfg)
}

func TestModalKernelParityWithCouplingAndResonance(t *testing.T) {
	cfg := defaultModalRenderConfig()
	cfg.coupling = true
	cfg.couplingMode = CouplingModePhysical
	cfg.resonance = true
	cfg.sustainAt = 4
	assertKernelParity(t, cfg)
}

func TestModalKernelParityAtLowSampleRate(t *testing.T) {
	// A low sample rate pushes high notes past the Nyquist cutoff, exercising
	// ragged mode counts and the single-mode fallback under every kernel.
	cfg := defaultModalRenderConfig()
	cfg.sampleRate = 8000
	cfg.notes = []int{60, 96, 103, 108}
	cfg.modalPartials = 8
	assertKernelParity(t, cfg)
}

func TestModalArenaBindsAndReleases(t *testing.T) {
	withModalArena(t)

	params := NewDefaultParams()
	params.StringModel = StringModelModal
	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)

	if sb.modalArena == nil {
		t.Fatalf("expected a modal arena for the modal core")
	}

	notes := []int{48, 60, 72}
	for _, n := range notes {
		sb.SetKeyDown(n, true)
		h.Trigger(n, 100)
	}
	_ = sb.Process(128, h)

	a := sb.modalArena
	if a.used == 0 {
		t.Fatalf("arena bound no modes; the batched path was never exercised")
	}
	if a.bound {
		t.Fatalf("arena still bound after Process returned")
	}
	for note, bound := range a.boundNote {
		if bound {
			t.Fatalf("note %d left marked as bound after release", note)
		}
	}

	// After release each group must own its state again, not alias the arena.
	for _, n := range notes {
		g := sb.ModalGroup(n)
		if g == nil {
			t.Fatalf("expected modal group for note %d", n)
		}
		if len(g.re) != len(g.order) {
			t.Fatalf("note %d: group slices not restored (len(re)=%d want %d)", n, len(g.re), len(g.order))
		}
		if &g.re[0] == &a.re[0] {
			t.Fatalf("note %d: group state still aliases the arena", n)
		}
	}
}

// TestModalArenaSurvivesActiveSetChanges exercises the layout-rebuild path: the
// compacted note set changes between blocks as notes are struck and released,
// which must not corrupt any group's state.
func TestModalArenaSurvivesActiveSetChanges(t *testing.T) {
	cfg := defaultModalRenderConfig()
	cfg.blocks = 96
	cfg.sustainAt = 5
	cfg.keyUpAt = 40
	cfg.sustainOffAt = 55
	assertKernelParity(t, cfg)
}

func TestModalAccScratchIsZeroBetweenSamples(t *testing.T) {
	withModalKernel(t, modalKernelAccum)

	params := NewDefaultParams()
	params.StringModel = StringModelModal
	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)

	notes := []int{48, 60, 72}
	for _, n := range notes {
		sb.SetKeyDown(n, true)
		h.Trigger(n, 100)
	}
	for range 16 {
		_ = sb.Process(128, h)
	}

	for _, n := range notes {
		g := sb.ModalGroup(n)
		if g == nil {
			t.Fatalf("expected modal group for note %d", n)
		}
		for i, v := range g.acc {
			if v != 0 {
				t.Fatalf("note %d: acc[%d] = %v, want 0; the fused clear in "+
					"advanceModesAccum is broken", n, i, v)
			}
		}
	}
}

func TestModalSoAOffsetsAreConsistent(t *testing.T) {
	for _, sampleRate := range []int{8000, 44100, 48000, 96000} {
		params := NewDefaultParams()
		params.StringModel = StringModelModal
		sb := NewStringBank(sampleRate, params)

		for note := 0; note < 128; note++ {
			g := sb.ModalGroup(note)
			if g == nil {
				continue
			}
			n := len(g.re)

			if len(g.modeStart) == 0 {
				t.Fatalf("sr=%d note=%d: modeStart must never be empty", sampleRate, note)
			}
			if g.modeStart[0] != 0 {
				t.Fatalf("sr=%d note=%d: modeStart[0] = %d, want 0", sampleRate, note, g.modeStart[0])
			}
			if got := int(g.modeStart[len(g.modeStart)-1]); got != n {
				t.Fatalf("sr=%d note=%d: modeStart tail = %d, want %d", sampleRate, note, got, n)
			}
			for i := 1; i < len(g.modeStart); i++ {
				if g.modeStart[i] < g.modeStart[i-1] {
					t.Fatalf("sr=%d note=%d: modeStart not monotonic at %d", sampleRate, note, i)
				}
			}

			lens := map[string]int{
				"im": len(g.im), "cosW": len(g.cosW), "sinW": len(g.sinW),
				"decay": len(g.decay), "decayUndamped": len(g.decayUndamped),
				"decayDamped": len(g.decayDamped), "gain": len(g.gain),
				"acc": len(g.acc), "order": len(g.order),
				"shapeRes": len(g.shapeRes), "shapeCoup": len(g.shapeCoup),
				"shapeHammer": len(g.shapeHammer),
			}
			for name, l := range lens {
				if l != n {
					t.Fatalf("sr=%d note=%d: len(%s) = %d, want %d", sampleRate, note, name, l, n)
				}
			}

			for i, o := range g.order {
				if o < 1 {
					t.Fatalf("sr=%d note=%d: order[%d] = %d, want >= 1", sampleRate, note, i, o)
				}
			}
		}
	}
}

func TestModalFallbackSingleModeLayout(t *testing.T) {
	// At 8 kHz the top of the keyboard sits above the Nyquist cutoff, so every
	// unison string must take the single-mode fallback rather than being
	// allocated zero modes.
	params := NewDefaultParams()
	params.StringModel = StringModelModal
	params.ModalPartials = 8
	sb := NewStringBank(8000, params)

	g := sb.ModalGroup(108)
	if g == nil {
		t.Skip("note 108 not allocated at this sample rate")
	}
	if g.stringCount() == 0 {
		t.Fatalf("expected at least one unison string")
	}
	for si := range g.stringCount() {
		if got := g.modeCount(si); got < 1 {
			t.Fatalf("string %d: modeCount = %d, want >= 1", si, got)
		}
	}
	if got := int(g.modeStart[g.stringCount()]); got != len(g.re) {
		t.Fatalf("fallback sizing is off: modeStart tail = %d, len(re) = %d", got, len(g.re))
	}
}

func TestModalLongRenderIsFiniteAcrossKernels(t *testing.T) {
	for _, k := range []modalKernel{modalKernelScalar, modalKernelAccum, modalKernelRotate} {
		t.Run(modalKernelNames[k], func(t *testing.T) {
			withModalKernel(t, k)

			params := NewDefaultParams()
			params.StringModel = StringModelModal
			p := NewPiano(48000, 16, params)
			p.SetSustainPedal(true)
			p.NoteOn(48, 80)
			p.NoteOn(60, 90)
			p.NoteOn(72, 110)

			for b := range 300 {
				out := p.Process(128)
				for j, s := range out {
					if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
						t.Fatalf("non-finite sample at block %d sample %d: %v", b, j, s)
					}
				}
			}
		})
	}
}
