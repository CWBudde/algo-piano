package piano

import "testing"

// newShapeTestGroup builds a mid-register modal group with several unison
// strings and enough partials to make the shape vectors interesting.
func newShapeTestGroup(t *testing.T) *ModalStringGroup {
	t.Helper()
	params := NewDefaultParams()
	params.StringModel = StringModelModal
	g := newModalStringGroup(48000, 60, params)
	if g == nil || len(g.order) == 0 {
		t.Fatalf("expected a populated modal group")
	}
	return g
}

// wantShape is the reference the cache must reproduce: the raw mode shape with
// inaudible modes zeroed. The division by the partial order stays in the
// injection loop, so it must not appear here.
func wantShape(order int32, strikePos float32) float32 {
	s := modalShape(int(order), strikePos)
	if s > -1e-6 && s < 1e-6 {
		return 0
	}
	return s
}

func assertShapeVector(t *testing.T, g *ModalStringGroup, got []float32, strikePos float32, ctx string) {
	t.Helper()
	if len(got) != len(g.order) {
		t.Fatalf("%s: len = %d, want %d", ctx, len(got), len(g.order))
	}
	for idx, order := range g.order {
		want := wantShape(order, strikePos)
		if got[idx] != want {
			t.Fatalf("%s: shape[%d] (order %d, pos %v) = %v, want %v",
				ctx, idx, order, strikePos, got[idx], want)
		}
	}
}

func TestModalShapeVectorsMatchDirectComputation(t *testing.T) {
	g := newShapeTestGroup(t)

	assertShapeVector(t, g, g.shapeRes, modalResonanceStrikePos, "resonance slot")
	assertShapeVector(t, g, g.shapeCoup, modalCouplingStrikePos, "coupling slot")

	for _, pos := range []float32{0.02, 0.1, 0.125, 0.25, 1.0 / 3.0, 0.5, 0.7, 0.98} {
		assertShapeVector(t, g, g.shapeVector(pos), pos, "hammer slot")
	}
}

func TestModalShapeVectorUsesClampedPositionAsCacheKey(t *testing.T) {
	g := newShapeTestGroup(t)

	// Two distinct out-of-range positions clamp to the same value, so the
	// second must be a cache hit on a vector built for the clamped position.
	got := g.shapeVector(clampStrikePos(-5))
	assertShapeVector(t, g, got, 0.01, "clamped low")
	if g.hammerShapePos != 0.01 {
		t.Fatalf("cache key = %v, want the clamped 0.01", g.hammerShapePos)
	}

	// A second, different below-minimum input clamps to the same 0.01, so this
	// call must hit the slot filled above instead of refilling it.
	filled := &g.shapeHammer[0]
	got = g.shapeVector(clampStrikePos(-0.5))
	assertShapeVector(t, g, got, 0.01, "clamped low again")
	if g.hammerShapePos != 0.01 {
		t.Fatalf("cache key = %v, want the clamped 0.01", g.hammerShapePos)
	}
	if &got[0] != filled {
		t.Fatalf("second below-minimum position did not reuse the cached slot")
	}

	got = g.shapeVector(clampStrikePos(3))
	assertShapeVector(t, g, got, 0.99, "clamped high")
	if g.hammerShapePos != 0.99 {
		t.Fatalf("cache key = %v, want the clamped 0.99", g.hammerShapePos)
	}
}

func TestModalShapeVectorInvalidatesOnPositionChange(t *testing.T) {
	g := newShapeTestGroup(t)

	if g.hammerShapePos != modalNoStrikePos {
		t.Fatalf("fresh group hammer key = %v, want %v", g.hammerShapePos, modalNoStrikePos)
	}

	// Alternating between the two constants and two hammer positions must never
	// hand back a stale vector: the constants have their own slots, and the
	// hammer slot refills whenever its position changes.
	positions := []float32{
		0.13, modalResonanceStrikePos, 0.13, modalCouplingStrikePos,
		0.27, modalResonanceStrikePos, 0.13, 0.27, 0.27,
		modalCouplingStrikePos, modalResonanceStrikePos, 0.13,
	}
	for i, pos := range positions {
		assertShapeVector(t, g, g.shapeVector(pos), pos, "alternating")
		if pos != modalResonanceStrikePos && pos != modalCouplingStrikePos && g.hammerShapePos != pos {
			t.Fatalf("step %d: hammer key = %v, want %v", i, g.hammerShapePos, pos)
		}
	}

	// The constant slots must be untouched by all that hammer traffic.
	assertShapeVector(t, g, g.shapeRes, modalResonanceStrikePos, "resonance slot after churn")
	assertShapeVector(t, g, g.shapeCoup, modalCouplingStrikePos, "coupling slot after churn")
}

