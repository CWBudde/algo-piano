package piano

type keyStateTracker struct {
	keyDown      [128]bool
	lastVelocity [128]int
}

func newKeyStateTracker() *keyStateTracker {
	return &keyStateTracker{}
}

func (k *keyStateTracker) NoteOn(note int, velocity int) {
	if note < 0 || note > 127 {
		return
	}
	k.keyDown[note] = true
	k.lastVelocity[note] = velocity
}

func (k *keyStateTracker) NoteOff(note int) {
	if note < 0 || note > 127 {
		return
	}
	k.keyDown[note] = false
}

type hammerStrike struct {
	note      int
	strikePos float32
	hammer    *Hammer

	// Attack noise state.
	noiseRemaining int     // samples left in noise burst
	noiseDecay     float32 // per-sample exponential decay factor
	noiseLevel     float32 // current noise amplitude
	noiseFilterZ   float32 // one-pole lowpass state for spectral coloring
	noiseFilterA   float32 // one-pole coefficient (0..1, higher = more LPF)
	noiseRNG       uint32  // xorshift32 state
}

// HammerExciter manages short-lived nonlinear hammer contact events.
type HammerExciter struct {
	sampleRate int
	params     *Params
	softPedal  bool
	active     [128][]*hammerStrike
}

func NewHammerExciter(sampleRate int, params *Params) *HammerExciter {
	return &HammerExciter{
		sampleRate: sampleRate,
		params:     params,
	}
}

func (h *HammerExciter) SetSoftPedal(down bool) {
	h.softPedal = down
}

func (h *HammerExciter) Trigger(note int, velocity int) {
	if note < 0 || note > 127 {
		return
	}
	strikePos := float32(0.18)
	softStrikeOffset := float32(0.08)
	softHardness := float32(0.78)

	if h.params != nil {
		if h.params.SoftPedalStrikeOffset >= 0 {
			softStrikeOffset = h.params.SoftPedalStrikeOffset
		}
		if h.params.SoftPedalHardness > 0 {
			softHardness = h.params.SoftPedalHardness
		}
		if np, ok := h.params.PerNote[note]; ok && np != nil {
			if np.StrikePosition > 0.0 && np.StrikePosition < 1.0 {
				strikePos = np.StrikePosition
			}
		}
	}

	hammer := NewHammer(h.sampleRate, velocity)
	if h.params != nil && hammer != nil {
		hammer.ApplyInfluenceScales(
			h.params.HammerStiffnessScale,
			h.params.HammerExponentScale,
			h.params.HammerDampingScale,
			h.params.HammerInitialVelocityScale,
			h.params.HammerContactTimeScale,
		)
	}

	if h.softPedal {
		strikePos = minf(strikePos+softStrikeOffset, 0.95)
		if hammer != nil {
			hammer.SetHardnessScale(softHardness)
		}
	}

	strike := &hammerStrike{
		note:      note,
		strikePos: strikePos,
		hammer:    hammer,
	}

	// Initialize attack noise burst if enabled.
	if h.params != nil && h.params.AttackNoiseLevel > 0 && h.params.AttackNoiseDurationMs > 0 {
		durMs := h.params.AttackNoiseDurationMs
		if durMs > 20 {
			durMs = 20
		}
		noiseSamples := int(durMs * 0.001 * float32(h.sampleRate))
		if noiseSamples < 1 {
			noiseSamples = 1
		}
		// Exponential decay: reach -60 dB at end of burst.
		strike.noiseRemaining = noiseSamples
		strike.noiseLevel = h.params.AttackNoiseLevel * (float32(velocity) / 127.0)
		strike.noiseDecay = expDecayPerSample(60.0, noiseSamples)

		// One-pole lowpass for spectral coloring.
		// AttackNoiseColor is dB/octave tilt (negative = darker).
		// Map to a simple lowpass coefficient: -6 dB/oct corresponds
		// to a cutoff around fs/4; more negative = lower cutoff.
		color := h.params.AttackNoiseColor
		if color >= 0 {
			strike.noiseFilterA = 0 // white noise, no filtering
		} else {
			// Approximate: map color to cutoff fraction of Nyquist.
			// -3 dB/oct → ~0.7 Nyquist, -6 → ~0.35, -12 → ~0.1
			frac := clampf(1.0+color/18.0, 0.02, 1.0)
			// Bilinear-approximated one-pole: a = exp(-2*pi*fc/fs)
			// fc = frac * fs/2
			strike.noiseFilterA = expf(-3.14159 * frac)
		}

		// Seed PRNG from note + velocity for reproducibility but variation.
		strike.noiseRNG = uint32(note)*2654435761 + uint32(velocity)*1597334677 + 1
	}

	h.active[note] = append(h.active[note], strike)
}

// ProcessSample advances active hammer events by one sample and injects force into the string bank.
func (h *HammerExciter) ProcessSample(bank *StringBank) {
	if h == nil || bank == nil {
		return
	}
	for note := 0; note < len(h.active); note++ {
		events := h.active[note]
		if len(events) == 0 {
			continue
		}
		keep := events[:0]
		for _, ev := range events {
			if ev == nil || ev.hammer == nil {
				continue
			}
			alive := false
			if ev.hammer.InContact() {
				contactForce := ev.hammer.Step(0)
				if contactForce != 0 {
					bank.InjectHammerForce(note, contactForce*0.2, ev.strikePos)
				}
				alive = ev.hammer.InContact()
			}
			// Attack noise burst (runs independently of hammer contact).
			if ev.noiseRemaining > 0 {
				n := xorshift32(&ev.noiseRNG)
				white := float32(n)*2.3283064e-10*2.0 - 1.0 // uniform [-1, 1]
				// Apply one-pole lowpass for spectral coloring.
				if ev.noiseFilterA > 0 {
					ev.noiseFilterZ = ev.noiseFilterA*ev.noiseFilterZ + (1.0-ev.noiseFilterA)*white
					white = ev.noiseFilterZ
				}
				noiseForce := white * ev.noiseLevel
				bank.InjectHammerForce(note, noiseForce, ev.strikePos)
				ev.noiseLevel *= ev.noiseDecay
				ev.noiseRemaining--
				alive = true
			}
			if alive {
				keep = append(keep, ev)
			}
		}
		h.active[note] = keep
	}
}
