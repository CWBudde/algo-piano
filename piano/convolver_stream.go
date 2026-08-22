package piano

import (
	dspconv "github.com/cwbudde/algo-dsp/dsp/conv"
)

// This file carries the block plumbing shared by SoundboardConvolver and
// BodyConvolver: how an arbitrary-length Process call is mapped onto the
// fixed-block streaming overlap-add stages underneath.
//
// # The contract
//
// Process is a stream: the output for a given sample must not depend on how the
// caller chopped the input up. Both convolvers used to break that. They sliced
// the input into partSize blocks, zero-padded a trailing short block up to
// partSize, pushed it through StreamingOverlapAddT.ProcessBlockTo (which accepts
// exactly blockSize samples) and kept only the first blockLen outputs. The
// output samples themselves were right — a zero-padded tail cannot corrupt the
// outputs that precede it — but the overlap-add state advanced by a whole
// partition, so the padding zeros were spliced into the stream. Every later
// sample was then the convolution of the wrong signal. Measured against a direct
// convolution with a 512-tap IR, streaming a 1000-sample sine in fixed chunks:
// 128 gave 1.19e-06 (float32 round-off), 100 gave 5.56, 64 gave 1.93, 1 gave
// 1.40.
//
// # Latency
//
// Process(n) returns n samples with no added latency, and that is pinned by
// radiation_test.go, which places the body and room contributions at the exact
// sum of two pure-delay IRs. The fixed-block stage cannot honour that on its
// own: it emits nothing until a full partition has arrived. So the samples of a
// partition that the caller asks for before that partition is complete are
// computed a second way, in the time domain, and the fixed-block stage is only
// ever fed complete partitions.
//
// Writing the convolution of the input x with the IR h at a sample base+i of the
// partition starting at base (0 <= i < partSize):
//
//	y[base+i] = sum_{j<partSize} h[j]·x[base+i-j]   (the "head")
//	          + sum_{j>=partSize} h[j]·x[base+i-j]  (the "tail")
//
// The head reaches back at most partSize-1 samples, all of them known, and costs
// partSize multiply-accumulates per sample — it is evaluated directly. The tail
// only reads input from before base, i.e. only committed samples, so a second
// overlap-add stage carrying the kernel h[partSize:] can produce the whole
// partition's worth of it as soon as the *previous* partition is committed. That
// is the classic zero-latency partitioned-convolution split; the cost of an
// off-grid call is O(partSize) per sample plus one extra FFT per partition,
// rather than the O(len(IR)) per sample a plain direct convolution would cost.
//
// The tail stage is built lazily, on the first off-grid call, and primed by
// replaying the retained input history through it on the same partition grid the
// main stage saw. A caller that stays on the partSize grid — cmd/piano-render,
// the fit render path and the AudioWorklet render quantum all do — never
// allocates it and pays nothing beyond a memcpy of the input into the history
// ring.
//
// # Bit-identity
//
// When the pending buffer is empty and a whole partition is available, the block
// goes straight through the main stage exactly as before. assets/thresholds/c4.json
// and every tracked report are calibrated on partSize=128 with 128-frame calls,
// so that path is deliberately left untouched and byte-for-byte identical.
// Samples emitted through the head/tail split differ from the single-FFT path by
// float32 round-off (order 1e-6 relative), not by more.

// olaLane is one output channel of a partitioned convolution: the fixed-block
// overlap-add stage over the whole IR, plus the pieces the off-grid path needs.
type olaLane struct {
	ir       []float32
	partSize int

	ola   *dspconv.StreamingOverlapAddT[float32, complex64]
	block []float32 // partSize scratch for one overlap-add block

	// tail carries h[partSize:]. Fed the same committed partitions as ola, it
	// runs one partition ahead of the output: after being fed partition N-1,
	// tailOut holds the contribution of all input before partition N to
	// partition N's output. It stays nil (and tailOut all-zero) while the IR
	// fits in a single partition, or until the first off-grid call.
	tail    *dspconv.StreamingOverlapAddT[float32, complex64]
	tailOut []float32

	out []float32 // reusable mono output for the current Process call
}

// newOLALane builds a lane for kernel ir at the given partition size.
func newOLALane(ir []float32, partSize int) (*olaLane, error) {
	ola, err := dspconv.NewStreamingOverlapAdd32(ir, partSize)
	if err != nil {
		return nil, err
	}
	kernel := make([]float32, len(ir))
	copy(kernel, ir)
	return &olaLane{
		ir:       kernel,
		partSize: partSize,
		ola:      ola,
		block:    make([]float32, partSize),
		tailOut:  make([]float32, partSize),
	}, nil
}

// primeLen is the amount of committed input history the tail stage needs to be
// brought in sync, rounded up to a whole number of partitions. The tail kernel
// has len(ir)-partSize taps and its output for a partition reaches back
// partSize+len(ir)-partSize-1 = len(ir)-1 samples.
func (l *olaLane) primeLen() int {
	if len(l.ir) <= l.partSize {
		return 0
	}
	return ((len(l.ir) - 1 + l.partSize - 1) / l.partSize) * l.partSize
}

// ensureOut resizes the per-call mono output buffer without reallocating when
// the existing one is large enough.
func (l *olaLane) ensureOut(n int) {
	if cap(l.out) < n {
		l.out = make([]float32, n)
		return
	}
	l.out = l.out[:n]
}

