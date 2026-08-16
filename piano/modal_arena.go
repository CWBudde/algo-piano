package piano

import vecmath "github.com/cwbudde/algo-vecmath"

// modalArena compacts the mode state of every currently active modal group
// into one contiguous struct-of-arrays region, so a whole block can be advanced
// with a single vectorized kernel call per sample instead of one call per note.
//
// Why this exists: a single note's bank holds only ~24 modes, which is three
// AVX2 iterations. At that width the per-call cost (seven slice headers, bounds
// checks, dispatch indirection) outweighs the vectorization, and measurements
// showed the per-group call was ~10% slower than the original scalar
// array-of-structs loop at high polyphony. Compacting ~86 active notes into one
// call of ~1500 modes amortizes that overhead across two orders of magnitude
// more work.
//
// Groups keep owning their state between blocks. On acquire, each active
// group's arrays are copied into the arena and the group's slice headers are
// repointed at it, so all existing per-sample code (excitation, crossfeed)
// operates on the arena without knowing it exists. On release, the evolving
// state is copied back and the headers are restored.
type modalArena struct {
	re    []float32
	im    []float32
	cosW  []float32
	sinW  []float32
	decay []float32
	gain  []float32
	acc   []float32
	buf   []float32

	// notes holds the compacted notes in arena order; offsets[i] is where
	// notes[i] starts. layoutNotes mirrors notes and is used to detect when the
	// active set changed and the static arrays need refreshing.
	notes       []int
	offsets     []int32
	layoutNotes []int

	// boundNote reports, per MIDI note, whether that group is currently
	// repointed at the arena. Notes activated mid-block are not compacted and
	// must fall back to the per-group kernel.
	boundNote [128]bool

	used  int
	bound bool
}

// newModalArena pre-sizes the arena to the largest mode count the bank can ever
// need, so no per-block growth (and therefore no per-block allocation) is
// possible later.
func newModalArena(groups []*ModalStringGroup, maxNotes int) *modalArena {
	total := 0
	for _, g := range groups {
		if g != nil {
			total += len(g.order)
		}
	}
	if total == 0 {
		return nil
	}

	a := &modalArena{
		buf:         make([]float32, modalArenaFieldCount*total),
		notes:       make([]int, 0, maxNotes),
		offsets:     make([]int32, 0, maxNotes),
		layoutNotes: make([]int, 0, maxNotes),
	}
	a.re = a.buf[0*total : 1*total : 1*total]
	a.im = a.buf[1*total : 2*total : 2*total]
	a.cosW = a.buf[2*total : 3*total : 3*total]
	a.sinW = a.buf[3*total : 4*total : 4*total]
	a.decay = a.buf[4*total : 5*total : 5*total]
	a.gain = a.buf[5*total : 6*total : 6*total]
	a.acc = a.buf[6*total : 7*total : 7*total]
	return a
}

// modalArenaFieldCount is the number of parallel float32 arrays carved out of
// the arena's single backing allocation.
const modalArenaFieldCount = 7

// acquire compacts the given notes and repoints their groups at the arena.
// It reports whether anything was compacted.
func (a *modalArena) acquire(sb *StringBank, notes []int) bool {
	if a == nil || a.bound {
		return false
	}

	a.notes = a.notes[:0]
	a.offsets = a.offsets[:0]
	off := 0
	for _, note := range notes {
		g := sb.modalGroups[note]
		if g == nil || len(g.order) == 0 || !g.isActive() {
			continue
		}
		a.notes = append(a.notes, note)
		a.offsets = append(a.offsets, int32(off))
		off += len(g.order)
	}
	a.used = off
	if a.used == 0 {
		return false
	}

	// The static per-mode coefficients only change when the compacted layout
	// changes, so refresh them only then. decay is excluded: it is rewritten by
	// every damper transition, which can happen between any two blocks.
	layoutChanged := a.layoutChanged()
	if layoutChanged {
		a.layoutNotes = append(a.layoutNotes[:0], a.notes...)
	}

	for i, note := range a.notes {
		g := sb.modalGroups[note]
		lo := int(a.offsets[i])
		hi := lo + len(g.order)

		copy(a.re[lo:hi], g.re)
		copy(a.im[lo:hi], g.im)
		copy(a.decay[lo:hi], g.decay)
		if layoutChanged {
			copy(a.cosW[lo:hi], g.cosW)
			copy(a.sinW[lo:hi], g.sinW)
			copy(a.gain[lo:hi], g.gain)
		}

		g.re = a.re[lo:hi:hi]
		g.im = a.im[lo:hi:hi]
		g.cosW = a.cosW[lo:hi:hi]
		g.sinW = a.sinW[lo:hi:hi]
		g.decay = a.decay[lo:hi:hi]
		g.gain = a.gain[lo:hi:hi]
		g.acc = a.acc[lo:hi:hi]
		a.boundNote[note] = true
	}

	a.bound = true
	return true
}

// layoutChanged reports whether the set of compacted notes differs from the one
// the static arrays were last filled for.
func (a *modalArena) layoutChanged() bool {
	if len(a.layoutNotes) != len(a.notes) {
		return true
	}
	for i := range a.notes {
		if a.layoutNotes[i] != a.notes[i] {
			return true
		}
	}
	return false
}

// release copies the evolving state back to the groups and restores their own
// slice headers.
func (a *modalArena) release(sb *StringBank) {
	if a == nil || !a.bound {
		return
	}
	for i, note := range a.notes {
		g := sb.modalGroups[note]
		if g == nil {
			continue
		}
		lo := int(a.offsets[i])
		hi := lo + len(g.order)

		g.restoreOwnSlices()
		copy(g.re, a.re[lo:hi])
		copy(g.im, a.im[lo:hi])
		a.boundNote[note] = false
	}
	a.bound = false
}

// advance rotates and decays every compacted mode by one sample, accumulating
// the gain-weighted real part into acc. This is the call the whole arena exists
// to make: one kernel invocation covering every active note.
func (a *modalArena) advance() {
	n := a.used
	vecmath.RotateDecayAccumulateF32(
		a.acc[:n], a.re[:n], a.im[:n],
		a.cosW[:n], a.sinW[:n], a.decay[:n], a.gain[:n],
	)
}

// restoreOwnSlices repoints the group's mode arrays back at its own backing
// buffer after an arena binding.
func (g *ModalStringGroup) restoreOwnSlices() {
	n := len(g.order)
	b := g.soaBuf
	g.re = b[0*n : 1*n : 1*n]
	g.im = b[1*n : 2*n : 2*n]
	g.cosW = b[2*n : 3*n : 3*n]
	g.sinW = b[3*n : 4*n : 4*n]
	g.decay = b[4*n : 5*n : 5*n]
	g.decayUndamped = b[5*n : 6*n : 6*n]
	g.decayDamped = b[6*n : 7*n : 7*n]
	g.gain = b[7*n : 8*n : 8*n]
	g.acc = b[8*n : 9*n : 9*n]
}
