# Piano Package Test Matrix

This maps each split source file to its direct and indirect test coverage.

## `engine.go`

- `TestLongRenderHasNoNaNOrInf` (`integration_test.go`)
- `TestReleaseWithPedalUpDecaysQuickly` (`pedals_test.go`)
- `TestSustainPedalKeepsNoteRinging` (`pedals_test.go`)
- `TestSoftPedalReducesAttackBrightness` (`pedals_test.go`)
- `TestSympatheticResonanceEnergizesSilentHeldString` (`resonance_test.go`)

## `ringing.go`

- `TestStringBankUnisonStringCountByRange` (`ringing_test.go`)
- `TestStringBankDetuneScaleZeroCollapsesDetuning` (`ringing_test.go`)
- `TestStringBankBuildsOctaveCouplingEdges` (`ringing_test.go`)
- `TestCouplingEnergizesOctaveWithoutResonanceEngine` (`ringing_test.go`)
- `TestStringBankProcessHasNoPerBlockHeapAllocs` (`ringing_test.go`)
- `TestStringBankCouplingModeOffDisablesEdges` (`ringing_test.go`)
- `TestStringBankPhysicalCouplingBuildsSparseTopKGraph` (`ringing_test.go`)
- `TestPhysicalCouplingAmountScalesOutgoingGain` (`ringing_test.go`)
- `TestPhysicalCouplingDetuneSigmaPenalizesOffHarmonicTargets` (`ringing_test.go`)
- `TestPhysicalCouplingDistanceExponentReducesFarTargets` (`ringing_test.go`)
- `TestPhysicalCouplingSourceStringCountScalesOutgoingGain` (`ringing_test.go`)
- `TestStringCountCouplingScaleMonotonic` (`ringing_test.go`)
- `TestStaticCouplingSourceStringCountScalesOutgoingGain` (`ringing_test.go`)
- `TestStringBankSetCouplingModeTransitions` (`ringing_test.go`)
- `TestPianoSetCouplingModeUpdatesEngineState` (`ringing_test.go`)
- `TestCouplingModeChangesSympatheticEnergyDWG` (`coupling_behaviour_test.go`)
- `TestCouplingModeChangesSympatheticEnergyModal` (`coupling_behaviour_test.go`)
- `TestPhysicalCouplingEnergyFollowsRelatedness` (`coupling_behaviour_test.go`) — DWG and modal
- `TestRuntimeCouplingModeSwitchTakesEffectMidRender` (`coupling_behaviour_test.go`)
- `TestCouplingDetuneSigmaReducesInjectedEnergy` (`coupling_behaviour_test.go`)
- `TestCouplingDistanceExponentReducesInjectedEnergy` (`coupling_behaviour_test.go`)
- `TestPhysicalCouplingEdgeSetFollowsNeighborCap` (`coupling_behaviour_test.go`)
- `TestPhysicalCouplingEdgeSetShrinksWithTighterDetunePenalty` (`coupling_behaviour_test.go`)
- `TestPianoKeyDownWithoutStrikeIsSilentAndUndamped` (`ringing_test.go`)
- `TestStringModelDefaultsToDWG` (`ringing_test.go`)
- `TestStringBankModalModelSelectable` (`ringing_test.go`)
- `TestStringBankModalProcessHasNoPerBlockHeapAllocs` (`ringing_test.go`)
- `TestPianoSetStringModelSwitchesCore` (`ringing_test.go`)
- `TestModalPartialsParameterControlsModeCount` (`ringing_test.go`)
- `TestModalExcitationParameterScalesOutputEnergy` (`ringing_test.go`)
- `TestSoftPedalReducesAttackBrightness` (`pedals_test.go`)
- `TestPerNoteResonanceFilterIsFrequencySelective` (`resonance_test.go`)
- `TestSympatheticResonanceEnergizesSilentHeldString` (`resonance_test.go`)
- `TestLongRenderHasNoNaNOrInf` (`integration_test.go`)

## `modal_group.go`

