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
// vectors are group-owned and must not be repointed at the modal arena, whose
// restoreOwnSlices only rebuilds the evolving mode state.
func TestModalShapeVectorsSurviveArenaBinding(t *testing.T) {
	g := newShapeTestGroup(t)
	_ = g.shapeVector(0.13)

	before := append([]float32(nil), g.shapeRes...)
	g.restoreOwnSlices()

	assertShapeVector(t, g, g.shapeRes, modalResonanceStrikePos, "resonance slot after restore")
	assertShapeVector(t, g, g.shapeCoup, modalCouplingStrikePos, "coupling slot after restore")
	assertShapeVector(t, g, g.shapeHammer, 0.13, "hammer slot after restore")
	for i := range before {
		if g.shapeRes[i] != before[i] {
			t.Fatalf("restoreOwnSlices clobbered shapeRes[%d]", i)
		}
	}
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
