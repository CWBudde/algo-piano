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

	h.active[note] = append(h.active[note], &hammerStrike{
		note:      note,
		strikePos: strikePos,
		hammer:    hammer,
	})
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
			if ev.hammer.InContact() {
				contactForce := ev.hammer.Step(0)
				if contactForce != 0 {
					bank.InjectHammerForce(note, contactForce*0.002, ev.strikePos)
				}
			}
			if ev.hammer.InContact() {
				keep = append(keep, ev)
			}
		}
		h.active[note] = keep
	}
}