- `TestStringBankModalModelSelectable` (`ringing_test.go`)
- `TestStringBankModalProcessHasNoPerBlockHeapAllocs` (`ringing_test.go`) — per kernel and arena
- `TestPianoSetStringModelSwitchesCore` (`ringing_test.go`)
- `TestModalPartialsParameterControlsModeCount` (`ringing_test.go`)
- `TestModalExcitationParameterScalesOutputEnergy` (`ringing_test.go`)
- `TestCouplingModeChangesSympatheticEnergyModal` (`coupling_behaviour_test.go`)
- `TestPhysicalCouplingEnergyFollowsRelatedness` (`coupling_behaviour_test.go`) — modal subtest
- `TestModalSoAOffsetsAreConsistent` (`modal_parity_test.go`)
- `TestModalFallbackSingleModeLayout` (`modal_parity_test.go`)
- `TestModalLongRenderIsFiniteAcrossKernels` (`modal_parity_test.go`)
- `TestModalResonanceEnergyStaysBounded` (`modal_resonance_test.go`) — the modal core with
  resonance and the sustain pedal held must be decaying by the end of a 6 s render, not
  merely finite
- `TestResonanceLoopGainIsBoundedAcrossCores` (`modal_resonance_test.go`) — `resonanceForceScale`
- `TestAggregateResonanceLoopIsBounded` (`modal_resonance_test.go`) — the same loop with the
  whole bank sustained, so every undamped group sums into one bridge signal
- `TestDWGResonanceLongRenderDecays` (`resonance_normalisation_test.go`) — the DWG counterpart,
  45 s rather than 6 s

## `modal_kernel.go`

Every kernel must be bit-exact with the scalar reference; the parity tests
assert equality with no tolerance.

- `TestModalKernelScalarMatchesVecmathBitExact` (`modal_parity_test.go`)
- `TestModalKernelParityAcrossDamperTransitions` (`modal_parity_test.go`)
- `TestModalKernelParityWithCouplingAndResonance` (`modal_parity_test.go`)
- `TestModalKernelParityAtLowSampleRate` (`modal_parity_test.go`) — ragged mode counts
- `TestModalAccScratchIsZeroBetweenSamples` (`modal_parity_test.go`)

## `modal_arena.go`

- `TestModalArenaBindsAndReleases` (`modal_parity_test.go`)
- `TestModalArenaSurvivesActiveSetChanges` (`modal_parity_test.go`)
- covered indirectly by every `assertKernelParity` case, which includes the arena

## `control.go`

- `TestHammerInfluenceScalesApplyToHammerExciter` (`ringing_test.go`)
- `TestSoftPedalAdjustsHammerExciterStrikeAndHardness` (`pedals_test.go`)

## `string_waveguide.go`

- `TestTuningAccuracy` (`string_waveguide_test.go`)
- `TestTuningAccuracyAcrossCompass` (`tuning_test.go`) — all 88 notes, per-register cents tolerances
- `TestTrebleRegisterIsStableInDWGCore` (`tuning_test.go`) — regression test for the delay-line
  injection headroom bug (bit-exact silence at MIDI 106-108) and for the loop DC runaway
- `TestLoopLossEnergyDecaysMonotonically` (`string_waveguide_test.go`)
- `TestDispersionDetunesPartialsFromHarmonicSeries` (`string_waveguide_test.go`)
- `TestStrikePositionChangesSpectralTilt` (`string_waveguide_test.go`)
- `TestUnisonDetuneProducesBeating` (`string_waveguide_test.go`)

## `hammer.go`

- `TestHammerVelocityIncreasesBrightnessProxy` (`hammer_test.go`)
- `TestSoftPedalAdjustsHammerExciterStrikeAndHardness` (`pedals_test.go`)

## `resonance.go`

- `TestSympatheticResonanceEnergizesSilentHeldString` (`resonance_test.go`)
- `TestPerNoteResonanceFilterIsFrequencySelective` (`resonance_test.go`)
- `TestNoteResonatorHasUnityPeakGain` (`resonance_normalisation_test.go`) — the two-pole
  resonator `filterResonanceDrive` is built from must peak at exactly one, checked both
  analytically from the coefficients and against a driven sine, at 44.1/48/96 kHz across
  notes 21-108 and all three bandwidths. It used to peak at roughly `1/(2*sin(w0))`, and
  that 1/f0 tilt is what made the DWG resonance loop diverge
- `TestResonanceDriveBankGainIsRegisterIndependent` (`resonance_normalisation_test.go`) — the
  bank-level statement of the same property in both cores: the summed peak of a group's three
  resonators is 1.85 (the partial weights) at every note, where it used to run 183.1 at note
  21 against 2.6 at note 96
