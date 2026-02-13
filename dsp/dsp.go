package dsp

import "math"

// Biquad implements a second-order IIR filter (no heap allocations in Process)
type Biquad struct {
	// Coefficients
	b0, b1, b2 float32
	a1, a2     float32

	// State (previous samples)
	x1, x2 float32 // input history
	y1, y2 float32 // output history
}

// NewBiquad creates a new biquad filter with the given coefficients
func NewBiquad(b0, b1, b2, a1, a2 float32) *Biquad {
	return &Biquad{
		b0: b0,
		b1: b1,
		b2: b2,
		a1: a1,
		a2: a2,
	}
}

// Process processes one sample through the biquad filter
func (b *Biquad) Process(input float32) float32 {
	// Direct Form I implementation
	output := b.b0*input + b.b1*b.x1 + b.b2*b.x2 - b.a1*b.y1 - b.a2*b.y2

	// Update state
	b.x2 = b.x1
	b.x1 = input
	b.y2 = b.y1
	b.y1 = output

	return output
}

// Reset clears the filter state
func (b *Biquad) Reset() {
	b.x1, b.x2 = 0, 0
	b.y1, b.y2 = 0, 0
}

// NewLowpass creates a simple lowpass biquad filter
func NewLowpass(cutoff, sampleRate, q float32) *Biquad {
	w0 := 2.0 * math.Pi * float64(cutoff) / float64(sampleRate)
	alpha := math.Sin(w0) / (2.0 * float64(q))
	cosw0 := math.Cos(w0)

	b0 := (1.0 - cosw0) / 2.0
	b1 := 1.0 - cosw0
	b2 := (1.0 - cosw0) / 2.0
	a0 := 1.0 + alpha
	a1 := -2.0 * cosw0
	a2 := 1.0 - alpha

	// Normalize by a0
	return NewBiquad(
		float32(b0/a0),
		float32(b1/a0),
		float32(b2/a0),
		float32(a1/a0),
		float32(a2/a0),
	)
}

// DelayLine implements a circular buffer for delay
type DelayLine struct {
	buffer   []float32
	writePos int
	size     int
}

// NewDelayLine creates a new delay line with the given size
func NewDelayLine(size int) *DelayLine {
	return &DelayLine{
		buffer: make([]float32, size),
		size:   size,
	}
}

// Write writes a sample to the delay line
func (d *DelayLine) Write(sample float32) {
	d.buffer[d.writePos] = sample
	d.writePos = (d.writePos + 1) % d.size
}

// Read reads a sample from the delay line at the given delay (in samples)
func (d *DelayLine) Read(delay int) float32 {
	readPos := (d.writePos - delay + d.size) % d.size
	return d.buffer[readPos]
}

// ReadFractional reads with fractional delay using linear interpolation
func (d *DelayLine) ReadFractional(delay float32) float32 {
	intDelay := int(delay)
	frac := delay - float32(intDelay)

	sample1 := d.Read(intDelay)
	sample2 := d.Read(intDelay + 1)

	// Linear interpolation
	return sample1 + frac*(sample2-sample1)
}

// Reset clears the delay line
func (d *DelayLine) Reset() {
	for i := range d.buffer {
		d.buffer[i] = 0
	}
	d.writePos = 0
}

// LagrangeInterpolator provides higher-order fractional delay interpolation
type LagrangeInterpolator struct {
	order int
}

// NewLagrangeInterpolator creates a new Lagrange interpolator
// order: 1 = linear, 3 = cubic
func NewLagrangeInterpolator(order int) *LagrangeInterpolator {
	return &LagrangeInterpolator{
		order: order,
	}
}

// Interpolate performs Lagrange interpolation
// samples: array of samples around the interpolation point
// frac: fractional position (0.0 to 1.0)
func (l *LagrangeInterpolator) Interpolate(samples []float32, frac float32) float32 {
	if l.order == 1 {
		// Linear interpolation
		return samples[0] + frac*(samples[1]-samples[0])
	}

	if l.order == 3 {
		// Cubic (3rd order) Lagrange interpolation
		// Requires 4 points: samples[0], samples[1], samples[2], samples[3]
		// Interpolating between samples[1] and samples[2]
		d := frac
		c0 := samples[1]
		c1 := samples[2] - samples[0]/3.0 - samples[1]/2.0 - samples[3]/6.0
		c2 := samples[0]/2.0 - samples[1] + samples[2]/2.0
		c3 := samples[1]/2.0 - samples[2]/2.0 + (samples[3]-samples[0])/6.0

		return c0 + d*(c1+d*(c2+d*c3))
	}

	// Fallback to linear
	return samples[0] + frac*(samples[1]-samples[0])
}

// FlushDenormals converts denormal numbers to zero to avoid performance issues
func FlushDenormals(x float32) float32 {
	const epsilon = 1e-30
	if x > -epsilon && x < epsilon {
		return 0.0
	}
	return x
}