// commit pushes one complete partition through the lane and returns the stage's
// output block. ok is false when the stage rejected the block, in which case the
// caller falls back to passing the input through, as it always has.
func (l *olaLane) commit(block []float32) (out []float32, ok bool) {
	if err := l.ola.ProcessBlockTo(l.block, block); err != nil {
		return nil, false
	}
	if l.tail != nil {
		if err := l.tail.ProcessBlockTo(l.tailOut, block); err != nil {
			// Drop back to evaluating the whole IR directly rather than
			// silently losing the tail contribution.
			l.tail = nil
			clear(l.tailOut)
		}
	}
	return l.block, true
}

// ensureTail builds and primes the h[partSize:] stage. hist holds the most
// recent input samples, oldest first, zero-filled before the start of the
// stream; committedEnd is the index in hist one past the last committed sample,
// and is a partition boundary of the stream.
func (l *olaLane) ensureTail(hist []float32, committedEnd int) {
	if l.tail != nil || len(l.ir) <= l.partSize {
		return
	}
	tail, err := dspconv.NewStreamingOverlapAdd32(l.ir[l.partSize:], l.partSize)
	if err != nil {
		return // emitDirect falls back to the full IR.
	}
	start := committedEnd - l.primeLen()
	if start < 0 {
		start = 0
	}
	for off := start; off+l.partSize <= committedEnd; off += l.partSize {
		if err := tail.ProcessBlockTo(l.tailOut, hist[off:off+l.partSize]); err != nil {
			clear(l.tailOut)
			return
		}
	}
	l.tail = tail
}

// emitDirect writes the output for the take most recent input samples, which end
// at hist[end-1] and sit at offset blockOff within the current partition.
//
// With a primed tail stage this evaluates only the first partSize taps and adds
// the stage's contribution; without one — IR shorter than a partition, or a tail
// stage that could not be built — it evaluates the whole IR, which hist is
// always long enough to cover. The accumulator is float64 so a long IR does not
// lose to float32 summation error what the FFT path keeps.
func (l *olaLane) emitDirect(hist []float32, end, take, blockOff int, dst []float32) {
	h := l.ir
	if l.tail != nil && len(h) > l.partSize {
		h = h[:l.partSize]
	}
	for k := range take {
		w := end - take + k
		var acc float64
		for j := 0; j < len(h) && w-j >= 0; j++ {
			acc += float64(h[j]) * float64(hist[w-j])
		}
		dst[k] = float32(acc) + l.tailOut[blockOff+k]
	}
}

// reset clears the lane's overlap state.
func (l *olaLane) reset() {
	if l.ola != nil {
		l.ola.Reset()
	}
	if l.tail != nil {
		l.tail.Reset()
	}
	clear(l.tailOut)
}

// blockStream owns the input-side state shared by every lane of one convolver:
// the samples of the current, still incomplete partition and the input history
// the off-grid path reads.
type blockStream struct {
	partSize int
	pending  []float32
	hist     []float32
}

// configure sizes the stream for a set of lanes. histLen covers the longest
// lane's priming window plus one partition, which is also enough for the
// partSize-1 samples the head reaches back over and for a full-IR fallback.
func (s *blockStream) configure(partSize int, lanes ...*olaLane) {
	s.partSize = partSize
	histLen := partSize
	for _, l := range lanes {
		if n := l.primeLen() + partSize; n > histLen {
			histLen = n
		}
	}
	s.pending = make([]float32, 0, partSize)
	s.hist = make([]float32, histLen)
}

// reset drops the partial partition and the input history.
func (s *blockStream) reset() {
	s.pending = s.pending[:0]
	clear(s.hist)
}

// pushHist appends consumed input to the history ring.
func (s *blockStream) pushHist(x []float32) {
	if len(s.hist) == 0 {
		return
	}
	if len(x) >= len(s.hist) {
		copy(s.hist, x[len(x)-len(s.hist):])
		return
	}
	copy(s.hist, s.hist[len(x):])
	copy(s.hist[len(s.hist)-len(x):], x)
}

// run convolves input through every lane, leaving each lane's mono result in
// lane.out. See the file comment for why the two paths exist.
func (s *blockStream) run(input []float32, lanes ...*olaLane) {
	p := s.partSize
	for _, l := range lanes {
		l.ensureOut(len(input))
	}

	for i := 0; i < len(input); {
		// Fast path: a whole partition, on the grid. Bit-identical to the
		// pre-fix code and to every 128-frame render.
		if len(s.pending) == 0 && len(input)-i >= p {
			s.pushHist(input[i : i+p])
			for _, l := range lanes {
				if out, ok := l.commit(input[i : i+p]); ok {
					copy(l.out[i:i+p], out)
				} else {
					copy(l.out[i:i+p], input[i:i+p])
				}
			}
			i += p
			continue
		}

		blockOff := len(s.pending)
		take := p - blockOff
		if take > len(input)-i {
			take = len(input) - i
		}
		s.pending = append(s.pending, input[i:i+take]...)
		s.pushHist(input[i : i+take])

		if len(s.pending) == p {
			// The chunk completed the partition, so the stage output for
			// these samples is exact and cheaper than the split.
			for _, l := range lanes {
				if out, ok := l.commit(s.pending); ok {
					copy(l.out[i:i+take], out[p-take:])
				} else {
					copy(l.out[i:i+take], input[i:i+take])
				}
			}
			s.pending = s.pending[:0]
		} else {
			committedEnd := len(s.hist) - len(s.pending)
			for _, l := range lanes {
				l.ensureTail(s.hist, committedEnd)
				l.emitDirect(s.hist, len(s.hist), take, blockOff, l.out[i:i+take])
			}
		}
		i += take
	}
}
