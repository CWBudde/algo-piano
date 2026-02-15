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
- `TestStringBankModalProcessHasNoPerBlockHeapAllocs` (`ringing_test.go`)
- `TestPianoSetStringModelSwitchesCore` (`ringing_test.go`)
- `TestModalPartialsParameterControlsModeCount` (`ringing_test.go`)
- `TestModalExcitationParameterScalesOutputEnergy` (`ringing_test.go`)

## `control.go`

- `TestHammerInfluenceScalesApplyToHammerExciter` (`ringing_test.go`)
- `TestSoftPedalAdjustsHammerExciterStrikeAndHardness` (`pedals_test.go`)

## `string_waveguide.go`

- `TestTuningAccuracy` (`string_waveguide_test.go`)
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

## `params.go`

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

## External dependency sanity checks

- `TestAlgoFFTConvolveRealMatchesDirect` (`integration_test.go`)
- `TestAlgoPDEEigenspectrumSanity` (`integration_test.go`)
