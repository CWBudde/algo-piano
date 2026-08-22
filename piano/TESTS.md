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