// TestModalShapeVectorsSurviveArenaBinding guards the layering: the shape
// vectors are group-owned and must not be repointed at the modal arena, which
// compacts only the evolving mode state. It binds real groups through
// modalArena.acquire and checks that the shape slices still point into each
// group's own soaBuf while the mode state aliases the arena, and again after
// release.
func TestModalShapeVectorsSurviveArenaBinding(t *testing.T) {
	withModalArena(t)

	params := NewDefaultParams()
	params.StringModel = StringModelModal
	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)

	notes := []int{48, 60, 72}
	for _, note := range notes {
		sb.SetKeyDown(note, true)
		h.Trigger(note, 100)
	}
	// One block activates the groups, so acquire below has something to compact.
	_ = sb.Process(128, h)

	a := sb.modalArena
	if a == nil {
		t.Fatalf("expected a modal arena for the modal core")
	}
	if a.bound {
		t.Fatalf("arena still bound after Process returned")
	}

	// Prime the hammer slot so all three shape vectors carry real content.
	const hammerPos = float32(0.13)
	before := make(map[int][]float32, len(notes))
	for _, note := range notes {
		g := sb.ModalGroup(note)
		if g == nil {
			t.Fatalf("expected modal group for note %d", note)
		}
		_ = g.shapeVector(hammerPos)
		before[note] = append([]float32(nil), g.shapeRes...)
	}

	checkShapesGroupOwned := func(stage string) {
		t.Helper()
		for _, note := range notes {
			g := sb.ModalGroup(note)
			n := len(g.order)
			if &g.shapeRes[0] != &g.soaBuf[9*n] ||
				&g.shapeCoup[0] != &g.soaBuf[10*n] ||
				&g.shapeHammer[0] != &g.soaBuf[11*n] {
				t.Fatalf("note %d: shape vectors no longer point into the group's own buffer %s",
					note, stage)
			}
			assertShapeVector(t, g, g.shapeRes, modalResonanceStrikePos, "resonance slot "+stage)
			assertShapeVector(t, g, g.shapeCoup, modalCouplingStrikePos, "coupling slot "+stage)
			assertShapeVector(t, g, g.shapeHammer, hammerPos, "hammer slot "+stage)
			for i, want := range before[note] {
				if g.shapeRes[i] != want {
					t.Fatalf("note %d: shapeRes[%d] changed %s", note, i, stage)
				}
			}
		}
	}

	checkShapesGroupOwned("before binding")

	if !a.acquire(sb, notes) {
		t.Fatalf("arena compacted nothing; the binding path was never exercised")
	}
	if !a.bound {
		t.Fatalf("arena reports itself unbound after acquire")
	}

	// While bound the mode state must alias the arena, and the shape vectors
	// must not have moved with it.
	for i, note := range a.notes {
		g := sb.ModalGroup(note)
		lo := int(a.offsets[i])
		if &g.re[0] != &a.re[lo] || &g.im[0] != &a.im[lo] {
			t.Fatalf("note %d: mode state was not repointed at the arena", note)
		}
		if !a.boundNote[note] {
			t.Fatalf("note %d: not marked as bound", note)
		}
	}
	checkShapesGroupOwned("while bound")

	a.release(sb)

	if a.bound {
		t.Fatalf("arena still bound after release")
	}
	for _, note := range notes {
		g := sb.ModalGroup(note)
		n := len(g.order)
		if &g.re[0] != &g.soaBuf[0] || &g.im[0] != &g.soaBuf[n] {
			t.Fatalf("note %d: mode state not restored to the group's own buffer", note)
		}
	}
	checkShapesGroupOwned("after release")
}

// TestModalInjectMatchesDirectShapeMath pins the injection arithmetic to the
// pre-cache expression, operand order included.
func TestModalInjectMatchesDirectShapeMath(t *testing.T) {
	g := newShapeTestGroup(t)

	const force = float32(0.375)
	const strikePos = float32(0.13)
	const modeScale = float32(0.55)

	want := make([]float32, len(g.re))
	scaled := force * g.excitation
	for si := 0; si+1 < len(g.modeStart); si++ {
		sg := g.stringGain(si)
		for idx := int(g.modeStart[si]); idx < int(g.modeStart[si+1]); idx++ {
			shape := modalShape(int(g.order[idx]), strikePos)
			if shape > -1e-6 && shape < 1e-6 {
				continue
			}
			want[idx] = scaled * sg * modeScale * shape / float32(g.order[idx])
		}
	}

	g.injectAtPosition(force, strikePos, modeScale)

	for idx := range want {
		if g.re[idx] != want[idx] {
			t.Fatalf("re[%d] = %v, want %v (bit-exact)", idx, g.re[idx], want[idx])
		}
	}
}
