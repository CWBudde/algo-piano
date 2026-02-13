package piano

import "math"

// HammerModel defines the interface for hammer models.
type HammerModel interface {
	ComputeForce(velocity float32, stringVelocity float32) float32
}

// Hammer is a nonlinear felt-hammer contact model with bounded contact duration.
type Hammer struct {
	sampleRate float32
	mass       float32
	stiffness  float32
	exponent   float32
	damping    float32
	baseStiff  float32
	baseExp    float32

	contactMaxSamples int
	contactMinSamples int
	contactSamples    int
	inContact         bool

	pos float32
	vel float32
}

// NewHammer creates a hammer initialized from MIDI velocity.
func NewHammer(sampleRate int, velocity int) *Hammer {
	if velocity < 1 {
		velocity = 1
	}
	if velocity > 127 {
		velocity = 127
	}
	v := float32(velocity) / 127.0
	initialVel := 0.6 + 3.0*v
	stiffness := float32(1.1e6) * (0.5 + 2.5*v)
	exponent := float32(2.3)

	return &Hammer{
		sampleRate:        float32(sampleRate),
		mass:              0.010,
		stiffness:         stiffness,
		exponent:          exponent,
		damping:           0.10 + 0.20*v,
		baseStiff:         stiffness,
		baseExp:           exponent,
		contactMaxSamples: int(float32(sampleRate) * (0.0040 - 0.0030*v)),
		contactMinSamples: int(float32(sampleRate) * 0.00025),
		inContact:         true,
		pos:               0.00012,
		vel:               initialVel,
	}
}

// SetHardnessScale scales felt hardness around base values.
func (h *Hammer) SetHardnessScale(scale float32) {
	if scale < 0.5 {
		scale = 0.5
	}
	if scale > 1.2 {
		scale = 1.2
	}
	h.stiffness = h.baseStiff * scale
	h.exponent = h.baseExp * (0.90 + 0.10*scale)
}

// InContact reports whether the hammer is still in contact with the string.
func (h *Hammer) InContact() bool {
	return h.inContact
}

// Step advances the nonlinear contact model and returns contact force.
func (h *Hammer) Step(stringDisp float32) float32 {
	if !h.inContact {
		return 0
	}

	dt := 1.0 / h.sampleRate
	indentation := h.pos - stringDisp
	relativeVel := h.vel

	force := float32(0.0)
	if indentation > 0 {
		indPow := float32(math.Pow(float64(indentation), float64(h.exponent)))
		dissipation := 1.0 + h.damping*maxf(relativeVel, 0.0)
		force = h.stiffness * indPow * dissipation
	}

	if !isFinite(force) {
		h.inContact = false
		return 0
	}

	acc := -force / h.mass
	h.vel += acc * dt
	h.pos += h.vel * dt

	h.contactSamples++
	if h.contactSamples >= h.contactMaxSamples {
		h.inContact = false
	}
	if h.contactSamples > h.contactMinSamples && indentation <= 0 && h.vel <= 0 {
		h.inContact = false
	}

	return force
}

// ComputeForce implements HammerModel with a simplified static contact law.
func (h *Hammer) ComputeForce(velocity float32, stringVelocity float32) float32 {
	indentation := maxf(velocity-stringVelocity, 0)
	return h.stiffness * float32(math.Pow(float64(indentation), float64(h.exponent)))
}
