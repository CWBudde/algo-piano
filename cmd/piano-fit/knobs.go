package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
)

type knobDef struct {
	Name  string
	Min   float64
	Max   float64
	IsInt bool
}

type candidate struct {
	Vals []float64
}

type irConfigs struct {
	body irsynth.BodyConfig
	room irsynth.RoomConfig
}

// parseOptimizeGroups parses a comma-separated string of group names.
// Valid groups: piano, body-ir, room-ir, mix.
func parseOptimizeGroups(raw string) (map[string]bool, error) {
	valid := map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true}
	groups := make(map[string]bool)
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !valid[s] {
			return nil, fmt.Errorf("unknown optimize group %q (valid: piano, body-ir, room-ir, mix)", s)
		}
		groups[s] = true
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("no optimize groups specified")
	}
	return groups, nil
}

// needsIRSynthesis returns true if body-ir or room-ir is in the active groups.
func needsIRSynthesis(groups map[string]bool) bool {
	return groups["body-ir"] || groups["room-ir"]
}

func initCandidate(
	base *piano.Params,
	sampleRate int,
	note int,
	baseVelocity int,
	baseReleaseAfter float64,
	groups map[string]bool,
) ([]knobDef, candidate) {
	bodyCfg := irsynth.DefaultBodyConfig()
	bodyCfg.SampleRate = sampleRate
	roomCfg := irsynth.DefaultRoomConfig()
	roomCfg.SampleRate = sampleRate

	np := base.PerNote[note]
	if np == nil {
		np = &piano.NoteParams{Loss: 0.9990, Inharmonicity: 0.12, StrikePosition: 0.18}
	}

	defs := make([]knobDef, 0, 32)
	vals := make([]float64, 0, 32)
	addKnob := func(def knobDef, val float64) {
		for _, d := range defs {
			if d.Name == def.Name {
				return
			}
		}
		defs = append(defs, def)
		vals = append(vals, val)
	}

	// Piano group knobs.
	if groups["piano"] {
		addKnob(knobDef{Name: "output_gain", Min: 0.4, Max: 1.8}, float64(base.OutputGain))
		addKnob(knobDef{Name: "hammer_stiffness_scale", Min: 0.6, Max: 1.8}, float64(base.HammerStiffnessScale))
		addKnob(knobDef{Name: "hammer_exponent_scale", Min: 0.8, Max: 1.2}, float64(base.HammerExponentScale))
		addKnob(knobDef{Name: "hammer_damping_scale", Min: 0.6, Max: 1.8}, float64(base.HammerDampingScale))
		addKnob(knobDef{Name: "hammer_initial_velocity_scale", Min: 0.7, Max: 1.4}, float64(base.HammerInitialVelocityScale))
		addKnob(knobDef{Name: "hammer_contact_time_scale", Min: 0.7, Max: 1.6}, float64(base.HammerContactTimeScale))
		addKnob(knobDef{Name: "unison_detune_scale", Min: 0.0, Max: 2.0}, float64(base.UnisonDetuneScale))
		addKnob(knobDef{Name: "unison_crossfeed", Min: 0.0, Max: 0.005}, float64(base.UnisonCrossfeed))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.loss", note), Min: 0.985, Max: 0.99995}, float64(np.Loss))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.inharmonicity", note), Min: 0.0, Max: 0.6}, float64(np.Inharmonicity))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.strike_position", note), Min: 0.08, Max: 0.45}, float64(np.StrikePosition))
		addKnob(knobDef{Name: "render.velocity", Min: 40, Max: 127, IsInt: true}, float64(baseVelocity))
		addKnob(knobDef{Name: "render.release_after", Min: 0.2, Max: 3.5}, baseReleaseAfter)
	}

	// Body IR group knobs.
	if groups["body-ir"] {
		addKnob(knobDef{Name: "body_modes", Min: 8, Max: 96, IsInt: true}, float64(bodyCfg.Modes))
		addKnob(knobDef{Name: "body_brightness", Min: 0.5, Max: 2.5}, bodyCfg.Brightness)
		addKnob(knobDef{Name: "body_density", Min: 0.5, Max: 4.0}, bodyCfg.Density)
		addKnob(knobDef{Name: "body_direct", Min: 0.1, Max: 1.2}, bodyCfg.DirectLevel)
		addKnob(knobDef{Name: "body_decay", Min: 0.01, Max: 0.5}, bodyCfg.DecayS)
		addKnob(knobDef{Name: "body_duration", Min: 0.02, Max: 0.3}, bodyCfg.DurationS)
	}

	// Room IR group knobs.
	if groups["room-ir"] {
		addKnob(knobDef{Name: "room_early", Min: 0, Max: 64, IsInt: true}, float64(roomCfg.EarlyCount))
		addKnob(knobDef{Name: "room_late", Min: 0.0, Max: 0.15}, roomCfg.LateLevel)
		addKnob(knobDef{Name: "room_stereo_width", Min: 0.0, Max: 1.0}, roomCfg.StereoWidth)
		addKnob(knobDef{Name: "room_brightness", Min: 0.3, Max: 2.0}, roomCfg.Brightness)
		addKnob(knobDef{Name: "room_low_decay", Min: 0.3, Max: 3.0}, roomCfg.LowDecayS)
		addKnob(knobDef{Name: "room_high_decay", Min: 0.05, Max: 0.8}, roomCfg.HighDecayS)
		addKnob(knobDef{Name: "room_duration", Min: 0.3, Max: 2.0}, roomCfg.DurationS)
	}

	// Mix group knobs: dual-IR vs legacy mode.
	if groups["mix"] {
		dualIR := needsIRSynthesis(groups) || base.BodyIRWavPath != "" || base.RoomIRWavPath != ""
		if dualIR {
			addKnob(knobDef{Name: "body_dry", Min: 0.2, Max: 1.5}, float64(base.BodyDryMix))
			addKnob(knobDef{Name: "body_gain", Min: 0.3, Max: 2.0}, float64(base.BodyIRGain))
			addKnob(knobDef{Name: "room_wet", Min: 0.0, Max: 1.0}, float64(base.RoomWetMix))
			addKnob(knobDef{Name: "room_gain", Min: 0.3, Max: 2.0}, float64(base.RoomGain))
		} else {
			addKnob(knobDef{Name: "ir_wet_mix", Min: 0.2, Max: 1.6}, float64(base.IRWetMix))
			addKnob(knobDef{Name: "ir_dry_mix", Min: 0.0, Max: 0.8}, float64(base.IRDryMix))
			addKnob(knobDef{Name: "ir_gain", Min: 0.4, Max: 2.2}, float64(base.IRGain))
		}
	}

	for i := range vals {
		vals[i] = clamp(vals[i], defs[i].Min, defs[i].Max)
		if defs[i].IsInt {
			vals[i] = math.Round(vals[i])
		}
	}
	return defs, candidate{Vals: vals}
}