- `TestDWGResonanceLongRenderDecays` (`resonance_normalisation_test.go`) — 45 s through the
  public `Piano.Process` with the DWG core, resonance on and the sustain pedal held; no
  five-second window may exceed 1.5x the first sustain window. This is the assertion the
  divergence escaped: it stayed finite for well over 40 s, so the 4 s
  `TestLongRenderHasNoNaNOrInf` rows never saw it
- `TestResonanceLoopGainIsBoundedAcrossCores` (`modal_resonance_test.go`) — per-note open-loop
  gain of the bridge-injection loop, notes 21-84, on both cores at the defaults and on the
  modal core with the knobs of `assets/presets/modal-calibrated.json`
- `TestAggregateResonanceLoopIsBounded` (`modal_resonance_test.go`) — the same loop with all 88
  groups undamped, both `ResonancePerNoteFilter` settings, plus a closed-loop run that must
  decay. The DWG rows asserted again on 2026-08-22 when the resonator normalisation brought
  that core's aggregate loop from 1.43 down to 0.02

Both open-loop probes warm up for 0.5 s, which is far short of an undamped string's steady
state, so their numbers are **lower bounds** on the loop gain and the 0.5 bound they assert is
not a proof of stability — see the caveat on `maxResonanceLoopGain` and the Phase 9.6 follow-up
in `PLAN.md`. `TestDWGResonanceLongRenderDecays` is what actually pins the loop.

## `convolver.go`

- `TestPartitionedConvolverMatchesDirectConvolution` (`convolver_test.go`)
- `TestConvolverResetClearsTail` (`convolver_test.go`)
- `TestConvolverLoads96kWavAndResamples` (`convolver_test.go`)
- `TestConvolverLoadsMonoWavAsDualMono` (`convolver_test.go`)
- `TestConvolverMatchesDirectConvolutionLongIR` (`convolver_test.go`) — IRs of 512 and 8192 taps, i.e. longer than one partition, for both `SoundboardConvolver` and `BodyConvolver`
- `TestConvolverBlockSizeContinuity` (`convolver_test.go`) — one stream chopped into chunks of 1, 63, 64, 100, 128, 256 and 333 must convolve the same; regression test for the non-`partSize` block defect
- `TestConvolverShortIRBlockSizeContinuity` (`convolver_test.go`) — the same sweep over IRs of 1, 32, 64, 127, 128 and 129 taps crossed with chunks of 1, 63, 64, 100, 128, 200, 255, 256, 333 and 1200; regression test for the input history being sized from the tail stage's priming window, which is zero for a single-partition IR
- `TestConvolverImpulseRecoversIR` (`convolver_test.go`) — pins zero added latency
- `TestConvolverResetMidStreamStartsFresh` (`convolver_test.go`) — `Reset()` off a partition boundary

## `convolver_stream.go`

- Covered by the five streaming tests listed under `convolver.go` above; the
  partition plumbing has no public surface of its own.

## `params.go`

- `TestCouplingDetuneSigmaReducesInjectedEnergy` (`coupling_behaviour_test.go`) — `CouplingDetuneSigmaCents`
- `TestCouplingDistanceExponentReducesInjectedEnergy` (`coupling_behaviour_test.go`) — `CouplingDistanceExponent`
- `TestPhysicalCouplingEdgeSetFollowsNeighborCap` (`coupling_behaviour_test.go`) — `CouplingMaxNeighbors`
- Covered indirectly by all tests that call `NewDefaultParams`, especially:
  - `TestLongRenderHasNoNaNOrInf` (`integration_test.go`)
  - `TestSoftPedalReducesAttackBrightness` (`pedals_test.go`)
  - `TestSympatheticResonanceEnergizesSilentHeldString` (`resonance_test.go`)

## `utils.go`

- Covered indirectly via frequency and math paths in:
  - `TestTuningAccuracy` (`string_waveguide_test.go`)
  - `TestUnisonDetuneProducesBeating` (`string_waveguide_test.go`)
  - `TestStringBankUnisonStringCountByRange` (`ringing_test.go`)
  - `TestPartitionedConvolverMatchesDirectConvolution` (`convolver_test.go`)

## Cross-core comparisons

- `TestDWGModalDistanceIsBounded` (`core_distance_test.go`) — bounds the objective
  `analysis.Compare` distance between the DWG and modal cores (PLAN.md 12.4)

## External dependency sanity checks

- `TestAlgoFFTConvolveRealMatchesDirect` (`integration_test.go`)
- `TestAlgoPDEEigenspectrumSanity` (`integration_test.go`)
