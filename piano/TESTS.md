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
- `TestResonanceProbeSeesKnownDivergingLoop` (`modal_resonance_test.go`) — the calibration of
  the two probes above against a configuration known to diverge
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
  `TestLongRenderHasNoNaNOrInf` rows never saw it. Its 45 s horizon has a blind spot of its
  own — see `resonance_growth_test.go`, which needs 120 s to make the decay unambiguous
- `TestResonanceLoopGainIsBoundedAcrossCores` (`modal_resonance_test.go`) — per-note open-loop
  gain of the bridge-injection loop, notes 21-84, on both cores at the defaults and on the
  modal core with the knobs of `assets/presets/modal-calibrated.json`. Subtests run in
  parallel; each drives for up to 24 s
- `TestAggregateResonanceLoopIsBounded` (`modal_resonance_test.go`) — the same loop with all 88
  groups undamped, both `ResonancePerNoteFilter` settings, plus a closed-loop run that must
  decay. The DWG rows asserted again on 2026-08-22 when the resonator normalisation brought
  that core's aggregate loop from 1.43 down to 0.02
- `TestResonanceProbeSeesKnownDivergingLoop` (`modal_resonance_test.go`) — runs the same probe
  on the DWG core at `resonance_gain` 0.0014 with the pedal held, a configuration whose 120 s
  render grows 8.4e3x, and requires the probe to report at least 1.0. Without it the two
  bounded-loop probes above are unfalsifiable: the 0.5 s-warmup probe they replace reported
  0.1968 for exactly this configuration. It shows the probe can see **a** diverging loop, not
  every one — see the growth tests below
- `TestResonanceLoopIsBlockSizeInvariant` (`resonance_blocksize_test.go`) — the assertion that
  the loop really is interleaved with rendering. With coupling off, a resonance-on tail
  rendered through `RingingState.Process` must be **bit-identical** across caller block sizes
  1/16/64/128/333, because every element of the loop is a per-sample recursion and
  `StringBank.prevMix` carries the one-sample delay across the call boundary. It fails on the
  pre-interleave code by 3 to 5 orders of magnitude
- `TestResonanceLoopIsBlockSizeInvariantWithoutEngine` (`resonance_blocksize_test.go`) — the
  negative control: the same comparison on a bank with no engine attached passes on both
  revisions, which is what makes a failure above attributable to the resonance path
- `TestPianoResonanceRenderIsBlockSizeStable` (`resonance_blocksize_test.go`) — the same
  comparison through the public `Piano.Process`, bounded at 1e-5 relative rather than exact:
  a non-partition-sized block goes through the convolvers' head/tail split and differs by
  ~1e-6 relative round-off (`convolver_stream.go`)
- `TestSetStringModelKeepsResonanceWired` (`resonance_blocksize_test.go`) — `SetStringModel`
  rebuilds the ringing state, and a fresh bank starts unwired, so this pins that
  `attachResonance` runs again afterwards and that the loop still drives a silent held note
- `TestStringBankResonantProcessHasNoPerBlockHeapAllocs` (`ringing_test.go`) — zero heap
  allocations per block with the loop closed and all 88 groups undamped. The older alloc
  tests build via `NewStringBank`, which leaves the bank unwired, so they never reach the
  injection path at all
- `TestDWGSustainedBankDecaysWithoutResonance` (`resonance_growth_test.go`) — six notes struck
  once under a held pedal, resonance off, coupling off: the DWG output must fall to 0.138x of
  its 25-30 s peak by 120 s. Measured 0.1275. Until the unison coupling was made dissipative
  on 2026-08-23 this same render **grew** 1.21x over 120 s and 5.41x over 300 s, and the test
  recorded that as a fence on a known-bad number
- `TestDWGResonanceSustainedDecayIsFenced` (`resonance_growth_test.go`) — the same render with
  the loop on at the shipped `resonance_gain` 0.00025: 0.1338 against the 0.1275 above, i.e.
  the sympathetic path costs about 5% of the ratio and the render still ends 17 dB below its
  own reference window. It used to read 5.06x, which was read at the time as the resonance
  loop being unstable; it was not, it was compounding a plant that was already above unity

### Unison bridge coupling (`unison_coupling_test.go`)

The strings of a unison are coupled through the bridge by a force proportional to
`mix - y_i`. The subtraction makes the term dissipative; without it the coupling was a bare
positive feedback loop around an already resonant string, and it is what made the bank above
grow. These four tests pin the property rather than the number.

- `TestUnisonCouplingRemovesEnergy` — the load-bearing one. On every multi-string note the
  coupled render must decay **faster** than the same note with `unison_crossfeed = 0`, which
  is what a dissipative term must do. Before the fix every one of them failed by orders of
  magnitude (note 60: 1.7159x coupled against 0.0003x uncoupled)
- `TestUnisonCouplingIsInertOnSingleStringNotes` — the control. Notes below MIDI 40 have one
  string and skip the coupling branch, so their render is bit-identical at any crossfeed. That
  is what attributes a failure above to the coupling and to nothing else in the render loop
- `TestUnisonCouplingDecaysAcrossTheKnobRange` — sweeps 0 … 0.005, the whole range
  `cmd/piano-fit` can select, plus the clamp. The difference form alone was not enough here:
  at the old strike position 0.92 the force came back nearly a full round trip late, and at
  0.005 — the value `assets/presets/fitted-c4.json` ships — the render still grew 88x.
  Injecting at `unisonCouplingStrikePos` instead makes the ratio flat across the range
- `TestUnisonCrossfeedIsClamped` — presets are hand-editable JSON and the coupling diverges at
  `c = 0.1` (1.8e14x), so `NewStringBank` clamps to `maxUnisonCrossfeed` rather than trusting

Both open-loop probes drive until the reading stops moving, up to a 24 s budget, and report
whether it did. A **settled** reading is the steady-state loop gain; an **unsettled** one is a
**lower bound**, because an undamped string's transient runs for minutes. The modal rows
settle; the DWG rows do not. The 0.5 bound is read through that shortfall (see
`maxResonanceLoopGain`).

**Treat a passing open-loop bound as necessary, never sufficient.** The probes inject a sine at
one note's fundamental into a plant they assume is stable. Between the interleave and the
unison-coupling fix the second assumption was false for the DWG core, and they read 0.174
against the 0.5 bound for a configuration whose render grew 5x in two minutes.
`TestDWGResonanceLongRenderDecays` and the two decay tests are what pin the loop end to end.

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