func applyCandidate(
	base *piano.Params,
	sampleRate int,
	note int,
	baseVelocity int,
	baseReleaseAfter float64,
	defs []knobDef,
	c candidate,
) (irConfigs, *piano.Params, int, float64) {
	bodyCfg := irsynth.DefaultBodyConfig()
	bodyCfg.SampleRate = sampleRate
	roomCfg := irsynth.DefaultRoomConfig()
	roomCfg.SampleRate = sampleRate
	params := cloneParams(base)
	if params.PerNote == nil {
		params.PerNote = make(map[int]*piano.NoteParams)
	}
	np := params.PerNote[note]
	if np == nil {
		np = &piano.NoteParams{}
		params.PerNote[note] = np
	}
	velocity := baseVelocity
	releaseAfter := baseReleaseAfter

	for i, def := range defs {
		v := c.Vals[i]
		switch def.Name {
		// Piano knobs.
		case "output_gain":
			params.OutputGain = float32(v)
		case "hammer_stiffness_scale":
			params.HammerStiffnessScale = float32(v)
		case "hammer_exponent_scale":
			params.HammerExponentScale = float32(v)
		case "hammer_damping_scale":
			params.HammerDampingScale = float32(v)
		case "hammer_initial_velocity_scale":
			params.HammerInitialVelocityScale = float32(v)
		case "hammer_contact_time_scale":
			params.HammerContactTimeScale = float32(v)
		case "unison_detune_scale":
			params.UnisonDetuneScale = float32(v)
		case "unison_crossfeed":
			params.UnisonCrossfeed = float32(v)
		case fmt.Sprintf("per_note.%d.loss", note):
			np.Loss = float32(v)
		case fmt.Sprintf("per_note.%d.inharmonicity", note):
			np.Inharmonicity = float32(v)
		case fmt.Sprintf("per_note.%d.strike_position", note):
			np.StrikePosition = float32(v)
		case "render.velocity":
			velocity = int(math.Round(v))
		case "render.release_after":
			releaseAfter = v
		// Body IR knobs.
		case "body_modes":
			bodyCfg.Modes = int(math.Round(v))
		case "body_brightness":
			bodyCfg.Brightness = v
		case "body_density":
			bodyCfg.Density = v
		case "body_direct":
			bodyCfg.DirectLevel = v
		case "body_decay":
			bodyCfg.DecayS = v
		case "body_duration":
			bodyCfg.DurationS = v
		// Room IR knobs.
		case "room_early":
			roomCfg.EarlyCount = int(math.Round(v))
		case "room_late":
			roomCfg.LateLevel = v
		case "room_stereo_width":
			roomCfg.StereoWidth = v
		case "room_brightness":
			roomCfg.Brightness = v
		case "room_low_decay":
			roomCfg.LowDecayS = v
		case "room_high_decay":
			roomCfg.HighDecayS = v
		case "room_duration":
			roomCfg.DurationS = v
		// Mix knobs (dual-IR).
		case "body_dry":
			params.BodyDryMix = float32(v)
		case "body_gain":
			params.BodyIRGain = float32(v)
		case "room_wet":
			params.RoomWetMix = float32(v)
		case "room_gain":
			params.RoomGain = float32(v)
		// Mix knobs (legacy).
		case "ir_wet_mix":
			params.IRWetMix = float32(v)
		case "ir_dry_mix":
			params.IRDryMix = float32(v)
		case "ir_gain":
			params.IRGain = float32(v)
		}
	}

	if bodyCfg.Modes < 1 {
		bodyCfg.Modes = 1
	}
	if roomCfg.EarlyCount < 0 {
		roomCfg.EarlyCount = 0
	}
	if velocity < 1 {
		velocity = 1
	}
	if velocity > 127 {
		velocity = 127
	}
	if releaseAfter < 0.05 {
		releaseAfter = 0.05
	}
	return irConfigs{body: bodyCfg, room: roomCfg}, params, velocity, releaseAfter
}

func fromNormalized(pos []float64, defs []knobDef) candidate {
	vals := make([]float64, len(defs))
	for i := range defs {
		x := 0.0
		if i < len(pos) {
			x = clamp(pos[i], 0, 1)
		}
		v := defs[i].Min + x*(defs[i].Max-defs[i].Min)
		if defs[i].IsInt {
			v = math.Round(v)
		}
		vals[i] = v
	}
	return candidate{Vals: vals}
}
