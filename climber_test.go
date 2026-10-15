// Copyright (c) 2026 Alexey Mayshev and contributors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otter

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// These are white-box tests of the climber's tiers and probe machine. The cache applies the
// returned adjustment by transferring entries between the regions, so the tests drive
// determineAdjustment directly with the caller's evolving window size; the policy tests cover the
// transfers.

const ctMaximum int64 = 8192

var (
	ctStride  = stepPercent * float64(ctMaximum)
	ctFloor   = windowFloorFraction * float64(ctMaximum)
	ctMaxStep = maxStepFraction * float64(ctMaximum)
)

func TestWindowClimber_Seeding(t *testing.T) {
	t.Parallel()

	t.Run("resized_seedsByTier", func(t *testing.T) {
		t.Parallel()

		c := newWindowClimber()
		c.resized(512)
		require.Equal(t, stepPercent*512, climberStepOf(&c).size)

		c.resized(ctMaximum)
		dc := densityOf(t, &c)
		require.Equal(t, -ctStride, climberStepOf(&c).size)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
		// Every resize re-arms the cold-start calibration audit.
		require.Equal(t, auditWaitFirst, dc.auditClock.waitSamples)
	})

	t.Run("resized_resetsProbeState", func(t *testing.T) {
		t.Parallel()

		c := makeTestClimber()
		dc := &c.density
		injectWalk(dc, false, false, 1024, 0.5, 0.5).samples = 5
		dc.refractoryLeft = 5
		c.carryOver(100)

		c.resized(ctMaximum)
		dc = densityOf(t, c)
		require.False(t, dc.walking)
		require.Equal(t, int64(0), c.adjustment)
		require.Equal(t, 0, dc.refractoryLeft)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("resized_clearsTheSample", func(t *testing.T) {
		t.Parallel()

		// Counts earned under the old geometry must not seed the first decision under the new one.
		c := makeTestClimber()
		c.recordHit(true, false)
		c.recordHit(false, true)
		c.recordMiss()
		c.sample.previousHitRate = 0.5

		c.resized(ctMaximum)
		require.Equal(t, int64(0), c.sample.hits)
		require.Equal(t, int64(0), c.sample.misses)
		require.Equal(t, int64(0), c.sample.windowHits)
		require.Equal(t, int64(0), c.sample.probationHits)
		require.Equal(t, 0.0, c.sample.previousHitRate)
	})

	t.Run("discardSample_keepsTheCrossSampleRate", func(t *testing.T) {
		t.Parallel()

		// A sample gathered below half occupancy says nothing about the split, so the first
		// decision after the gap compares against the rate from before it rather than against zero.
		c := makeTestClimber()
		c.sample.previousHitRate = 0.6
		c.sample.hits += 8192
		c.sample.misses += 8192

		c.discardSample(ctMaximum, 16_384)
		require.Equal(t, int64(0), c.sample.hits)
		require.Equal(t, int64(0), c.sample.misses)
		require.Equal(t, 0.6, c.sample.previousHitRate)
	})
}

func TestWindowClimber_Sampling(t *testing.T) {
	t.Parallel()

	t.Run("samplePeriod_tierBoundary_isTheDensityThreshold", func(t *testing.T) {
		t.Parallel()

		// At the threshold the reactive climber runs on the sketch's period; one entry above it the
		// density climber runs on min(4*maximum, sketch). A 16,400-request feed sits between the
		// two periods, so it decides above the boundary and holds at it.
		reactive := newWindowClimber()
		reactive.resized(densityThreshold)
		reactive.sample.hits += 8192
		reactive.sample.misses += 8208
		reactive.determineAdjustment(densityThreshold, 41, protectedMax(densityThreshold, 41), 16_488)
		require.Equal(t, int64(0), reactive.adjustment)
		require.Equal(t, int64(8192), reactive.sample.hits)

		density := newWindowClimber()
		density.resized(densityThreshold + 1)
		density.sample.hits += 5000
		density.sample.misses += 5000
		density.determineAdjustment(densityThreshold+1, 41, protectedMax(densityThreshold+1, 41), 16_488)
		require.Equal(t, int64(0), density.adjustment)

		density.sample.hits += 3192
		density.sample.misses += 3208
		density.determineAdjustment(densityThreshold+1, 41, protectedMax(densityThreshold+1, 41), 16_488)
		require.NotEqual(t, int64(0), density.adjustment)
		require.Equal(t, int64(0), density.sample.hits)
	})

	t.Run("samplePeriod_largeTier_completesExactlyAtFourMaxima", func(t *testing.T) {
		t.Parallel()

		// One request short of 4*maximum holds the carry-over; the exact count decides.
		c := makeTestClimber()
		c.sample.hits += 16_384
		c.sample.misses += 16_383
		c.determineAdjustment(ctMaximum, 2000, protectedMax(ctMaximum, 2000), 100_000)
		require.Equal(t, int64(0), c.adjustment)
		require.Equal(t, int64(16_384), c.sample.hits)

		c.sample.misses++
		c.determineAdjustment(ctMaximum, 2000, protectedMax(ctMaximum, 2000), 100_000)
		require.NotEqual(t, int64(0), c.adjustment)
		require.Equal(t, int64(0), c.sample.hits)
	})

	t.Run("samplePeriod_hugeMaximum_staysQuiescent", func(t *testing.T) {
		t.Parallel()

		// 4*maximum overflows near MaxInt64; the guard saturates it rather than letting a negative
		// period close every sample, so the sketch's sample size bounds the period.
		maximum := int64(math.MaxInt64 - 100)
		c := newWindowClimber()
		c.resized(maximum)
		c.sample.hits += 500_000
		c.sample.misses += 500_000
		c.determineAdjustment(maximum, maximum/100, maximum/2, math.MaxInt32)
		require.Equal(t, int64(0), c.adjustment)
		require.Equal(t, int64(500_000), c.sample.hits)
	})

	t.Run("samplePeriod_smallTier_stretchesAsTheStepDecays", func(t *testing.T) {
		t.Parallel()

		// The period grows by the step's decay from its initial magnitude, capped at the ratio
		// limit; a zeroed step on a zero-capacity cache degenerates to a ratio of one.
		c := newWindowClimber()
		c.resized(512)
		c.sample.hits += 500
		c.sample.misses += 500
		c.determineAdjustment(512, 5, protectedMax(512, 5), 1000)
		require.Equal(t, int64(0), c.sample.hits)

		climberStepOf(&c).size = 8.0
		c.sample.hits += 2000
		c.sample.misses += 1999
		c.determineAdjustment(512, 5, protectedMax(512, 5), 1000)
		require.Equal(t, int64(2000), c.sample.hits)

		c.sample.misses++
		c.determineAdjustment(512, 5, protectedMax(512, 5), 1000)
		require.Equal(t, int64(0), c.sample.hits)

		empty := newWindowClimber()
		empty.resized(0)
		climberStepOf(&empty).size = 0.0
		empty.sample.misses += 1000
		empty.determineAdjustment(0, 0, 0, 1000)
		require.Equal(t, int64(0), empty.sample.misses)
	})

	t.Run("stepDecayRate_boundary_isTheSlowAdaptThreshold", func(t *testing.T) {
		t.Parallel()

		// 512 decays at the slow rate, 513 at the standard rate.
		slow := newWindowClimber()
		slow.resized(512)
		slowSeed := climberStepOf(&slow).size
		reactiveSampleAt(&slow, 512, 500, 501)
		reactiveSampleAt(&slow, 512, 505, 496)
		require.InDelta(t, 0.995*slowSeed, climberStepOf(&slow).size, 1.0e-9)

		stock := newWindowClimber()
		stock.resized(513)
		stockSeed := climberStepOf(&stock).size
		reactiveSampleAt(&stock, 513, 500, 501)
		reactiveSampleAt(&stock, 513, 505, 496)
		require.InDelta(t, stepDecayRate*stockSeed, climberStepOf(&stock).size, 1.0e-9)
	})

	t.Run("reactiveStep_flatSampleKeepsDirection_exactRestartReseeds", func(t *testing.T) {
		t.Parallel()

		// A hit rate change of exactly zero keeps the direction, and a change of exactly the
		// restart threshold restores the decayed step to its full magnitude.
		c := newWindowClimber()
		c.resized(2048)
		seed := climberStepOf(&c).size
		reactiveSampleAt(&c, 2048, 500, 501)
		reactiveSampleAt(&c, 2048, 500, 501)
		require.Equal(t, int64(seed), c.adjustment)
		require.InDelta(t, stepDecayRate*seed, climberStepOf(&c).size, 1.0e-9)

		reactiveSampleAt(&c, 2048, 560, 441)
		require.Equal(t, int64(stepDecayRate*seed), c.adjustment)
		require.Equal(t, seed, climberStepOf(&c).size)
	})
}

func TestWindowClimber_Arming(t *testing.T) {
	t.Parallel()

	t.Run("armProbe_windowBlind_probesUp", func(t *testing.T) {
		t.Parallel()

		c := makeTestClimber()
		dc := &c.density
		adjustment := sampleClimber(c, 1024, 0, 500, 500)

		w := walkOf(t, dc)
		require.False(t, w.down)
		require.Equal(t, int64(ctStride), adjustment)
		require.Equal(t, int64(1024), w.baseWindow)
		require.Equal(t, 0.5, w.baseHitRate)
		require.Equal(t, 1, w.samples)
	})

	t.Run("armProbe_mainBlind_neverProbes", func(t *testing.T) {
		t.Parallel()

		// A starved main beside a large window arms nothing, since the equilibrium audit owns that
		// terrain; the sample falls through to steering with main priced at its floor.
		c := makeTestClimber()
		dc := &c.density
		adjustment := sampleClimber(c, 7000, 500, 0, 500)

		windowDensity := 500 / 7000.0
		mainFloor := (0.125 * 4) / (float64(ctMaximum) - 7000.0)
		require.False(t, dc.walking)
		require.Equal(t, int64(math.Log(windowDensity/mainFloor)*densityGain*float64(ctMaximum)), adjustment)
	})

	t.Run("armProbe_deadSample_probesAwayFromNearerBound", func(t *testing.T) {
		t.Parallel()

		wide := makeTestClimber()
		down := sampleClimber(wide, 5000, 0, 0, 1000)
		require.True(t, walkOf(t, &wide.density).down)
		require.Equal(t, int64(-ctStride), down)

		narrow := makeTestClimber()
		up := sampleClimber(narrow, 3000, 0, 0, 1000)
		require.False(t, walkOf(t, &narrow.density).down)
		require.Equal(t, int64(ctStride), up)
	})

	t.Run("armProbe_starvedLargeRegion_doesNotProbe", func(t *testing.T) {
		t.Parallel()

		// A main earning nothing is visible to density; probing would shrink the one region that
		// is working.
		c := makeTestClimber()
		adjustment := sampleClimber(c, 4000, 1000, 0, 0)
		require.False(t, c.density.walking)
		require.Greater(t, adjustment, int64(0))

		other := makeTestClimber()
		shrink := sampleClimber(other, 3000, 0, 1000, 0)
		require.False(t, other.density.walking)
		require.Less(t, shrink, int64(0))
	})

	t.Run("armProbe_refractory_ticksAndHolds", func(t *testing.T) {
		t.Parallel()

		// A refractory sample is one the density signal cannot be trusted on, so the climber holds
		// rather than letting a handful of window hits in a blank sample authorize a large step.
		c := makeTestClimber()
		dc := &c.density
		dc.refractoryLeft = 3
		adjustment := sampleClimber(c, 1024, 0, 500, 500)

		require.False(t, dc.walking)
		require.Equal(t, 2, dc.refractoryLeft)
		require.Equal(t, int64(0), adjustment)
	})

	t.Run("armProbe_refractory_liftsABelowFloorWindow", func(t *testing.T) {
		t.Parallel()

		// The hold still lifts a window below the floor; on a workload whose every sample is blind,
		// a bare hold would wedge the initial window under the floor for the life of the run.
		c := makeTestClimber()
		dc := &c.density
		dc.refractoryLeft = 3
		adjustment := sampleClimber(c, 82, 0, 500, 500)

		require.False(t, dc.walking)
		require.Equal(t, 2, dc.refractoryLeft)
		require.Equal(t, int64(ctFloor-82), adjustment)
	})

	t.Run("armProbe_refractory_dueClockAuditsRatherThanHolding", func(t *testing.T) {
		t.Parallel()

		// A corner that never clears would otherwise serve its whole backoff standing still while
		// the clock says the position is due to be tested.
		c := makeTestClimber()
		dc := &c.density
		dc.refractoryLeft = 3
		dc.auditClock.stillSamples = auditWaitFirst
		adjustment := sampleClimber(c, 1024, 0, 500, 500)

		require.True(t, walkOf(t, dc).isAudit)
		require.Equal(t, 3, dc.refractoryLeft)
		require.Equal(t, int64(-ctStride), adjustment)
	})

	t.Run("undoProbe_auditRetreat_leavesTheStarvationRefractoryAlone", func(t *testing.T) {
		t.Parallel()

		// The refractory is the starvation probe's backoff. An audit that preempted a hold and
		// crashed leaves the hold where it was rather than re-arming it to the whole rung.
		c := makeTestClimber()
		dc := &c.density
		dc.refractoryLeft = 3
		dc.auditClock.stillSamples = auditWaitFirst
		sampleClimber(c, 1024, 0, 500, 500)
		require.True(t, walkOf(t, dc).isAudit)

		undo := sampleClimber(c, 512, 0, 300, 700)
		require.False(t, dc.walking)
		require.Equal(t, int64(1024-512), undo)
		require.Equal(t, 3, dc.refractoryLeft)
		require.Equal(t, probeBackoffInitial, dc.auditClock.waitSamples)
	})

	t.Run("armProbe_refractory_dueClockBelowTheFloor_stillClearsIt", func(t *testing.T) {
		t.Parallel()

		// Preempting the hold must not cost the lift the hold would have made: below the floor the
		// audit is refused a downward direction and its entry stride carries it clear.
		c := makeTestClimber()
		dc := &c.density
		dc.refractoryLeft = 3
		dc.auditClock.stillSamples = auditWaitFirst
		adjustment := sampleClimber(c, 82, 0, 500, 500)

		require.True(t, walkOf(t, dc).isAudit)
		require.GreaterOrEqual(t, 82+adjustment, int64(ctFloor))
	})

	t.Run("blindCorner_positionBoundaries_windowQuarterPointOnly", func(t *testing.T) {
		t.Parallel()

		// A starved window is blind only while it holds at most a quarter of the cache, exact at
		// the edge; a starved main is never blind, since the equilibrium audit owns that terrain.
		atEdge := makeTestClimber()
		sampleClimber(atEdge, 2048, 0, 600, 400)
		require.True(t, atEdge.density.walking)

		pastEdge := makeTestClimber()
		sampleClimber(pastEdge, 2049, 0, 600, 400)
		require.False(t, pastEdge.density.walking)

		mainAtCorner := makeTestClimber()
		sampleClimber(mainAtCorner, 6144, 600, 0, 400)
		require.False(t, mainAtCorner.density.walking)

		mainPastCorner := makeTestClimber()
		sampleClimber(mainPastCorner, 6600, 600, 0, 400)
		require.False(t, mainPastCorner.density.walking)
	})

	t.Run("armProbe_deadSampleDirection_splitsAtTheMidpoint", func(t *testing.T) {
		t.Parallel()

		down := makeTestClimber()
		sampleClimber(down, 4096, 0, 0, 1000)
		require.True(t, walkOf(t, &down.density).down)

		up := makeTestClimber()
		sampleClimber(up, 4095, 0, 0, 1000)
		require.False(t, walkOf(t, &up.density).down)
	})

	t.Run("starvationBar_knifeEdge_probesOnlyBelowTheBar", func(t *testing.T) {
		t.Parallel()

		// Window hits at the starvation bar are sighted (no probe), while one hit fewer is starved
		// and arms a probe. The bar's floor is 4 for these short samples.
		sighted := makeTestClimber()
		held := sampleClimber(sighted, 164, 4, 800, 196)
		require.False(t, sighted.density.walking)
		require.LessOrEqual(t, held, int64(0))

		starved := makeTestClimber()
		probed := sampleClimber(starved, 164, 3, 800, 197)
		require.False(t, walkOf(t, &starved.density).down)
		require.Equal(t, int64(ctStride), probed)
	})
}

func TestWindowClimber_DensityStep(t *testing.T) {
	t.Parallel()

	t.Run("densityStep_starvedOpposingRegion_boundsTheStep", func(t *testing.T) {
		t.Parallel()

		// A blank main is imputed an eighth of the starvation bar spread over its capacity rather
		// than the epsilon's huge ratio, so a handful of window hits cannot authorize the maximum
		// step.
		c := makeTestClimber()
		adjustment := sampleClimber(c, 2049, 40, 0, 960)

		windowDensity := 40 / 2049.0
		mainFloor := (0.125 * 4) / float64(ctMaximum-2049)
		require.False(t, c.density.walking)
		require.Equal(t, int64(math.Log(windowDensity/mainFloor)*densityGain*float64(ctMaximum)), adjustment)
		require.Less(t, adjustment, int64(ctMaxStep))
	})

	t.Run("densityStep_commonModeScaling_cancels", func(t *testing.T) {
		t.Parallel()

		// A phase that scales both regions' earnings together cancels in the density ratio, so two
		// samples with the same split at different traffic intensities command the same step.
		quiet := makeTestClimber()
		small := sampleClimber(quiet, 2000, 100, 300, 600)

		loud := makeTestClimber()
		large := sampleClimber(loud, 2000, 400, 1200, 2400)

		require.Equal(t, densityStep(2000, 100, 300), small)
		require.NotEqual(t, int64(0), small)
		require.Equal(t, small, large)
	})

	t.Run("densityStep_starvedInteriorWindow_steersInsteadOfProbing", func(t *testing.T) {
		t.Parallel()

		// A starved region that is not small is not blind: the other region is measurable, and the
		// floored steering error walks the window down without a probe.
		c := makeTestClimber()
		adjustment := sampleClimber(c, 3000, 0, 600, 400)

		windowFloor := (0.125 * 4) / 3000.0
		mainDensity := 600 / float64(ctMaximum-3000)
		require.False(t, c.density.walking)
		require.Equal(t, int64(math.Log(windowFloor/mainDensity)*densityGain*float64(ctMaximum)), adjustment)
	})

	t.Run("densityStep_floorCrossingShrink_clampsAtTheFloor", func(t *testing.T) {
		t.Parallel()

		c := makeTestClimber()
		adjustment := sampleClimber(c, 200, 4, 900, 96)

		require.False(t, c.density.walking)
		require.Equal(t, int64(ctFloor-200), adjustment)
	})

	t.Run("densityStep_balancedShares_holdsFirm", func(t *testing.T) {
		t.Parallel()

		// At the proportional balance point the error is about zero and the window rests.
		for _, windowMax := range []int64{1000, 2000, 4000, 6000} {
			c := makeTestClimber()
			adjustment := steadySample(c, windowMax, 0.8)
			require.False(t, c.density.walking, "windowMax %d", windowMax)
			require.LessOrEqual(t, absInt64(adjustment), int64(2), "windowMax %d", windowMax)
		}
	})

	t.Run("densityStep_extremeImbalance_saturatesAtMaximumStep", func(t *testing.T) {
		t.Parallel()

		// The steering floor prices "earned nothing" low rather than infinitely low, and the cap
		// bounds the displacement.
		c := makeTestClimber()
		adjustment := sampleClimber(c, 164, 1000, 0, 0)

		require.False(t, c.density.walking)
		require.Equal(t, int64(ctMaxStep), adjustment)
	})
}

func TestWindowClimber_GuardRail(t *testing.T) {
	t.Parallel()

	t.Run("guardRail_sustainedShortfall_vetoesBackToAnchor", func(t *testing.T) {
		t.Parallel()

		// When steering walks away from a well-earning anchor and the smoothed rate settles below
		// its claim, the veto returns there and parks.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		require.Equal(t, int64(2000), dc.anchor.window)
		require.InDelta(t, 0.70, dc.anchor.rate, 1.0e-6)

		windowMax := int64(4000)
		vetoed := false
		for range 40 {
			adjustment := steadySample(c, windowMax, 0.68)
			if dc.anchor.returning || dc.anchor.held {
				vetoed = true
				require.LessOrEqual(t, adjustment, int64(0))
				windowMax += adjustment
			}
			if dc.anchor.held && !dc.anchor.returning {
				break
			}
		}
		require.True(t, vetoed)
		require.True(t, dc.anchor.held)
		require.True(t, dc.anchor.isAt(windowMax, stableBand(ctMaximum)))

		held := steadySample(c, windowMax, 0.68)
		require.Equal(t, int64(0), held)
	})

	t.Run("guardRail_onAnchor_rateClaimTracksReality", func(t *testing.T) {
		t.Parallel()

		// Standing on the anchor re-syncs its claim, so a workload whose rate genuinely fell does
		// not leave a stale claim that vetoes forever.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		for range 40 {
			steadySample(c, 2000, 0.66)
		}
		require.Equal(t, int64(2000), dc.anchor.window)
		require.InDelta(t, 0.66, dc.anchor.rate, 0.005)
		require.False(t, dc.anchor.held)
	})

	t.Run("guardRail_crashScaleShift_nearAnchor_standsDown", func(t *testing.T) {
		t.Parallel()

		// A crash-scale swing at the anchor's own position shows its claim is wrong.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		steadySample(c, 2000, 0.55)
		require.False(t, dc.anchor.isPlanted())
		require.False(t, dc.anchor.held)
		require.Equal(t, 0, dc.anchor.shortfallStreak)
	})

	t.Run("guardRail_crashScaleShift_nearAnchor_reseedsTheGoalMetric", func(t *testing.T) {
		t.Parallel()

		// A smoothed rate carried across the shift is mostly the old regime, and a claim re-planted
		// from it could not be earned at any position.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		steadySample(c, 2000, 0.20)
		require.False(t, dc.anchor.isPlanted())
		require.InDelta(t, 0.20, dc.rates.smoothed, 1.0e-6)
		require.InDelta(t, deviationSeed, dc.rates.deviation, 1.0e-6)

		// The re-plant on the next sample claims the new regime's rate, not a blend of both.
		steadySample(c, 5000, 0.20)
		require.Equal(t, int64(5000), dc.anchor.window)
		require.InDelta(t, 0.20, dc.anchor.rate, 0.01)
	})

	t.Run("guardRail_crashScaleShift_farFromAnchor_keepsAnchor", func(t *testing.T) {
		t.Parallel()

		// A crash far from the anchor is not evidence against it; it is typically the climber's
		// own movement, which the veto exists to catch.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		steadySample(c, 5000, 0.55)
		require.Equal(t, int64(2000), dc.anchor.window)
		require.False(t, dc.anchor.held)
		require.Equal(t, 0, dc.anchor.shortfallStreak)
		require.InDelta(t, 0.67, dc.rates.smoothed, 1.0e-6)
	})

	t.Run("guardRail_noAnchorSeedWhileProbing", func(t *testing.T) {
		t.Parallel()

		// A probe's transient window paired with a rate earned elsewhere is a phantom claim, so the
		// seed waits until the walk resolves.
		c := probingUp(ctStride, 0.5, 0)
		dc := &c.density
		dc.anchor.window = -1
		dc.rates.smoothed = 0.5
		// The watched region stays under the adjudication bar, so the walk continues.
		walking := sampleClimber(c, 3000, 5, 515, 480)
		require.NotEqual(t, int64(0), walking)
		require.True(t, dc.walking)
		require.Equal(t, int64(-1), dc.anchor.window)
	})

	t.Run("guardRail_vetoPark_isNotCrashShielded", func(t *testing.T) {
		t.Parallel()

		// The fresh-park shield belongs to audit confirmations alone. A crash-scale swing on the
		// landing sample is the return's own and waits for the retest, which then releases the
		// park one sample later.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for range vetoStreak {
			steadySample(c, 1500, 0.66)
		}
		require.True(t, dc.anchor.held)
		require.Equal(t, 0, dc.anchor.freshLeft)

		steadySample(c, 2000, 0.60)
		require.True(t, dc.anchor.held)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		steadySample(c, 2000, 0.60)
		require.False(t, dc.anchor.held)
		require.False(t, dc.anchor.isPlanted())
		require.True(t, dc.rates.isUnseeded())
	})

	t.Run("guardRail_marginTracksNoise_silentWhileWideThenVetoes", func(t *testing.T) {
		t.Parallel()

		// While the deviation estimate is wide the rail stays silent on a gap it cannot tell from
		// noise; as quiet samples tighten the estimate the same gap becomes actionable. The anchor
		// is one capped stride away, so the return completes within a single sample.
		c := makeTestClimber()
		dc := &c.density
		dc.anchor.window = 2000
		dc.anchor.rate = 0.70
		dc.rates.smoothed = 0.66
		c.sample.previousHitRate = 0.66
		dc.rates.deviation = 0.05
		dc.auditClock.waitSamples = auditWaitInitial

		vetoedAt := -1
		for i := 1; i <= 12; i++ {
			steadySample(c, 4000, 0.66)
			if dc.anchor.held {
				vetoedAt = i
				break
			}
		}
		require.Greater(t, vetoedAt, 5)
		require.LessOrEqual(t, vetoedAt, 12)
		require.False(t, dc.anchor.returning)
		require.Equal(t, int64(2000-4000), c.adjustment)
	})

	t.Run("guardRail_vetoReturnBudget_settlesWhereItStands", func(t *testing.T) {
		t.Parallel()

		// A return whose strides the cache cannot realize settles at its budget rather than
		// striding forever; the park then holds the reached position.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial

		strides := 0
		for range 20 {
			adjustment := steadySample(c, 7500, 0.66)
			if adjustment == -int64(ctMaxStep) {
				strides++
			}
			if dc.anchor.held && !dc.anchor.returning {
				break
			}
		}
		require.Equal(t, vetoReturnBudget, strides)
		require.True(t, dc.anchor.held)
		require.False(t, dc.anchor.returning)

		held := steadySample(c, 7500, 0.66)
		require.Equal(t, int64(0), held)
	})

	t.Run("guardRail_returnArrival_falsifiesAStaleClaim", func(t *testing.T) {
		t.Parallel()

		// Arriving proves nothing about the claim that sent the window here. The position is
		// settled and then judged against the claim frozen when the return began.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for range vetoStreak {
			steadySample(c, 1500, 0.66)
		}
		require.True(t, dc.anchor.held)
		require.False(t, dc.anchor.returning)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		for range retestSettle - 1 {
			steadySample(c, 2000, 0.66)
			require.True(t, dc.anchor.isPlanted())
		}
		// The re-sync at the anchor has decayed the live claim into the shortfall being tested,
		// which is why the frozen one is the judge.
		require.InDelta(t, 0.66, dc.anchor.rate, 0.005)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		steadySample(c, 2000, 0.66)
		require.False(t, dc.anchor.isPlanted())
		require.False(t, dc.anchor.held)
		require.True(t, dc.rates.isUnseeded())
	})

	t.Run("guardRail_returnArrival_keepsAClaimThePositionEarns", func(t *testing.T) {
		t.Parallel()

		// A rate that recovers on the way home leaves the window at a position that earns the
		// claim, and the park stands.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for range vetoStreak {
			steadySample(c, 7500, 0.66)
		}
		require.True(t, dc.anchor.returning)

		// Strides home under a recovery that no single sample reports as a workload change.
		windowMax := int64(7500)
		hitRate := 0.66
		for range 20 {
			hitRate = math.Min(0.76, hitRate+0.02)
			windowMax += steadySample(c, windowMax, hitRate)
			if !dc.anchor.returning {
				break
			}
		}
		require.False(t, dc.anchor.returning)
		require.True(t, dc.anchor.isAt(windowMax, stableBand(ctMaximum)))
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		for range retestSettle {
			require.Equal(t, int64(0), steadySample(c, windowMax, hitRate))
		}
		require.Equal(t, -1.0, dc.anchor.retestClaim)
		require.True(t, dc.anchor.isPlanted())
		require.True(t, dc.anchor.held)
		require.False(t, dc.rates.isUnseeded())
	})

	t.Run("guardRail_retestDue_waitsForTheReturnToEnd", func(t *testing.T) {
		t.Parallel()

		// A return still under way has no retest due, and asking from off the anchor must not
		// cancel the retest that the arrival owes.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for range vetoStreak {
			steadySample(c, 7500, 0.66)
		}
		require.True(t, dc.anchor.returning)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)
		settleLeft := dc.anchor.settleLeft

		r := newClimberReading(&c.sample, ctMaximum, 7500, protectedMax(ctMaximum, 7500))
		require.False(t, dc.anchor.isRetestDue(&r))
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)
		require.Equal(t, settleLeft, dc.anchor.settleLeft)
	})

	t.Run("guardRail_returnLanding_isJudgedByTheRetest", func(t *testing.T) {
		t.Parallel()

		// The landing sample's recovery is the return's own, not a workload change: the claim
		// survives to the retest, which keeps a position that earns it.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for range vetoStreak {
			steadySample(c, 1500, 0.66)
		}
		require.True(t, dc.anchor.held)
		require.False(t, dc.anchor.returning)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		steadySample(c, 2000, 0.80)
		require.True(t, dc.anchor.isPlanted())
		require.True(t, dc.anchor.held)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)
		require.False(t, dc.rates.isUnseeded())

		for range retestSettle - 1 {
			steadySample(c, 2000, 0.80)
		}
		require.Equal(t, -1.0, dc.anchor.retestClaim)
		require.True(t, dc.anchor.isPlanted())
		require.True(t, dc.anchor.held)
	})

	t.Run("guardRail_returnShortOfTheAnchor_hasNothingToRetest", func(t *testing.T) {
		t.Parallel()

		// A return that spends its budget without arriving never reached the position its claim
		// describes, so the reached position keeps its park.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for range 20 {
			steadySample(c, 7500, 0.66)
			if dc.anchor.held && !dc.anchor.returning {
				break
			}
		}
		require.False(t, dc.anchor.returning)
		require.False(t, dc.anchor.isAt(7500, stableBand(ctMaximum)))

		for range retestSettle + 1 {
			steadySample(c, 7500, 0.66)
		}
		require.True(t, dc.anchor.isPlanted())
		require.Equal(t, int64(2000), dc.anchor.window)
		require.Equal(t, -1.0, dc.anchor.retestClaim)
	})

	t.Run("guardRail_crashScaleShift_releasesTheParkAndReturn", func(t *testing.T) {
		t.Parallel()

		// A crash-scale swing announces a workload change, so any park or return is released.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		dc.anchor.held = true
		dc.anchor.returning = true
		dc.anchor.returnLeft = 5

		steadySample(c, 5000, 0.55)
		require.False(t, dc.anchor.held)
		require.False(t, dc.anchor.returning)
		require.Equal(t, int64(2000), dc.anchor.window)
	})

	t.Run("guardRail_vetoReturn_shiftMidReturn_abandonsTheRetest", func(t *testing.T) {
		t.Parallel()

		// An interrupted return does not land, so its retest goes with it. A pending claim would
		// otherwise judge a same-sample re-plant against a past regime's rate.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		dc.anchor.window = 7000 // too far to reach in one stride, so the return is in flight
		for range vetoStreak {
			steadySample(c, 1500, 0.66)
		}
		require.True(t, dc.anchor.returning)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		// Mid-return the workload recovers and then jumps crash-scale, far from the anchor.
		dc.rates.smoothed = 0.75
		dc.rates.deviation = 0.001
		c.sample.previousHitRate = 0.75
		adjustment := sampleClimber(c, 3957, 5, 805, 190)

		require.Equal(t, -1.0, dc.anchor.retestClaim)
		require.Equal(t, 0, dc.anchor.settleLeft)
		require.False(t, dc.anchor.returning)
		require.Equal(t, int64(3957), dc.anchor.window)
		require.InDelta(t, 0.762, dc.anchor.rate, 1.0e-6)
		require.NotEqual(t, int64(0), adjustment)
	})

	t.Run("guardRail_vetoReturn_budgetOut_thenAnchorMoves_abandonsTheRetest", func(t *testing.T) {
		t.Parallel()

		// A return that spends its budget short of the anchor leaves the retest pending; a rate
		// that recovered meanwhile moves the anchor onto the window, and the old claim must not
		// judge the new position.
		c := seededShortfall()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitMax
		for range 20 {
			steadySample(c, 7500, 0.66)
			if dc.anchor.held && !dc.anchor.returning {
				break
			}
		}
		require.False(t, dc.anchor.returning)
		require.Equal(t, int64(2000), dc.anchor.window)
		require.InDelta(t, 0.70, dc.anchor.retestClaim, 1.0e-6)

		dc.rates.smoothed = 0.76
		dc.rates.deviation = 0.001
		c.sample.previousHitRate = 0.76
		steadySample(c, 7500, 0.76)

		require.Equal(t, int64(7500), dc.anchor.window)
		require.Equal(t, -1.0, dc.anchor.retestClaim)
		require.Equal(t, 0, dc.anchor.settleLeft)
	})

	t.Run("guardRail_blindCorner_outranksTheVeto", func(t *testing.T) {
		t.Parallel()

		// A starved sample cannot judge a shortfall, so blindness routes to a probe before the
		// veto may fire, while an identically placed sighted sample vetoes.
		vetoed := seededShortfall()
		probed := seededShortfall()
		for range 3 {
			steadySample(vetoed, 1500, 0.66)
			steadySample(probed, 1500, 0.66)
		}
		require.False(t, vetoed.density.anchor.held)
		require.False(t, probed.density.anchor.held)

		steadySample(vetoed, 1500, 0.66)
		require.True(t, vetoed.density.anchor.held)
		require.Equal(t, int64(2000-1500), vetoed.adjustment)

		adjustment := sampleClimber(probed, 1500, 0, 660, 340)
		require.True(t, probed.density.walking)
		require.False(t, probed.density.anchor.held)
		require.Equal(t, int64(ctStride), adjustment)
	})
}

func TestWindowClimber_Anchor(t *testing.T) {
	t.Parallel()

	t.Run("anchor_ratchetsToClearlyBetterPosition_andReleasesPark", func(t *testing.T) {
		t.Parallel()

		// A smoothed rate a full margin above the anchor's claim moves the anchor here and releases
		// a park. The rate step that signals the improvement also widens the deviation, so the
		// ratchet waits for the smoothing to settle.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		require.Equal(t, int64(2000), dc.anchor.window)
		dc.anchor.held = true
		// An audit confirming here first would plant and park by itself, which would hide the
		// ratchet, so the clock is held back.
		dc.auditClock.stillSamples = 0

		parked := steadySample(c, 4000, 0.74)
		require.Equal(t, int64(0), parked)
		require.True(t, dc.anchor.held)

		ratchetedAt := -1
		for i := 2; i <= 20; i++ {
			steadySample(c, 4000, 0.74)
			if dc.anchor.window == 4000 {
				ratchetedAt = i
				break
			}
		}
		require.Greater(t, ratchetedAt, 2)
		require.LessOrEqual(t, ratchetedAt, 20)
		require.False(t, dc.anchor.held)
		require.InDelta(t, dc.rates.smoothed, dc.anchor.rate, 1.0e-9)
	})

	t.Run("anchor_midUndoDrain_doesNotPlantAtTheRetreatingWindow", func(t *testing.T) {
		t.Parallel()

		// A capped retreat passes through positions the probe was charged for rejecting, so an
		// unplanted anchor waits for it to land. The sample that empties the remainder is still
		// in transit.
		c := failingDownProbe()
		dc := &c.density
		sampleClimber(c, 2000, 200, 500, 300)
		require.False(t, dc.walking)
		require.Greater(t, dc.undoRemaining, int64(0))

		steadySample(c, 4457, 0.70)
		require.Greater(t, dc.undoRemaining, int64(0))
		require.False(t, dc.anchor.isPlanted())

		steadySample(c, 6914, 0.70)
		require.Equal(t, int64(0), dc.undoRemaining)
		require.False(t, dc.anchor.isPlanted())

		// The retreat has landed, so the position may be claimed.
		steadySample(c, 7000, 0.70)
		require.Equal(t, int64(7000), dc.anchor.window)
		require.InDelta(t, 0.70, dc.anchor.rate, 1.0e-9)
	})

	t.Run("anchor_midUndoDrain_doesNotRatchetOffTheProbeBase", func(t *testing.T) {
		t.Parallel()

		// A parked anchor at the probe's base must not move to wherever the retreat happens to be
		// and drop its park.
		c := failingDownProbe()
		dc := &c.density
		dc.anchor.plant(7000, 0.40)
		dc.anchor.park(auditWaitInitial)
		sampleClimber(c, 2000, 200, 500, 300)

		steadySample(c, 4457, 0.70)
		require.Equal(t, int64(7000), dc.anchor.window)
		require.Equal(t, 0.40, dc.anchor.rate)
		require.True(t, dc.anchor.held)

		// The re-sync does not wait: once the retreat is back inside the anchor's band the stale
		// claim is replaced by the live measurement.
		steadySample(c, 6914, 0.70)
		require.Equal(t, int64(0), dc.undoRemaining)
		require.Equal(t, int64(7000), dc.anchor.window)
		require.InDelta(t, 0.70, dc.anchor.rate, 1.0e-9)

		parked := steadySample(c, 7000, 0.70)
		require.Equal(t, int64(0), parked)
		require.Equal(t, int64(7000), dc.anchor.window)
		require.True(t, dc.anchor.held)
	})
}

func TestWindowClimber_AuditClock(t *testing.T) {
	t.Parallel()

	t.Run("auditClock_rateSwings_doNotResetStillness", func(t *testing.T) {
		t.Parallel()

		// Stillness is a property of the position, not the rate.
		c := makeTestClimber()
		dc := &c.density
		armed := false
		for i := range 2 * auditWaitInitial {
			hitRate := 0.60
			if i%2 == 0 {
				hitRate = 0.70
			}
			steadySample(c, 2000, hitRate)
			if dc.walking {
				armed = true
				break
			}
		}
		require.True(t, armed)
		require.True(t, walkOf(t, dc).isAudit)
	})

	t.Run("auditClock_firstSampleAfterAResize_isStillWhereverItRests", func(t *testing.T) {
		t.Parallel()

		// Nothing moves the window before the first sample closes, so that sample counts as still
		// wherever the resize left it.
		atSplit := makeTestClimber()
		steadySample(atSplit, 82, 0.70)
		require.Equal(t, 1, atSplit.density.auditClock.stillSamples)

		elsewhere := makeTestClimber()
		steadySample(elsewhere, 2000, 0.70)
		require.Equal(t, 1, elsewhere.density.auditClock.stillSamples)
	})

	t.Run("auditClock_overflow_remainsDue", func(t *testing.T) {
		t.Parallel()

		c := makeTestClimber()
		clock := &c.density.auditClock
		band := stableBand(ctMaximum)
		clock.tick(2000, band)

		// Stationary samples at the boundary must leave the audit due without wrapping.
		clock.stillSamples = math.MaxInt32 - 1
		require.True(t, clock.isDue())
		clock.tick(2000, band)
		require.Equal(t, math.MaxInt32, clock.stillSamples)

		clock.tick(2000, band)
		require.Equal(t, math.MaxInt32, clock.stillSamples)
		require.True(t, clock.isDue())

		clock.tick(4000, band)
		require.Equal(t, math.MaxInt32-1, clock.stillSamples)
		require.True(t, clock.isDue())
	})

	t.Run("auditClock_restart_makesTheNextAuditWaitOutTheStillnessAgain", func(t *testing.T) {
		t.Parallel()

		// Arming an audit restarts the stillness run; otherwise an audit that ends quickly would be
		// due again immediately.
		c := makeTestClimber()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		armedAt := -1
		for i := 1; i <= 4*auditWaitInitial; i++ {
			steadySample(c, 2000, 0.70)
			if dc.walking {
				armedAt = i
				break
			}
		}
		require.GreaterOrEqual(t, armedAt, auditWaitInitial)
		// The arm restarts the run and this sample's own tick is the first of the new one.
		require.Equal(t, 1, dc.auditClock.stillSamples)

		// A crash-scale drop ends the walk on its first sample.
		steadySample(c, 2000+c.adjustment, 0.40)
		require.False(t, dc.walking)

		rearmedAt := -1
		for i := 1; i <= 4*auditWaitInitial; i++ {
			steadySample(c, 2000, 0.40)
			if dc.walking {
				rearmedAt = i
				break
			}
		}
		require.GreaterOrEqual(t, rearmedAt, probeBackoffInitial)
	})

	t.Run("auditClock_stillnessToleratesTheOrbit", func(t *testing.T) {
		t.Parallel()

		// An orbit within the band accumulates toward the audit, while a wander beyond it on every
		// sample starves the clock.
		orbiting := makeTestClimber()
		armed := false
		for i := range 2 * auditWaitInitial {
			windowMax := int64(2163)
			if i%2 == 0 {
				windowMax = 2000
			}
			steadySample(orbiting, windowMax, 0.70)
			if orbiting.density.walking {
				armed = true
				break
			}
		}
		require.True(t, armed)

		wandering := makeTestClimber()
		for i := range 2 * auditWaitInitial {
			windowMax := int64(2164)
			if i%2 == 0 {
				windowMax = 2000
			}
			steadySample(wandering, windowMax, 0.70)
		}
		require.False(t, wandering.density.walking)
	})

	t.Run("auditClock_periodicSuperBandMotion_decaysInsteadOfResetting", func(t *testing.T) {
		t.Parallel()

		// A workload that moves the window past the band once per wait would hold a hard-reset
		// clock at zero forever; a moving sample decays the run instead.
		c := makeTestClimber()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		armed := false
		for i := range 3 * auditWaitInitial {
			windowMax := int64(2400)
			if (i/8)%2 == 0 {
				windowMax = 2000
			}
			steadySample(c, windowMax, 0.70)
			if dc.walking {
				armed = true
				break
			}
		}
		require.True(t, armed)
		require.True(t, walkOf(t, dc).isAudit)
	})

	t.Run("auditClock_halfMotionAlternation_neverAccumulates", func(t *testing.T) {
		t.Parallel()

		// Motion on half the samples is net-zero drift, so the clock never accumulates to an arm.
		c := makeTestClimber()
		dc := &c.density
		dc.auditClock.waitSamples = auditWaitInitial
		for i := range 8 * auditWaitInitial {
			windowMax := int64(2400)
			if (i/2)%2 == 0 {
				windowMax = 2000
			}
			steadySample(c, windowMax, 0.70)
			require.False(t, dc.walking, "sample %d", i)
		}
	})
}

func TestWindowClimber_Audit(t *testing.T) {
	t.Parallel()

	t.Run("audit_freshPark_shieldLastsItsWholeSchedule", func(t *testing.T) {
		t.Parallel()

		// A confirmation parks for exactly one initial audit wait: a shorter shield releases a
		// validated position into the first squall, and one that never ages holds a position the
		// workload has left.
		c := armAudit(t, 2000)
		dc := &c.density
		walked := 2000 + c.adjustment
		for i := 0; dc.walking && i < probeWalkBudget; i++ {
			walked += steadySample(c, walked, 0.74)
		}
		require.True(t, dc.anchor.held)
		require.Equal(t, auditWaitInitial, dc.anchor.freshLeft)

		// Alternating crash-scale swings would stand the anchor down on every sample but for the
		// shield, so the release lands on the sample that spends its last count.
		releasedAt := -1
		for i := 1; i <= 2*auditWaitInitial; i++ {
			hitRate := 0.74
			if i%2 == 0 {
				hitRate = 0.80
			}
			steadySample(c, walked, hitRate)
			if !dc.anchor.held {
				releasedAt = i
				break
			}
		}
		require.Equal(t, auditWaitInitial, releasedAt)
		require.Equal(t, 0, dc.anchor.freshLeft)
	})

	t.Run("audit_armsAtQuietEquilibrium_andBacksOffOnFailure", func(t *testing.T) {
		t.Parallel()

		// The first audit is the cold-start calibration and arms after a short wait; an
		// unconfirmed audit then retries on the ladder's cadence.
		c := makeTestClimber()
		dc := &c.density
		armedAt := -1
		for i := range 80 {
			steadySample(c, 2000, 0.70)
			if dc.walking {
				armedAt = i
				break
			}
		}
		require.GreaterOrEqual(t, armedAt, auditWaitFirst-1)
		require.Less(t, armedAt, auditWaitInitial-1)
		w := walkOf(t, dc)
		require.True(t, w.isAudit)
		require.True(t, w.down)
		require.Equal(t, int64(-ctStride), c.adjustment)

		// The audit is judged by hit rate, not density: it walks its whole budget while the rate
		// fails to clear the margin, then undoes in full.
		walked := 2000 + c.adjustment
		for range 2 * probeWalkBudget {
			walked += steadySample(c, walked, 0.70)
			if !dc.walking && dc.undoRemaining == 0 {
				break
			}
		}
		require.False(t, dc.walking)
		require.Equal(t, int64(2000), walked)
		require.Equal(t, dc.audit.rung, dc.auditClock.waitSamples)
		require.Equal(t, 2*probeBackoffInitial, dc.auditClock.waitSamples)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("audit_sustainedImprovement_confirmsAndParks", func(t *testing.T) {
		t.Parallel()

		// An audit that finds a sustained improvement keeps the position and parks there, since
		// density disagreed with the position by construction.
		c := makeTestClimber()
		dc := &c.density
		for range auditWaitInitial + 2 {
			steadySample(c, 2000, 0.70)
			if dc.walking {
				break
			}
		}
		require.True(t, walkOf(t, dc).isAudit)
		dc.anchor.held = true

		walked := 2000 + c.adjustment
		for range probeWalkBudget {
			walked += steadySample(c, walked, 0.74)
			if !dc.walking {
				break
			}
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.NotEqual(t, int64(2000), walked)
		require.Equal(t, auditWaitInitial, dc.auditClock.waitSamples)
		require.Equal(t, 1, dc.starvation.rung)

		// The validated position becomes the anchor at once and the park holds it.
		require.Equal(t, walked, dc.anchor.window)
		require.InDelta(t, dc.rates.smoothed, dc.anchor.rate, 1.0e-9)
		held := steadySample(c, walked, 0.74)
		require.Equal(t, int64(0), held)
	})

	t.Run("audit_confirmSample_takesNoSteeringStep", func(t *testing.T) {
		t.Parallel()

		// A parting density step on the confirming sample would move the window off the position
		// the anchor now defends.
		c := armAudit(t, 2000)
		dc := &c.density
		walked := 2000 + c.adjustment
		confirmStep := int64(-1)
		for i := 0; dc.walking && i < 2*probeWalkBudget; i++ {
			confirmStep = sampleClimber(c, walked, 600, 140, 260)
			walked += confirmStep
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, int64(0), confirmStep)
		require.Equal(t, walked, dc.anchor.window)
	})

	t.Run("audit_interruptedImprovement_failsTheWalk", func(t *testing.T) {
		t.Parallel()

		// Confirmation needs consecutive raw samples above the reference; scattered samples whose
		// average clears the bar cannot confirm.
		c := armAudit(t, 2000)
		dc := &c.density
		cycle := []float64{0.74, 0.74, 0.74, 0.703}
		walked := 2000 + c.adjustment
		for i := 0; dc.walking && i < 2*probeWalkBudget; i++ {
			walked += steadySample(c, walked, cycle[i%len(cycle)])
		}
		require.False(t, dc.walking)
		require.Equal(t, 2*probeBackoffInitial, dc.audit.rung)
	})

	t.Run("audit_walkThatNeverBeatsItsStart_cannotConfirm", func(t *testing.T) {
		t.Parallel()

		// The smoothed reference can be colder than the walk, so confirmation also requires the
		// walk to match the raw rate it started from at least once.
		c := makeTestClimber()
		dc := &c.density
		for range auditWaitInitial + 2 {
			steadySample(c, 2000, 0.70)
			if dc.walking {
				break
			}
		}
		require.True(t, walkOf(t, dc).isAudit)
		require.Equal(t, 0.70, rebaseReference(t, c, 0.40).baseHitRate)

		walked := 2000 + c.adjustment
		for i := 0; dc.walking && i < 2*probeWalkBudget; i++ {
			walked += steadySample(c, walked, 0.69)
		}
		require.False(t, dc.walking)
		require.False(t, dc.anchor.held)
		require.Equal(t, 2*probeBackoffInitial, dc.audit.rung)
	})

	t.Run("audit_walkThatMatchesASaturatedStart_stillConfirms", func(t *testing.T) {
		t.Parallel()

		// The starting-rate test is inclusive, or a saturated arming sample would make every later
		// confirmation impossible.
		c := makeTestClimber()
		dc := &c.density
		for range auditWaitInitial + 2 {
			steadySample(c, 2000, 1.0)
			if dc.walking {
				break
			}
		}
		require.True(t, walkOf(t, dc).isAudit)
		require.Equal(t, 1.0, rebaseReference(t, c, 0.40).baseHitRate)

		walked := 2000 + c.adjustment
		for i := 0; dc.walking && i < probeWalkBudget; i++ {
			walked += steadySample(c, walked, 1.0)
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
	})

	t.Run("audit_freshPark_ridesOutCrashWeather", func(t *testing.T) {
		t.Parallel()

		// A just-confirmed park survives crash-scale swings for one initial audit wait; after the
		// shield lapses, the stand-down releases as before.
		c := armAudit(t, 2000)
		dc := &c.density
		walked := 2000 + c.adjustment
		for i := 0; dc.walking && i < probeWalkBudget; i++ {
			walked += steadySample(c, walked, 0.74)
		}
		require.True(t, dc.anchor.held)
		require.Equal(t, walked, dc.anchor.window)

		held := steadySample(c, walked, 0.80)
		require.Equal(t, int64(0), held)
		require.True(t, dc.anchor.held)
		require.Equal(t, walked, dc.anchor.window)

		dc.anchor.freshLeft = 0
		steadySample(c, walked, 0.74)
		// The discarded anchor re-plants at the freed position in the same sample, so the release
		// is observable through the hold alone.
		require.False(t, dc.anchor.held)
		require.Equal(t, 0, dc.anchor.freshLeft)
	})

	t.Run("audit_confirmUsesReferenceFrozenAtArm", func(t *testing.T) {
		t.Parallel()

		// A mid-walk move of the smoothed rate or the anchor must not move the audit's bar.
		c := makeTestClimber()
		dc := &c.density
		for range auditWaitInitial + 2 {
			steadySample(c, 2000, 0.70)
			if dc.walking {
				break
			}
		}
		require.True(t, walkOf(t, dc).isAudit)
		dc.rates.smoothed = 0.99
		dc.anchor.rate = 0.99

		walked := 2000 + c.adjustment
		confirmed := false
		for range probeWalkBudget {
			walked += steadySample(c, walked, 0.74)
			if !dc.walking {
				confirmed = dc.starvation.rung == 1
				break
			}
		}
		require.True(t, confirmed)
	})

	t.Run("audit_afterARegimeShift_confirmsAgainstTheNewRegime", func(t *testing.T) {
		t.Parallel()

		// A reference smoothed across a crash-scale shift would leave a claim no position of the
		// new regime could clear, failing every later audit.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		steadySample(c, 2000, 0.20)

		armed := false
		for range 2 * auditWaitInitial {
			steadySample(c, 5000, 0.20)
			if dc.walking {
				armed = true
				break
			}
		}
		require.True(t, armed)
		require.True(t, walkOf(t, dc).isAudit)
		require.Less(t, walkOf(t, dc).baseSmoothedRate, 0.25)

		// A position the new regime can hold clears the bar and is kept.
		walked := 5000 + c.adjustment
		for range probeWalkBudget {
			walked += steadySample(c, walked, 0.24)
			if !dc.walking {
				break
			}
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, walked, dc.anchor.window)
	})

	t.Run("audit_afterAnAwayShift_confirmsAgainstTheNewRegime", func(t *testing.T) {
		t.Parallel()

		// The window rests off the anchor when the regime shifts, so the guard keeps the claim. The
		// walk is measured against the rate it left rather than that claim, and confirms.
		c := makeTestClimber()
		dc := &c.density
		for range 30 {
			steadySample(c, 2000, 0.70)
		}
		for range 4 {
			steadySample(c, 5000, 0.70)
		}
		steadySample(c, 5000, 0.20)
		require.Equal(t, int64(2000), dc.anchor.window)
		require.InDelta(t, 0.70, dc.anchor.rate, 0.01)

		// The clock is not due at the shift, so the smoothing has learned the regime by the time
		// the audit arms.
		dc.auditClock.restart()
		armed := false
		for range 2 * auditWaitInitial {
			steadySample(c, 5000, 0.20)
			if dc.walking {
				armed = true
				break
			}
		}
		require.True(t, armed)
		require.True(t, walkOf(t, dc).isAudit)
		require.InDelta(t, 0.70, dc.anchor.rate, 0.01)
		require.Less(t, walkOf(t, dc).baseSmoothedRate, 0.25)

		walked := 5000 + c.adjustment
		for range probeWalkBudget {
			walked += steadySample(c, walked, 0.24)
			if !dc.walking {
				break
			}
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, walked, dc.anchor.window)
	})

	t.Run("audit_retryAfterConfirm_flooredAtInitialBackoff", func(t *testing.T) {
		t.Parallel()

		// The audit clock is paced by the audit's own rung, so a starvation ladder cheapened by a
		// confirmation cannot leak a short retry cadence into the audit schedule.
		c := makeTestClimber()
		dc := &c.density
		dc.starvation.rung = 1
		w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
		w.samples = probeWalkBudget
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		steadySample(c, 1800, 0.70)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, 2*probeBackoffInitial, dc.audit.rung)
		require.Equal(t, 2*probeBackoffInitial, dc.auditClock.waitSamples)
	})

	t.Run("audit_directionByPosition_needsRoomToStride", func(t *testing.T) {
		t.Parallel()

		// An audit refuses a direction with less than one stride of room; a walk sent into a nearer
		// wall would stand still for its budget and be charged as a failure.
		low := armAudit(t, 300)
		require.False(t, walkOf(t, &low.density).down)
		require.Equal(t, int64(ctStride), low.adjustment)

		atTwiceTheFloor := armAudit(t, 327)
		require.False(t, walkOf(t, &atTwiceTheFloor.density).down)

		interior := armAudit(t, 328)
		require.False(t, walkOf(t, &interior.density).down)

		lastWithoutRoom := armAudit(t, int64(ctFloor+ctStride))
		require.False(t, walkOf(t, &lastWithoutRoom.density).down)

		firstWithRoom := armAudit(t, int64(ctFloor+ctStride)+1)
		require.True(t, walkOf(t, &firstWithRoom.density).down)

		high := armAudit(t, 6500)
		require.True(t, walkOf(t, &high.density).down)
		require.Equal(t, int64(-ctStride), high.adjustment)
	})

	t.Run("audit_direction_roomIsMeasuredAgainstTheRungsOwnStride", func(t *testing.T) {
		t.Parallel()

		// At the deepest rung a walk strides four times the base magnitude, so room for the base
		// stride alone must be refused.
		windowMax := int64(ctFloor + 3*ctStride)

		deep := armAuditAtRung(t, windowMax, probeBackoffMax)
		require.False(t, walkOf(t, &deep.density).down)

		shallow := armAuditAtRung(t, windowMax, probeBackoffInitial)
		require.True(t, walkOf(t, &shallow.density).down)
		require.Equal(t, int64(-ctStride), shallow.adjustment)
	})

	t.Run("audit_direction_roomRefusalOverridesAlternation", func(t *testing.T) {
		t.Parallel()

		// Near a rail, alternation would point every other audit into the wall.
		c := makeTestClimber()
		dc := &c.density
		audits := 0
		for i := 0; i < 300 && audits < 2; i++ {
			steadySample(c, 500, 0.70)
			if dc.walking && dc.walk.samples == 1 {
				audits++
				require.True(t, dc.walk.isAudit)
				require.False(t, dc.walk.down)
			}
		}
		require.Equal(t, 2, audits)
	})

	t.Run("audit_streakBoundary_confirmsAtExactlyFourConsecutive", func(t *testing.T) {
		t.Parallel()

		// An interrupted opening resets the run, and the confirmation fires on the fourth
		// consecutive above-reference sample.
		c := armAudit(t, 2000)
		dc := &c.density
		rates := []float64{0.703, 0.74, 0.74, 0.74, 0.74}
		walked := 2000 + c.adjustment
		for i, rate := range rates {
			walked += steadySample(c, walked, rate)
			if i < len(rates)-1 {
				require.True(t, dc.walking, "sample %d", i)
			}
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
	})

	t.Run("audit_direction_ceilingSideRoomRefusal", func(t *testing.T) {
		t.Parallel()

		// Within one stride of the upper corner, alternation's up-turn is refused back downward.
		c := makeTestClimber()
		dc := &c.density
		audits := 0
		for i := 0; i < 300 && audits < 2; i++ {
			steadySample(c, 5800, 0.70)
			if dc.walking && dc.walk.samples == 1 {
				audits++
				require.True(t, dc.walk.isAudit)
				require.True(t, dc.walk.down)
			}
		}
		require.Equal(t, 2, audits)
	})

	t.Run("audit_ceilingDirection_isSticky", func(t *testing.T) {
		t.Parallel()

		// A position past the upper corner is forced downward on every audit.
		c := makeTestClimber()
		dc := &c.density
		audits := 0
		for i := 0; i < 150 && audits < 2; i++ {
			steadySample(c, 6500, 0.70)
			if dc.walking && dc.walk.samples == 1 {
				audits++
				require.True(t, dc.walk.isAudit)
				require.True(t, dc.walk.down)
			}
		}
		require.Equal(t, 2, audits)
	})

	t.Run("audit_interiorDirection_alternatesAcrossAudits", func(t *testing.T) {
		t.Parallel()

		// Interior positions are searched in both directions, so an escape opposite the first walk
		// is still found.
		c := makeTestClimber()
		dc := &c.density
		windowMax := int64(2000)
		firstDown := false
		audits := 0
		for i := 0; i < 200 && audits < 2; i++ {
			if dc.walking && dc.walk.isAudit && dc.walk.samples == 1 {
				audits++
				if audits == 1 {
					firstDown = dc.walk.down
				} else {
					require.Equal(t, !firstDown, dc.walk.down)
				}
			}
			adjustment := steadySample(c, windowMax, 0.70)
			windowMax = max(164, windowMax+adjustment)
			if !dc.walking && dc.undoRemaining == 0 && !dc.anchor.returning {
				windowMax = 2000
			}
		}
		require.Equal(t, 2, audits)
	})

	t.Run("audit_parksFirstAudit_followsTheConfirmedWalk", func(t *testing.T) {
		t.Parallel()

		// A confirmation stops a walk that was still improving, so the park's first audit continues
		// in that direction rather than retracing the walk.
		c := confirmedAuditPark(t)
		dc := &c.density
		require.Less(t, dc.anchor.window, int64(5000))
		armNextAudit(t, c, dc.anchor.window, 0.74)
		require.True(t, walkOf(t, dc).down)

		// The audit after that alternates.
		settled := confirmedAuditPark(t)
		sdc := &settled.density
		parked := sdc.anchor.window
		armNextAudit(t, settled, parked, 0.74)
		for i := 0; i < probeWalkBudget && sdc.walking; i++ {
			steadySample(settled, parked, 0.74)
		}
		require.False(t, sdc.walking)
		sdc.refractoryLeft = 0
		sdc.undoRemaining = 0
		armNextAudit(t, settled, parked, 0.74)
		require.False(t, walkOf(t, sdc).down)
	})

	t.Run("audit_parksFirstAudit_alternatesWhenTheClaimMoved", func(t *testing.T) {
		t.Parallel()

		// A smoothed rate that moved by a restart threshold says the workload moved, so the walk's
		// direction says nothing about the terrain. So does a park released before its first audit.
		trended := confirmedAuditPark(t)
		armNextAudit(t, trended, trended.density.anchor.window, 0.74+climberRestartThreshold+0.01)
		require.False(t, walkOf(t, &trended.density).down)

		released := confirmedAuditPark(t)
		parked := released.density.anchor.window
		released.density.anchor.release()
		armNextAudit(t, released, parked, 0.74)
		require.False(t, walkOf(t, &released.density).down)
	})

	t.Run("audit_commitmentDepth_blocksAnImmediateConfirm", func(t *testing.T) {
		t.Parallel()

		// An audit may not confirm before its committed depth even when every stride clears the
		// reference.
		c := armAudit(t, 2000)
		dc := &c.density
		walked := 2000 + c.adjustment
		for depth := 1; depth <= auditCommitment; depth++ {
			adjustment := steadySample(c, walked, 0.78)
			walked = max(164, walked+adjustment)
			if depth < auditCommitment {
				require.True(t, dc.walking, "depth %d", depth)
			}
		}
		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, 1, dc.starvation.rung)
	})

	t.Run("audit_confirm_parksAtTheWalksBestSample", func(t *testing.T) {
		t.Parallel()

		// The walk strides on while collecting its streak, so a crest crossed on the way is behind
		// the window by the time of the verdict; the window returns to the best position.
		c := makeTestClimber()
		dc := &c.density
		injectWalk(dc, true, false, 2000, 0.70, 0.40)
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		climberStepOf(c).size = ctStride

		rates := []float64{0.80, 0.78, 0.77, 0.76, 0.755, 0.75}
		confirmStep := int64(0)
		for i, rate := range rates {
			confirmStep = steadySample(c, 2200+200*int64(i), rate)
		}

		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, int64(2200), dc.anchor.window)
		// A return within one step arrives on the confirming sample.
		require.Equal(t, int64(2200-3200), confirmStep)
		require.False(t, dc.anchor.returning)
	})

	t.Run("audit_confirm_skipsANegativeBestSample", func(t *testing.T) {
		t.Parallel()

		// A transient negative policy weight can report the window below zero, and the verdict must
		// not send the window there.
		c := makeTestClimber()
		dc := &c.density
		for range auditWaitInitial + 2 {
			steadySample(c, 4096, 0.50)
			if dc.walking {
				break
			}
		}
		require.True(t, walkOf(t, dc).isAudit)

		sampleClimber(c, -100, 0, 600, 400)
		for i := 0; i < probeWalkBudget && dc.walking; i++ {
			steadySample(c, 3000, 0.58)
		}

		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, int64(3000), dc.anchor.window)
	})

	t.Run("audit_confirm_onAnImprovingWalk_parksWhereItStopped", func(t *testing.T) {
		t.Parallel()

		// A walk still improving has its best sample last, and a flat plateau's best sample is
		// noise that the verdict's margin refuses: both park where the walk ended.
		walks := [][]float64{
			{0.72, 0.73, 0.74, 0.75, 0.76, 0.80},
			{0.75, 0.755, 0.75, 0.752, 0.75, 0.753},
		}
		for _, rates := range walks {
			c := makeTestClimber()
			dc := &c.density
			injectWalk(dc, true, false, 2000, 0.70, 0.40)
			dc.rates.smoothed = 0.70
			c.sample.previousHitRate = 0.70
			climberStepOf(c).size = ctStride

			confirmStep := int64(0)
			for i, rate := range rates {
				confirmStep = steadySample(c, 2200+200*int64(i), rate)
			}

			require.False(t, dc.walking, "rates %v", rates)
			require.Equal(t, int64(3200), dc.anchor.window, "rates %v", rates)
			require.False(t, dc.anchor.returning, "rates %v", rates)
			require.Equal(t, int64(0), confirmStep, "rates %v", rates)
		}
	})

	t.Run("audit_retreatLandingAtThePark_keepsTheAnchor", func(t *testing.T) {
		t.Parallel()

		// The retreat restores the rate the walk had spent, a crash-scale move at the anchor's own
		// position. It is the climber's own move, so the park survives.
		c := armAudit(t, 2000)
		dc := &c.density
		dc.anchor.held = true
		dc.anchor.freshLeft = 0
		dc.anchor.plant(2000, 0.70)
		walked := 2000 + c.adjustment

		undo := steadySample(c, walked, 0.50)
		require.False(t, dc.walking)
		require.Equal(t, 2000-walked, undo)
		require.Equal(t, retreatCover, dc.retreatLeft)

		// The landing sample: the rate returns to what the park earns.
		held := steadySample(c, 2000, 0.70)
		require.Equal(t, int64(0), held)
		require.True(t, dc.anchor.held)
		require.Equal(t, int64(2000), dc.anchor.window)
		require.Equal(t, retreatCover-1, dc.retreatLeft)

		// The cover runs out, and the next crash-scale move is the workload's again.
		steadySample(c, 2000, 0.70)
		require.Equal(t, 0, dc.retreatLeft)
		steadySample(c, 2000, 0.30)
		require.False(t, dc.anchor.isPlanted())
	})

	t.Run("audit_deepestRungFailure_doublesTheWaitToItsCap", func(t *testing.T) {
		t.Parallel()

		// Only a failure at the deepest rung doubles the audit clock, up to the cap.
		expected := []int{2 * probeBackoffMax, 4 * probeBackoffMax, auditWaitMax, auditWaitMax}
		c := makeTestClimber()
		dc := &c.density
		dc.auditClock.waitSamples = probeBackoffMax
		for _, expectedWait := range expected {
			w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
			w.samples = probeWalkBudget
			dc.rates.smoothed = 0.70
			c.sample.previousHitRate = 0.70
			dc.audit.rung = probeBackoffMax
			steadySample(c, 1800, 0.70)

			require.False(t, dc.walking)
			require.Equal(t, expectedWait, dc.auditClock.waitSamples)
			steadySample(c, 2000, 0.70)
		}
	})

	t.Run("audit_deepestRungCrash_retriesOnTheLadderCadence", func(t *testing.T) {
		t.Parallel()

		// A lone crash is priced as a workload shift, so it keeps the ladder's cadence even at the
		// deepest rung, and a first crash aborts on its first below-bar sample.
		c := makeTestClimber()
		dc := &c.density
		dc.auditClock.waitSamples = 4 * probeBackoffMax
		w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
		w.samples = probeWalkBudget - 10
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		dc.audit.rung = probeBackoffMax
		steadySample(c, 1800, 0.60)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.audit.crashStreak)
		require.Equal(t, probeBackoffMax, dc.audit.rung)
		require.Equal(t, probeBackoffMax, dc.auditClock.waitSamples)
	})

	t.Run("audit_deepestRungEscalatedCrash_keepsTheLadderCadence", func(t *testing.T) {
		t.Parallel()

		// Repeated crashes escalate the audit's rung like a failure but never double the clock. The
		// prior crash also arms tolerance: the retry rides out two below-bar samples and aborts on
		// the third.
		c := makeTestClimber()
		dc := &c.density
		dc.auditClock.waitSamples = probeBackoffMax
		dc.audit.crashStreak = 1
		w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
		w.samples = probeWalkBudget - 10
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		dc.starvation.rung = probeBackoffInitial
		dc.audit.rung = probeBackoffMax
		steadySample(c, 1800, 0.60)
		require.True(t, dc.walking)
		steadySample(c, 1800, 0.60)
		require.True(t, dc.walking)
		steadySample(c, 1800, 0.60)

		require.False(t, dc.walking)
		require.Equal(t, 2, dc.audit.crashStreak)
		require.Equal(t, probeBackoffMax, dc.audit.rung)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, probeBackoffMax, dc.auditClock.waitSamples)
	})

	t.Run("audit_loneCrashTrain_neverDefersBeyondTheLadder", func(t *testing.T) {
		t.Parallel()

		// Three audits, each killed by an exogenous pulse: the crash streak deepens the audit rung
		// to its cap, but the wait never doubles and the starvation ladder never moves.
		c := makeTestClimber()
		dc := &c.density
		expectedRung := []int{probeBackoffInitial, 2 * probeBackoffInitial, probeBackoffMax}
		for crash := range 3 {
			w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
			w.samples = probeWalkBudget - 10
			dc.rates.smoothed = 0.70
			c.sample.previousHitRate = 0.70
			samples := auditCrashPersistence
			if crash == 0 {
				samples = 1
			}
			for range samples {
				steadySample(c, 1800, 0.60)
			}
			require.False(t, dc.walking, "crash %d", crash)
			require.Equal(t, min(crash+1, probeCrashEscalation), dc.audit.crashStreak, "crash %d", crash)
			require.Equal(t, expectedRung[crash], dc.audit.rung, "crash %d", crash)
			require.LessOrEqual(t, dc.auditClock.waitSamples, probeBackoffMax, "crash %d", crash)
			require.Equal(t, probeBackoffInitial, dc.starvation.rung, "crash %d", crash)
		}
	})

	t.Run("audit_toleratedDip_holdsTheWalkDirection", func(t *testing.T) {
		t.Parallel()

		// While a dip is being judged, the walk neither crashes nor reverses; a reversal through
		// the base would turn the tolerated dip into a failure.
		c := makeTestClimber()
		dc := &c.density
		dc.audit.crashStreak = 1
		w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
		w.samples = probeWalkBudget - 10
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		climberStepOf(c).size = -ctStride
		step := steadySample(c, 1800, 0.55)

		requireSameWalk(t, dc, w)
		require.Equal(t, 1, w.belowBarStreak)
		require.Less(t, step, int64(0))
	})

	t.Run("audit_lowBaseRate_crashAbortsOnAProportionalDrop", func(t *testing.T) {
		t.Parallel()

		// An absolute threshold wider than the starting rate could never be crossed.
		c := makeTestClimber()
		dc := &c.density
		injectWalk(dc, true, false, 1024, 0.04, 0.0)
		c.sample.previousHitRate = 0.04

		// 0.033 falls past 0.04 - (auditBarFraction * 0.04) but not past 0.04 - 0.05.
		sampleClimber(c, 2048, 20, 13, 967)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.audit.crashStreak)
	})

	t.Run("audit_highBaseRate_crashBarStaysAbsolute", func(t *testing.T) {
		t.Parallel()

		// The proportional threshold applies only where the absolute one exceeds it.
		c := makeTestClimber()
		dc := &c.density
		injectWalk(dc, true, false, 1024, 0.60, 0.0)
		c.sample.previousHitRate = 0.60

		// 0.53 falls past the absolute 5pp but not past the proportional 9pp.
		sampleClimber(c, 2048, 280, 250, 470)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.audit.crashStreak)
	})

	t.Run("audit_noisyLowRate_crashBarStaysOnTheFrozenRate", func(t *testing.T) {
		t.Parallel()

		// The noise term belongs to the reversal alone, so a drop past the fraction of the
		// starting rate aborts however noisy the workload is.
		c := makeTestClimber()
		dc := &c.density
		injectWalk(dc, true, false, 1024, 0.04, 0.0)
		dc.rates.smoothed = 0.04
		dc.rates.deviation = 0.05
		c.sample.previousHitRate = 0.04

		sampleClimber(c, 2048, 20, 13, 967)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.audit.crashStreak)
	})

	t.Run("audit_walkThroughValley_confirmsPastTheDip", func(t *testing.T) {
		t.Parallel()

		// On the retry of a crashing equilibrium two below-bar samples are tolerated, the far side
		// resets the persistence counter, and the walk survives to confirm.
		c := makeTestClimber()
		dc := &c.density
		dc.audit.crashStreak = 1
		w := injectWalk(dc, true, false, 2000, 0.70, 0.40)
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		climberStepOf(c).size = ctStride
		steadySample(c, 2200, 0.55)
		requireSameWalk(t, dc, w)
		steadySample(c, 2400, 0.56)
		requireSameWalk(t, dc, w)
		require.Equal(t, 2, w.belowBarStreak)
		steadySample(c, 2600, 0.80)
		require.Equal(t, 0, w.belowBarStreak)
		for i := 0; dc.walking && i < probeWalkBudget; i++ {
			steadySample(c, 2800, 0.80)
		}

		require.False(t, dc.walking)
		require.True(t, dc.anchor.held)
		require.Equal(t, 0, dc.audit.crashStreak)
		require.Equal(t, auditWaitInitial, dc.auditClock.waitSamples)
	})

	t.Run("audit_budgetExpiry_leavesTheStarvationLedger", func(t *testing.T) {
		t.Parallel()

		// An audit's endings clear only its own crash streak.
		c := makeTestClimber()
		dc := &c.density
		dc.starvation.crashStreak = 1
		dc.audit.crashStreak = 1
		w := injectWalk(dc, true, true, 2000, 0.70, 0.99)
		w.samples = probeWalkBudget
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		steadySample(c, 1800, 0.70)

		require.False(t, dc.walking)
		require.Equal(t, 0, dc.audit.crashStreak)
		require.Equal(t, 1, dc.starvation.crashStreak)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("audit_sightedFalseEquilibrium_armsWithoutStarvation", func(t *testing.T) {
		t.Parallel()

		// A thin trickle keeps the window sighted at the floor, steering holds it there, no
		// starvation is declared, and the stillness clock arms an upward audit anyway.
		c := makeTestClimber()
		dc := &c.density
		armedAt := -1
		for i := range 2 * auditWaitInitial {
			adjustment := sampleClimber(c, 164, 5, 595, 400)
			if dc.walking {
				armedAt = i
				require.True(t, walkOf(t, dc).isAudit)
				break
			}
			require.Equal(t, int64(0), adjustment)
		}
		require.GreaterOrEqual(t, armedAt, auditWaitFirst-1)
		require.False(t, walkOf(t, dc).down)
		require.Equal(t, int64(ctStride), c.adjustment)
	})

	t.Run("audit_walkStep_reversalStaysAbsolute", func(t *testing.T) {
		t.Parallel()

		// An audit reverses on the absolute 5pp: the same 7pp drop that a starvation probe walks
		// through turns an audit around.
		c := makeTestClimber()
		dc := &c.density
		w := injectWalk(dc, true, false, 1024, 0.60, 0.99)
		w.samples = probeWalkBudget - 10
		climberStepOf(c).size = ctStride
		dc.rates.smoothed = 0.70
		c.sample.previousHitRate = 0.70
		adjustment := sampleClimber(c, 2560, 5, 625, 370)

		require.True(t, dc.walking)
		require.Equal(t, int64(-ctStride), adjustment)
	})

	t.Run("audit_walkStep_reversalIsPricedAboveTheNoise", func(t *testing.T) {
		t.Parallel()

		// The same 1pp drop reverses the walk on a quiet workload and is no evidence on a noisy one.
		for _, noisy := range []bool{false, true} {
			c := makeTestClimber()
			dc := &c.density
			w := injectWalk(dc, true, false, 1024, 0.04, 0.99)
			w.samples = probeWalkBudget - 10
			climberStepOf(c).size = ctStride
			dc.rates.smoothed = 0.05
			dc.rates.deviation = 0.001
			if noisy {
				dc.rates.deviation = 0.05
			}
			c.sample.previousHitRate = 0.05
			adjustment := sampleClimber(c, 2560, 10, 30, 960)

			require.True(t, dc.walking, "noisy %v", noisy)
			require.Equal(t, noisy, adjustment > 0, "noisy %v", noisy)
		}
	})

	t.Run("audit_walkFromPark_crashScaleMove_keepsThePark", func(t *testing.T) {
		t.Parallel()

		// An audit's walk out of a park is the park's own test, so its crash-scale move is not read
		// as a workload shift.
		c := armAudit(t, 2000)
		dc := &c.density
		dc.anchor.held = true
		dc.anchor.freshLeft = 0
		walked := 2000 + c.adjustment
		undo := steadySample(c, walked, 0.50)
		require.False(t, dc.walking)
		require.Equal(t, 2000-walked, undo)
		require.True(t, dc.anchor.held)
		require.Equal(t, int64(2000), dc.anchor.window)
	})
}

func TestWindowClimber_WalkStep(t *testing.T) {
	t.Parallel()

	t.Run("walkStep_reversalThroughBase_leavesTheOtherLayersLedger", func(t *testing.T) {
		t.Parallel()

		// A reversal through the walk's own start retires only the owning layer's crash streak.
		for _, audit := range []bool{false, true} {
			c := makeTestClimber()
			dc := &c.density
			dc.starvation.crashStreak = 1
			dc.audit.crashStreak = 1
			w := injectWalk(dc, audit, false, 2000, 0.70, 0.99)
			w.samples = 1
			dc.rates.smoothed = 0.70
			dc.rates.deviation = 0.001
			c.sample.previousHitRate = 0.70
			climberStepOf(c).size = -ctStride
			sampleClimber(c, 2000, 10, 690, 300)

			wantAudit, wantStarvation := 1, 0
			if audit {
				wantAudit, wantStarvation = 0, 1
			}
			require.False(t, dc.walking, "audit %v", audit)
			require.Equal(t, wantAudit, dc.audit.crashStreak, "audit %v", audit)
			require.Equal(t, wantStarvation, dc.starvation.crashStreak, "audit %v", audit)
		}
	})

	t.Run("walkStep_floorBasedWalk_endsAtBudgetWithAFullUndo", func(t *testing.T) {
		t.Parallel()

		// A walk based just below the floor cannot end through its own base: a reversal landing on
		// the base is not a crossing, and the floor clamp then commits one entry above it. The
		// budget and the full undo bound such a walk.
		c := makeTestClimber()
		dc := &c.density
		w := injectWalk(dc, true, false, int64(ctFloor), 0.06, 0.99)
		w.samples = 1
		dc.rates.smoothed = 0.06
		dc.rates.deviation = 0.001
		c.sample.previousHitRate = 0.06
		climberStepOf(c).size = ctStride

		// Improving samples keep the heading; a worsening one reverses it.
		steadySample(c, 675, 0.70)
		requireSameWalk(t, dc, w)
		steadySample(c, 1187, 0.40)
		requireSameWalk(t, dc, w)

		// The reversal proposes the base exactly; the walk survives and rests one entry above it.
		landing := steadySample(c, 675, 0.60)
		requireSameWalk(t, dc, w)
		require.Equal(t, int64(-511), landing)

		steadySample(c, 164, 0.055)
		requireSameWalk(t, dc, w)

		w.samples = probeWalkBudget
		undo := steadySample(c, 676, 0.70)
		require.Equal(t, int64(ctFloor)-676, undo)
		require.False(t, dc.walking)
		require.Equal(t, int64(0), dc.undoRemaining)
		require.Equal(t, 0, dc.audit.crashStreak)
		require.Equal(t, 2*probeBackoffInitial, dc.audit.rung)
		require.False(t, dc.anchor.held)
	})

	t.Run("walkStep_strideScalesByRung", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			rung  int
			scale int64
		}{{16, 1}, {32, 2}, {64, 4}} {
			c := makeTestClimber()
			c.density.starvation.rung = tt.rung
			adjustment := sampleClimber(c, 1024, 0, 500, 500)
			require.Equal(t, tt.scale*int64(ctStride), adjustment, "rung %d", tt.rung)
		}
	})

	t.Run("walkStep_continuation_decaysWhileQuiet", func(t *testing.T) {
		t.Parallel()

		c := probingUp(ctStride, 0.5, 0)
		adjustment := sampleClimber(c, 1536, 5, 495, 500)

		require.True(t, c.density.walking)
		require.Equal(t, int64(stepDecayRate*ctStride), adjustment)
	})

	t.Run("walkStep_crashScaleSampleDrop_reverses", func(t *testing.T) {
		t.Parallel()

		// A drop past the reversal bar (at the cold-start deviation seed it sits at its 15pp cap)
		// reverses the walk while staying above the crash threshold.
		c := probingUp(ctStride, 0.5, 0)
		c.sample.previousHitRate = 0.6
		adjustment := sampleClimber(c, 2048, 5, 435, 560)

		require.True(t, c.density.walking)
		require.Equal(t, int64(-ctStride), adjustment)
	})

	t.Run("walkStep_reversalThroughBase_failsAndDoubles", func(t *testing.T) {
		t.Parallel()

		c := probingUp(ctStride, 0.5, 0)
		dc := &c.density
		c.sample.previousHitRate = 0.6
		adjustment := sampleClimber(c, 1280, 5, 435, 560)

		require.False(t, dc.walking)
		require.Equal(t, int64(1024-1280), adjustment)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, dc.starvation.rung, dc.refractoryLeft)
	})

	t.Run("walkStep_reversalThroughBase_failsDownward", func(t *testing.T) {
		t.Parallel()

		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		c.sample.previousHitRate = 0.6
		adjustment := sampleClimber(c, 6900, 435, 5, 560)

		require.False(t, dc.walking)
		require.Equal(t, int64(7000-6900), adjustment)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("walkStep_floorClamp_landsOnFloor", func(t *testing.T) {
		t.Parallel()

		c := probingDown(-ctStride, 0.5, 0)
		adjustment := sampleClimber(c, 400, 495, 5, 500)

		require.True(t, c.density.walking)
		require.Equal(t, int64(ctFloor-400), adjustment)
		require.Equal(t, ctFloor-400, climberStepOf(c).size)
	})

	t.Run("walkStep_floorBlocked_restartHoldsDirection", func(t *testing.T) {
		t.Parallel()

		// At the floor the clamp yields a zero step whose sign must stay negative, or a
		// restart-scale improving sample would stride upward during a down walk.
		c := probingDown(-ctStride, 0.5, 0)
		landing := sampleClimber(c, 163, 495, 5, 500)
		require.Equal(t, int64(0), landing)
		requireNegativeZero(t, climberStepOf(c).size)

		jitter := sampleClimber(c, 163, 555, 5, 440)
		require.Equal(t, int64(0), jitter)
		require.True(t, c.density.walking)
		requireNegativeZero(t, climberStepOf(c).size)
	})

	t.Run("walkStep_floorBlocked_crashScaleDropWalksBackOut", func(t *testing.T) {
		t.Parallel()

		// A floor position that is hurting walks back out.
		c := probingDown(-ctStride, 0.5, 0)
		landing := sampleClimber(c, 163, 555, 5, 440)
		require.Equal(t, int64(0), landing)

		reversal := sampleClimber(c, 163, 395, 5, 600)
		require.True(t, c.density.walking)
		require.Equal(t, int64(ctStride), reversal)
	})
}

func TestWindowClimber_ProbeEnding(t *testing.T) {
	t.Parallel()

	t.Run("probeEnding_starvationConfirm_doesNotPark", func(t *testing.T) {
		t.Parallel()

		// Steering agrees with a density-confirmed position, so the climber stays live; nor does
		// the confirmation touch the audit clock.
		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 5, 900, 95)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.False(t, dc.anchor.held)
		require.Equal(t, 0, dc.anchor.freshLeft)
		require.Equal(t, auditWaitFirst, dc.auditClock.waitSamples)
		require.Equal(t, densityStep(2000, 5, 900), adjustment)
	})

	t.Run("probeEnding_starvationConfirm_leavesTheAuditClock", func(t *testing.T) {
		t.Parallel()

		// Resetting the audit clock here would spend the cold-start calibration schedule before the
		// first audit ran. The calibration audit still arms early from the confirmed position.
		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		sampleClimber(c, 2000, 5, 900, 95)
		require.False(t, dc.walking)
		require.Equal(t, auditWaitFirst, dc.auditClock.waitSamples)

		armedAt := -1
		for i := range auditWaitInitial {
			steadySample(c, 2000, 0.70)
			if dc.walking {
				armedAt = i
				break
			}
		}
		require.GreaterOrEqual(t, armedAt, auditWaitFirst-1)
		require.Less(t, armedAt, auditWaitInitial-1)
		require.True(t, walkOf(t, dc).isAudit)

		// A deep post-failure schedule survives the confirmation unchanged as well.
		deep := probingDown(-ctStride, 0.9, 0)
		deep.density.auditClock.waitSamples = auditWaitMax
		sampleClimber(deep, 2000, 5, 900, 95)
		require.False(t, deep.density.walking)
		require.Equal(t, auditWaitMax, deep.density.auditClock.waitSamples)
	})

	t.Run("probeEnding_starvationConfirm_leavesTheAuditLadder", func(t *testing.T) {
		t.Parallel()

		// A starvation confirmation must not reset the deep stride and commitment that the audit's
		// next test earned.
		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		dc.audit.rung = probeBackoffMax
		dc.audit.crashStreak = 1
		sampleClimber(c, 2000, 5, 900, 95)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, 1, dc.audit.crashStreak)
		require.Equal(t, probeBackoffMax, dc.audit.rung)
	})

	t.Run("probeEnding_crash_undoesWithoutDoubling", func(t *testing.T) {
		t.Parallel()

		// At the cold-start deviation seed the crash threshold sits at its 15pp cap.
		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		adjustment := sampleClimber(c, 5000, 340, 0, 660)

		require.False(t, dc.walking)
		require.Equal(t, int64(7000-5000), adjustment)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, probeBackoffInitial, dc.refractoryLeft)
	})

	t.Run("probeEnding_subBarDrop_doesNotCrashAStarvationProbe", func(t *testing.T) {
		t.Parallel()

		// The starvation threshold scales with the workload's deviation, so a 10pp drop is noise
		// while the same drop aborts an audit.
		c := probingDown(-ctStride, 0.5, 0)
		adjustment := sampleClimber(c, 5000, 400, 0, 600)

		require.True(t, c.density.walking)
		require.Equal(t, int64(stepDecayRate*-ctStride), adjustment)
	})

	t.Run("probeEnding_probeBarFloorsAtTheRestartThreshold", func(t *testing.T) {
		t.Parallel()

		// With a small deviation the threshold floors at the absolute one, so a 6pp drop on a quiet
		// workload still aborts.
		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		dc.rates.smoothed = 0.5
		dc.rates.deviation = 0.001
		adjustment := sampleClimber(c, 5000, 440, 0, 560)

		require.False(t, dc.walking)
		require.Equal(t, int64(7000-5000), adjustment)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("probeEnding_starvationCrash_leavesTheAuditLadder", func(t *testing.T) {
		t.Parallel()

		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		dc.rates.smoothed = 0.5
		dc.rates.deviation = 0.001
		sampleClimber(c, 5000, 440, 0, 560)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.crashStreak)
		require.Equal(t, 0, dc.audit.crashStreak)
		require.Equal(t, probeBackoffInitial, dc.audit.rung)
		require.Equal(t, auditWaitFirst, dc.auditClock.waitSamples)
	})

	t.Run("probeEnding_probeBarCapsAtThreeRestarts", func(t *testing.T) {
		t.Parallel()

		// On a workload whose deviation alone reads ~34pp, a 16pp collapse must still abort.
		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		dc.rates.smoothed = 0.5
		dc.rates.deviation = 0.10
		adjustment := sampleClimber(c, 5000, 340, 0, 660)

		require.False(t, dc.walking)
		require.Equal(t, int64(7000-5000), adjustment)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("probeEnding_probeBarTracksTheLiveDeviation_notTheArmingValue", func(t *testing.T) {
		t.Parallel()

		// The threshold uses the deviation as it stands now. A value frozen at the arm would sit
		// below the noise a walk later meets and abort it inside that noise.
		for _, weatherRose := range []bool{false, true} {
			c := probingDown(-ctStride, 0.5, 0)
			dc := &c.density
			dc.rates.smoothed = 0.5
			dc.rates.deviation = 0.001
			if weatherRose {
				dc.rates.deviation = 0.06
			}
			sampleClimber(c, 5000, 410, 0, 590)

			require.Equal(t, weatherRose, dc.walking, "weatherRose %v", weatherRose)
		}
	})

	t.Run("probeEnding_budgetExpiry_undoesAndDoubles", func(t *testing.T) {
		t.Parallel()

		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		walkOf(t, dc).samples = probeWalkBudget
		adjustment := sampleClimber(c, 5000, 495, 5, 500)

		require.False(t, dc.walking)
		require.Equal(t, int64(7000-5000), adjustment)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, dc.starvation.rung, dc.refractoryLeft)
	})

	t.Run("probeEnding_watchedRegionIsMainForDownProbe", func(t *testing.T) {
		t.Parallel()

		// A richly earning window must not judge a down probe; main is the watched region.
		c := probingDown(-ctStride, 0.9, 0)
		adjustment := sampleClimber(c, 2000, 900, 5, 95)

		require.True(t, c.density.walking)
		require.Equal(t, int64(stepDecayRate*-ctStride), adjustment)
	})

	t.Run("probeEnding_adjudication_confirmsOnDensity", func(t *testing.T) {
		t.Parallel()

		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 5, 900, 95)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, 0, dc.refractoryLeft)
		require.Equal(t, densityStep(2000, 5, 900), adjustment)
	})

	t.Run("probeEnding_adjudication_wrongSignFailsAndDoubles", func(t *testing.T) {
		t.Parallel()

		// The 5000-entry restore exceeds the maximum step, so the undo honors the cap and the
		// remainder is carried across the following samples.
		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		capStep := maxStepFraction * float64(ctMaximum)
		adjustment := sampleClimber(c, 2000, 880, 20, 100)

		require.False(t, dc.walking)
		require.Equal(t, int64(capStep), adjustment)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)

		second := sampleClimber(c, 2000+adjustment, 880, 20, 100)
		require.Equal(t, int64(capStep), second)

		third := sampleClimber(c, 2000+adjustment+second, 880, 20, 100)
		require.Equal(t, 5000-2*int64(ctMaxStep), third)
		require.Equal(t, int64(5000), adjustment+second+third)
		require.Equal(t, int64(0), dc.undoRemaining)
	})

	t.Run("probeEnding_secondConsecutiveCrash_escalates", func(t *testing.T) {
		t.Parallel()

		// One crash may be a workload shift; consecutive crashes are probe damage and escalate like
		// failures.
		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		first := sampleClimber(c, 5000, 340, 0, 660)
		require.Equal(t, int64(7000-5000), first)
		require.Equal(t, 1, dc.starvation.crashStreak)
		require.Equal(t, probeBackoffInitial, dc.starvation.rung)

		w := injectWalk(dc, false, true, 7000, 0.5, 0.0)
		w.samples = 1
		climberStepOf(c).size = -ctStride
		c.sample.previousHitRate = 0.5
		second := sampleClimber(c, 5000, 340, 0, 660)
		require.Equal(t, int64(7000-5000), second)
		require.Equal(t, 2, dc.starvation.crashStreak)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("probeEnding_crashStreak_saturates", func(t *testing.T) {
		t.Parallel()

		// The streak only distinguishes a lone crash from a run, so it holds at the escalation
		// threshold.
		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		dc.starvation.crashStreak = probeCrashEscalation
		returned := sampleClimber(c, 5000, 340, 0, 660)
		require.Equal(t, int64(7000-5000), returned)
		require.Equal(t, probeCrashEscalation, dc.starvation.crashStreak)
		require.True(t, dc.starvation.crashEscalates())
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("probeEnding_midRungCommitment_blocksEarlyExit", func(t *testing.T) {
		t.Parallel()

		c := makeTestClimber()
		dc := &c.density
		dc.starvation.rung = 2 * probeBackoffInitial
		entry := sampleClimber(c, 1024, 0, 500, 500)
		require.True(t, dc.walking)
		require.Equal(t, 2*int64(ctStride), entry)

		// Depth 1 is below the middle rung's commitment of 2, so the verdict is blocked.
		walked := sampleClimber(c, 3072, 400, 100, 500)
		require.True(t, dc.walking)
		require.Equal(t, int64(stepDecayRate*2*ctStride), walked)

		// At depth 2 the same signal confirms; the window is the denser region.
		confirmed := sampleClimber(c, 3072, 400, 100, 500)
		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, densityStep(3072, 400, 100), confirmed)
	})

	t.Run("probeEnding_upProbe_confirmsAgainstFrozenBaseline", func(t *testing.T) {
		t.Parallel()

		// A window that loses to main's average density still confirms by out-earning the
		// probation baseline frozen at the start. Density walks it home in the same sample, and
		// the ladder prices that reversal as a completed experiment.
		c := probingUp(ctStride, 0.5, 0.002)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 30, 940, 30)

		require.False(t, dc.walking)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, 0, dc.refractoryLeft)
		require.Equal(t, densityStep(2000, 30, 940), adjustment)
	})

	t.Run("probeEnding_upProbe_failsAgainstRicherFrozenBaseline", func(t *testing.T) {
		t.Parallel()

		c := probingUp(ctStride, 0.5, 0.5)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 30, 940, 30)

		require.False(t, dc.walking)
		require.Equal(t, int64(1024-2000), adjustment)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, dc.starvation.rung, dc.refractoryLeft)
	})

	t.Run("probeEnding_upProbeVerdict_isFreeOfTheSampleLength", func(t *testing.T) {
		t.Parallel()

		// The same per-request rates over a sample twice as long must reach the same verdict, or a
		// sample period that grows with the resident count would confirm every up probe.
		brief := probingUp(ctStride, 0.97, 0.02)
		briefVerdict := sampleClimber(brief, 2000, 30, 940, 30)

		extended := probingUp(ctStride, 0.97, 0.02)
		extendedVerdict := sampleClimber(extended, 2000, 60, 1880, 60)

		// The window earns less per request than the boundary did, so both fail.
		require.False(t, brief.density.walking)
		require.False(t, extended.density.walking)
		require.Equal(t, int64(1024-2000), briefVerdict)
		require.Equal(t, int64(1024-2000), extendedVerdict)
		require.Equal(t, 2*probeBackoffInitial, brief.density.starvation.rung)
		require.Equal(t, 2*probeBackoffInitial, extended.density.starvation.rung)
	})

	t.Run("armProbe_freezesProbationBaselinePerArm", func(t *testing.T) {
		t.Parallel()

		// The baseline is taken at each arm, never mid-walk: the walk's own demotions enrich live
		// probation, and re-arming replaces a transient baseline.
		c := makeTestClimber()
		dc := &c.density
		capacity := probationCapacity(1024)
		entry := sampleWithProbation(c, 1024, 0, 500, 200, 500)
		require.True(t, dc.walking)
		require.Equal(t, int64(ctStride), entry)
		require.InDelta(t, 200.0/float64(capacity), walkOf(t, dc).baseProbationDensity, 1.0e-9)

		dc.walking = false
		dc.refractoryLeft = 0
		rearm := sampleWithProbation(c, 1024, 0, 500, 10, 500)
		require.True(t, dc.walking)
		require.Equal(t, int64(ctStride), rearm)
		require.InDelta(t, 10.0/float64(capacity), walkOf(t, dc).baseProbationDensity, 1.0e-9)
	})

	t.Run("probeEnding_upProbe_zeroBaselineConfirmsOnAnySignal", func(t *testing.T) {
		t.Parallel()

		// A probation region earning nothing at the arm costs nothing to claim, so any watched
		// earnings above the bar confirm; density then reverses the sparser window.
		c := probingUp(ctStride, 0.5, 0.0)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 30, 940, 30)

		require.False(t, dc.walking)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
		require.Equal(t, densityStep(2000, 30, 940), adjustment)
	})

	t.Run("probeEnding_starvationConfirm_reversedByDensity_escalatesTheLadder", func(t *testing.T) {
		t.Parallel()

		// A confirmation that density reverses in the same sample keeps nothing, so rewarding it
		// would restart the ladder on every cycle of a dither.
		reversed := probingUp(ctStride, 0.5, 0.002)
		rdc := &reversed.density
		adjustment := sampleClimber(reversed, 2000, 30, 940, 30)
		require.False(t, rdc.walking)
		require.Equal(t, 2*probeBackoffInitial, rdc.starvation.rung)
		require.Equal(t, 0, rdc.refractoryLeft)
		require.False(t, rdc.anchor.held)
		require.Less(t, adjustment, int64(0))
		require.Equal(t, densityStep(2000, 30, 940), adjustment)

		// The same verdict where density agrees with the walk keeps the reward.
		agreed := probingDown(-ctStride, 0.9, 0)
		sampleClimber(agreed, 2000, 5, 900, 95)
		require.False(t, agreed.density.walking)
		require.Equal(t, 1, agreed.density.starvation.rung)
		require.Equal(t, 0, agreed.density.refractoryLeft)

		// At the deepest rung a reversal cannot deepen the ladder further.
		deepest := probingUp(ctStride, 0.5, 0.002)
		ddc := &deepest.density
		ddc.starvation.rung = probeBackoffMax
		walkOf(t, ddc).samples = probeCommitmentDeep
		sampleClimber(deepest, 2000, 30, 940, 30)
		require.False(t, ddc.walking)
		require.Equal(t, probeBackoffMax, ddc.starvation.rung)
		require.Equal(t, 0, ddc.refractoryLeft)
	})

	t.Run("probeEnding_starvationConfirm_repeatOfTheFarthest_escalatesTheLadder", func(t *testing.T) {
		t.Parallel()

		// A confirmation at or short of the farthest confirmed window re-finds lost ground, so it
		// escalates rather than rewards; a confirmation beyond it is new ground.
		c := probingUp(ctStride, 0.5, 0)
		dc := &c.density
		sampleClimber(c, 2000, 500, 400, 100)
		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, int64(2000), dc.starvation.farthest)
		require.False(t, dc.starvation.farthestDown)

		reprobeUp(c)
		adjustment := sampleClimber(c, 1800, 500, 400, 100)
		require.False(t, dc.walking)
		require.Equal(t, 2, dc.starvation.rung)
		require.Equal(t, 0, dc.refractoryLeft)
		require.Equal(t, int64(2000), dc.starvation.farthest)
		require.Equal(t, densityStep(1800, 500, 400), adjustment)

		// Within the stable band of the farthest is still a repeat.
		reprobeUp(c)
		sampleClimber(c, 2100, 500, 400, 100)
		require.Equal(t, 4, dc.starvation.rung)
		require.Equal(t, int64(2100), dc.starvation.farthest)

		reprobeUp(c)
		sampleClimber(c, 2600, 500, 400, 100)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, int64(2600), dc.starvation.farthest)
	})

	t.Run("probeEnding_starvationConfirm_theOtherDirection_startsTheMemoryOver", func(t *testing.T) {
		t.Parallel()

		// The memory is per direction, and the farthest window of a down walk is the smallest.
		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		dc.starvation.remember(false, 2000)
		sampleClimber(c, 2000, 5, 900, 95)
		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.True(t, dc.starvation.farthestDown)
		require.Equal(t, int64(2000), dc.starvation.farthest)

		reprobeDown(c)
		sampleClimber(c, 2100, 5, 900, 95)
		require.Equal(t, 2, dc.starvation.rung)
		require.Equal(t, int64(2000), dc.starvation.farthest)

		reprobeDown(c)
		sampleClimber(c, 1500, 5, 900, 95)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, int64(1500), dc.starvation.farthest)
	})

	t.Run("undoProbe_starvationFailOrCrash_forgetsTheFarthest", func(t *testing.T) {
		t.Parallel()

		// A walk that keeps nothing shows that the terrain has moved; an audit's endings leave the
		// starvation memory alone.
		failed := probingUp(ctStride, 0.5, 0)
		fdc := &failed.density
		sampleClimber(failed, 2000, 500, 400, 100)
		require.Equal(t, int64(2000), fdc.starvation.farthest)
		reprobeUp(failed).samples = probeWalkBudget
		sampleClimber(failed, 1500, 0, 500, 500)
		require.False(t, fdc.walking)
		require.Equal(t, int64(-1), fdc.starvation.farthest)

		crashed := probingUp(ctStride, 0.5, 0)
		cdc := &crashed.density
		sampleClimber(crashed, 2000, 500, 400, 100)
		reprobeUp(crashed)
		sampleClimber(crashed, 1500, 0, 100, 900)
		require.False(t, cdc.walking)
		require.Equal(t, 1, cdc.starvation.crashStreak)
		require.Equal(t, int64(-1), cdc.starvation.farthest)

		audited := probingUp(ctStride, 0.5, 0)
		adc := &audited.density
		sampleClimber(audited, 2000, 500, 400, 100)
		injectWalk(adc, true, true, 2000, 0.5, 0.5).samples = probeWalkBudget
		audited.sample.previousHitRate = 0.5
		sampleClimber(audited, 1800, 400, 100, 500)
		require.False(t, adc.walking)
		require.Equal(t, int64(2000), adc.starvation.farthest)
	})

	t.Run("probeEnding_deepReversedConfirm_goalConfirmed_parksAsAnAudit", func(t *testing.T) {
		t.Parallel()

		// A deep starvation walk whose confirmation density reverses, and that passes the audit's
		// own confirmation test, is kept as a park with no parting step.
		c := probingUp(ctStride, 0.30, 0.002)
		dc := &c.density
		dc.starvation.rung = probeBackoffMax
		w := walkOf(t, dc)
		w.samples = probeCommitmentDeep
		w.aboveStreak = auditConfirmStreak - 1
		adjustment := sampleClimber(c, 2000, 30, 940, 30)
		require.False(t, dc.walking)
		require.Equal(t, int64(0), adjustment)
		require.True(t, dc.anchor.held)
		require.Equal(t, auditWaitInitial, dc.anchor.freshLeft)
		require.Equal(t, int64(2000), dc.anchor.window)
		require.Equal(t, probeBackoffMax, dc.starvation.rung)

		// Without the hit-rate streak the same confirmation hands back to density.
		unconfirmed := probingUp(ctStride, 0.30, 0.002)
		udc := &unconfirmed.density
		udc.starvation.rung = probeBackoffMax
		walkOf(t, udc).samples = probeCommitmentDeep
		steered := sampleClimber(unconfirmed, 2000, 30, 940, 30)
		require.False(t, udc.walking)
		require.False(t, udc.anchor.held)
		require.Equal(t, densityStep(2000, 30, 940), steered)

		// And so does a walk short of the deepest commitment, streak or not.
		shallow := probingUp(ctStride, 0.30, 0.002)
		sdc := &shallow.density
		walkOf(t, sdc).aboveStreak = auditConfirmStreak - 1
		shallowStep := sampleClimber(shallow, 2000, 30, 940, 30)
		require.False(t, sdc.walking)
		require.False(t, sdc.anchor.held)
		require.Equal(t, densityStep(2000, 30, 940), shallowStep)
	})

	t.Run("probeEnding_downProbe_ignoresProbationBaseline", func(t *testing.T) {
		t.Parallel()

		// Down probes use average densities, so a poisoned baseline must not affect them.
		c := probingDown(-ctStride, 0.9, 1.0e9)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 5, 900, 95)

		require.False(t, dc.walking)
		require.Equal(t, 1, dc.starvation.rung)
		require.Equal(t, densityStep(2000, 5, 900), adjustment)
	})

	t.Run("probeEnding_adjudicationBar_isFourTimesTheStarvationBar", func(t *testing.T) {
		t.Parallel()

		// The watched region may judge at exactly four times the bar; one hit fewer keeps walking.
		judged := probingDown(-ctStride, 0.9, 0)
		verdict := sampleClimber(judged, 2000, 850, 16, 134)
		require.False(t, judged.density.walking)
		require.Equal(t, int64(ctMaxStep), verdict)

		walking := probingDown(-ctStride, 0.9, 0)
		stride := sampleClimber(walking, 2000, 850, 15, 135)
		require.True(t, walking.density.walking)
		require.Equal(t, int64(stepDecayRate*-ctStride), stride)
	})

	t.Run("probeEnding_neutralVerdict_fails", func(t *testing.T) {
		t.Parallel()

		// Keeping neutral positions would let density walk the window home and the probe re-fire
		// forever. Equal densities give a verdict of exactly zero, which fails.
		c := probingDown(-ctStride, 0.9, 0)
		dc := &c.density
		adjustment := sampleClimber(c, 2000, 250, 774, 0)

		require.False(t, dc.walking)
		require.Equal(t, int64(ctMaxStep), adjustment)
		require.Equal(t, 5000-int64(ctMaxStep), dc.undoRemaining)
		require.Equal(t, 2*probeBackoffInitial, dc.starvation.rung)
	})

	t.Run("probeEnding_ladderCapBinds", func(t *testing.T) {
		t.Parallel()

		c := probingDown(-ctStride, 0.5, 0)
		dc := &c.density
		dc.starvation.rung = probeBackoffMax
		walkOf(t, dc).samples = probeWalkBudget
		undo := sampleClimber(c, 5000, 495, 5, 500)

		require.Equal(t, int64(7000-5000), undo)
		require.False(t, dc.walking)
		require.Equal(t, probeBackoffMax, dc.starvation.rung)
	})
}

func TestWindowClimber_Tiers(t *testing.T) {
	t.Parallel()

	t.Run("mediumTier_usesSketchPeriodAndStandardDecay", func(t *testing.T) {
		t.Parallel()

		c := newWindowClimber()
		c.resized(2048)
		seed := climberStepOf(&c).size

		for range 500 {
			c.recordHit(false, false)
			c.recordMiss()
		}
		c.determineAdjustment(2048, 200, protectedMax(2048, 200), 1001)
		require.Equal(t, int64(0), c.adjustment)
		require.Equal(t, int64(500), c.sample.hits)

		c.recordMiss()
		c.determineAdjustment(2048, 200, protectedMax(2048, 200), 1001)
		require.Equal(t, int64(seed), c.adjustment)
		require.Equal(t, int64(0), c.sample.hits)

		for range 505 {
			c.recordHit(false, false)
		}
		for range 496 {
			c.recordMiss()
		}
		c.determineAdjustment(2048, 200, protectedMax(2048, 200), 1001)
		require.Equal(t, int64(seed), c.adjustment)
		require.InDelta(t, stepDecayRate*seed, climberStepOf(&c).size, 1.0e-9)
	})

	t.Run("mediumTier_reversesOnWorse_andRestartsOnCrashScaleChange", func(t *testing.T) {
		t.Parallel()

		// A worsening sub-crash sample reverses the step, and a crash-scale change restores the
		// decayed step to its full magnitude.
		c := newWindowClimber()
		c.resized(2048)
		seed := climberStepOf(&c).size
		require.Equal(t, -stepPercent*2048, seed)

		reactiveSample(&c, 500, 501)
		require.Equal(t, int64(seed), c.adjustment)

		reactiveSample(&c, 480, 521)
		require.Equal(t, int64(-seed), c.adjustment)
		require.InDelta(t, stepDecayRate*-seed, climberStepOf(&c).size, 1.0e-9)

		reactiveSample(&c, 590, 411)
		require.Equal(t, int64(stepDecayRate*-seed), c.adjustment)
		require.Equal(t, -seed, climberStepOf(&c).size)
	})
}

func TestWindowClimber_FloorBlockedWalk(t *testing.T) {
	t.Parallel()

	t.Run("walk_floorBlockedDownProbe_neverStridesUpAndUndoesOnExpiry", func(t *testing.T) {
		t.Parallel()

		maximum := int64(1_048_576)
		c := newTestClimber(maximum)
		dc := &c.density
		dc.starvation.rung = probeBackoffMax
		floor := windowFloorFraction * float64(maximum)
		stride := stepPercent * float64(maximum)
		base := 800_000.0

		// The walk arms from a dead sample past the midpoint.
		windowMax := int64(base)
		arming := bigSample(c, maximum, windowMax, 0)
		expected := -math.Min(maxStepFraction*float64(maximum), 4*stride)
		require.Equal(t, int64(expected), arming)
		require.True(t, walkOf(t, dc).down)
		windowMax += arming

		moving, blocked := 1, 0
		for i := 0; i < probeWalkBudget && dc.walking; i++ {
			hits := 500
			if blocked == 4 {
				hits = 540 // an improving sub-crash jitter against the floor
			}
			adjustment := bigSample(c, maximum, windowMax, hits)
			if dc.walking {
				require.LessOrEqual(t, adjustment, int64(0))
				if adjustment == 0 {
					require.GreaterOrEqual(t, windowMax, int64(floor))
					require.LessOrEqual(t, windowMax, int64(math.Ceil(floor))+1)
					blocked++
				} else {
					moving++
				}
				windowMax += adjustment
			} else {
				// The restore honors the maximum step; the remainder continues on later samples.
				capStep := maxStepFraction * float64(maximum)
				require.LessOrEqual(t, adjustment, int64(capStep))
				windowMax += adjustment
				for j := 0; j < probeWalkBudget && dc.undoRemaining != 0; j++ {
					chunk := bigSample(c, maximum, windowMax, 500)
					require.LessOrEqual(t, absInt64(chunk), int64(capStep))
					windowMax += chunk
				}
				require.Equal(t, int64(0), dc.undoRemaining)
			}
		}
		require.False(t, dc.walking)
		require.InDelta(t, base, float64(windowMax), 4.0)
		require.LessOrEqual(t, moving, 5)
		require.GreaterOrEqual(t, blocked, 10)
		require.Equal(t, probeBackoffMax, dc.starvation.rung)
	})

	t.Run("walk_floorBlockedAudit_chargesTheClockForEvidenceItNeverGathered", func(t *testing.T) {
		t.Parallel()

		// Strides that clamp at the floor still spend a sample apiece, and the expiry is priced as
		// a completed failure; at the deepest rung that doubles the wait between audits. This pins
		// the current behavior, so a dedicated blocked ending would have to change it deliberately.
		c := makeTestClimber()
		dc := &c.density
		dc.audit.rung = probeBackoffMax
		dc.auditClock.waitSamples = auditWaitMax / 2
		dc.auditClock.stillSamples = auditWaitMax
		dc.rates.smoothed = 0.50
		dc.rates.deviation = 0.001
		c.sample.previousHitRate = 0.50

		// Both regions earn, so no blind corner preempts the clock; past the upper corner the audit
		// walks down into the floor.
		windowMax := int64(7000)
		windowMax += sampleClimber(c, windowMax, 250, 250, 500)
		require.True(t, walkOf(t, dc).isAudit)
		require.True(t, walkOf(t, dc).down)

		lowest := windowMax
		blocked := 0
		for i := 0; i < probeWalkBudget && dc.walking; i++ {
			adjustment := sampleClimber(c, windowMax, 250, 250, 500)
			if dc.walking && adjustment == 0 {
				blocked++
			}
			windowMax += adjustment
			lowest = min(lowest, windowMax)
		}

		require.False(t, dc.walking)
		require.GreaterOrEqual(t, blocked, probeWalkBudget/2)
		require.LessOrEqual(t, lowest, int64(math.Ceil(ctFloor))+1)
		require.Equal(t, probeBackoffMax, dc.audit.rung)
		require.Equal(t, auditWaitMax, dc.auditClock.waitSamples)
	})
}

// FuzzWindowClimber drives the climber with random samples, window positions, region geometries,
// and resizes. The cache applies an adjustment through capped transfers, so each adjustment is
// applied only partially, and the window occasionally jumps within its legal range. Every decision
// is checked against the climber's invariants and the rules that tie each deferral to the layer
// that owns it.
func FuzzWindowClimber(f *testing.F) {
	f.Add([]byte{})
	r := rand.New(rand.NewPCG(1, 2))
	for range 8 {
		seed := make([]byte, 16<<10)
		for i := range seed {
			seed[i] = byte(r.Uint32())
		}
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		t.Parallel()

		data := &climberFuzzData{data: input}
		maximum := pickFuzzMaximum(data)
		climber := newWindowClimber()
		c := &climber
		c.resized(maximum)
		requireClimberInvariants(t, c, maximum)

		windowMax := maximum / 100
		carryOver := c.adjustment
		operations := data.consumeInt(200)
		for range operations {
			if data.consumeInt(7) == 0 {
				// An occasional resize, sometimes crossing tiers, resets the climber.
				maximum = pickFuzzMaximum(data)
				c.resized(maximum)
				windowMax = min(windowMax, maximum)
				carryOver = c.adjustment
				requireClimberInvariants(t, c, maximum)
				continue
			}

			// Transfers lag the commands, and expiration or weight changes move the window
			// independently of the climber.
			if data.consumeInt(3) == 0 {
				windowMax = data.consumeInt64(0, maximum)
			}

			// The policy discards the partial sample while the sketch is uninitialized.
			if data.consumeInt(15) == 0 {
				c.resetSample()
			}

			requests := data.consumeInt(5_000_000)
			hits := data.consumeInt(requests)
			windowHits := data.consumeInt(hits)
			probationHits := data.consumeInt(hits - windowHits)
			recordFuzzSample(c, hits, windowHits, probationHits, requests-hits)
			pending := c.sample.hits + c.sample.misses

			var before climberFuzzSnapshot
			if c.dense {
				before = takeClimberFuzzSnapshot(&c.density)
			}

			mainProtected := data.consumeInt64(0, max(0, maximum-windowMax))
			sketchSample := data.consumeInt64(4, 1_000_000)
			c.determineAdjustment(maximum, windowMax, mainProtected, sketchSample)
			requireClimberInvariants(t, c, maximum)
			if c.dense {
				requireDeferralOwnership(t, &c.density, before)
				requireRetestOwnership(t, &c.density, before.anchorWindow)
			}

			// A sample completes when the accumulated counts reached the period and were consumed.
			completed := pending > 0 && c.sample.hits == 0 && c.sample.misses == 0
			if !completed {
				// A partial sample preserves the carry-over, which keeps a capped transfer
				// draining until a fresh sample replaces it.
				require.Equal(t, carryOver, c.adjustment, "a partial sample must not disturb the carry-over")
			}
			carryOver = c.adjustment

			// Apply the command partially, as the transfer throttle would, staying in the legal
			// range. The percentage is split and the move is measured against its room, since at
			// the largest maxima the product and the sum would both overflow.
			adjustment := c.adjustment
			percent := int64(data.consumeInt(100))
			applied := (adjustment/100)*percent + ((adjustment%100)*percent)/100
			if applied >= 0 {
				windowMax += min(applied, maximum-windowMax)
			} else {
				windowMax += max(applied, -windowMax)
			}
		}
	})
}

// climberFuzzSnapshot is the state of a density climber before a decision, which the ownership
// checks compare against after it.
type climberFuzzSnapshot struct {
	walking        bool
	walkIsAudit    bool
	anchorWindow   int64
	starvationRung int
	auditRung      int
	auditWait      int
}

func takeClimberFuzzSnapshot(dc *densityClimber) climberFuzzSnapshot {
	return climberFuzzSnapshot{
		walking:        dc.walking,
		walkIsAudit:    dc.walking && dc.walk.isAudit,
		anchorWindow:   dc.anchor.window,
		starvationRung: dc.starvation.rung,
		auditRung:      dc.audit.rung,
		auditWait:      dc.auditClock.waitSamples,
	}
}

// requireRetestOwnership checks that a retest still pending after the anchor moved belongs to the
// move, since a retest judges the claim frozen for the position its return set out for. The stale
// state is well formed at rest, so the check compares a decision against the one before it.
func requireRetestOwnership(t *testing.T, dc *densityClimber, anchorWindow int64) {
	t.Helper()

	moved := dc.anchor.window != anchorWindow
	require.True(t, !moved || dc.anchor.retestClaim < 0 || dc.anchor.retestClaim == dc.anchor.rate,
		"a retest that survives the anchor moving was armed by the move")
}

// requireDeferralOwnership checks that a layer's ladder and schedule grow only on a decision that
// ended that layer's own walk, and that the audit clock passes its ladder only on a verdict rather
// than a crash. A decision never ends one walk and arms another, so a walk that was in flight and
// is no longer is the one that ended.
func requireDeferralOwnership(t *testing.T, dc *densityClimber, before climberFuzzSnapshot) {
	t.Helper()

	ended := before.walking && !dc.walking
	endedAudit := ended && before.walkIsAudit
	endedStarvation := ended && !before.walkIsAudit

	require.True(t, dc.starvation.rung <= before.starvationRung || endedStarvation,
		"the starvation rung deepens only on a starvation ending")
	require.True(t, dc.audit.rung <= before.auditRung || endedAudit,
		"the audit rung deepens only on an audit ending")
	require.True(t, dc.auditClock.waitSamples <= before.auditWait || endedAudit,
		"the audit clock defers only on an audit ending")
	require.True(t, dc.auditClock.waitSamples <= before.auditWait ||
		dc.auditClock.waitSamples <= max(auditWaitInitial, dc.audit.rung) ||
		dc.audit.crashStreak == 0,
		"the audit clock passes its ladder only on a verdict, never a crash")
}

// pickFuzzMaximum returns a maximum size, weighted toward the tier boundaries and including the
// extremes.
func pickFuzzMaximum(data *climberFuzzData) int64 {
	switch data.consumeInt(4) {
	case 0:
		return data.consumeInt64(0, 512) // the slow-adapt regime
	case 1:
		return data.consumeInt64(513, 4096) // the standard reactive regime
	case 2:
		return data.consumeInt64(4097, 65_536) // the density tier
	case 3:
		return data.consumeInt64(65_537, 1<<40) // large weighted maxima
	default:
		return math.MaxInt64 - data.consumeInt64(0, 1024) // the overflow guard's territory
	}
}

// recordFuzzSample feeds a sample's counts, through the recording methods when they are small
// enough to loop over cheaply.
func recordFuzzSample(c *windowClimber, hits, windowHits, probationHits, misses int) {
	if hits+misses > 4096 {
		c.sample.hits += int64(hits)
		c.sample.windowHits += int64(windowHits)
		c.sample.probationHits += int64(probationHits)
		c.sample.misses += int64(misses)
		return
	}
	for range windowHits {
		c.recordHit(true, false)
	}
	for range probationHits {
		c.recordHit(false, true)
	}
	for range hits - windowHits - probationHits {
		c.recordHit(false, false)
	}
	for range misses {
		c.recordMiss()
	}
}

// climberFuzzData turns the fuzzer's bytes into bounded integers. Once the bytes run out, every
// value is the lower bound.
type climberFuzzData struct {
	data []byte
}

func (d *climberFuzzData) consumeUint64() uint64 {
	var v uint64
	for i := 0; i < 8 && len(d.data) > 0; i++ {
		v = v<<8 | uint64(d.data[0])
		d.data = d.data[1:]
	}
	return v
}

// consumeInt64 returns a value in [lo, hi]; lo must not be negative.
func (d *climberFuzzData) consumeInt64(lo, hi int64) int64 {
	if lo >= hi {
		return lo
	}
	span := uint64(hi-lo) + 1
	//nolint:gosec // the result is at most hi
	return lo + int64(d.consumeUint64()%span)
}

// consumeInt returns a value in [0, hi].
func (d *climberFuzzData) consumeInt(hi int) int {
	return int(d.consumeInt64(0, int64(hi)))
}

// requireClimberInvariants checks every invariant that holds of the climber at rest between
// samples.
func requireClimberInvariants(t *testing.T, c *windowClimber, maximum int64) {
	t.Helper()

	require.Empty(t, climberInvariantViolations(c, maximum))
}

// climberInvariantViolations returns the invariants of the climber at rest between samples that
// do not hold. validateCache reports them for the cache's climber as well.
func climberInvariantViolations(c *windowClimber, maximum int64) []string {
	var violations []string
	check := func(ok bool, msg string) {
		if !ok {
			violations = append(violations, msg)
		}
	}
	isRate := func(r float64) bool { return r >= 0 && r <= 1 }
	isFinite := func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }

	check(c.sample.hits >= 0 && c.sample.misses >= 0, "counters are non-negative")
	check(c.sample.windowHits+c.sample.probationHits <= c.sample.hits,
		"region hits cannot exceed the sample's hits")

	// The cap is a float the climber never truncates, so recomputing it in entries needs one of
	// headroom.
	bound := int64(math.Max(minInitialStep, maxStepFraction*float64(maximum))) + 1
	step := climberStepOf(c)
	check(absInt64(c.adjustment) <= bound, "the adjustment honors the maximum step")
	check(math.Abs(step.size) <= float64(bound), "the step size honors the maximum step")
	check(isFinite(step.size), "the step size is finite")

	check(densityClimberAppliesTo(maximum) == c.dense, "the tier is the one bound for this maximum")
	if !c.dense {
		return violations
	}
	dc := &c.density

	check(dc.starvation.rung >= 1 && dc.starvation.rung <= probeBackoffMax,
		"the starvation rung stays within its schedule")
	check(dc.refractoryLeft >= 0, "the countdown is non-negative")
	check(dc.retreatLeft >= 0 && dc.retreatLeft <= retreatCover, "a retreat's cover stays within its span")
	check(dc.refractoryLeft <= dc.starvation.rung, "the refractory countdown is bounded by its own rung")

	if dc.walking {
		w := &dc.walk
		check(w.samples >= 0 && w.samples <= probeWalkBudget, "a walk is bounded by its budget")
		// The streak is incremented before the abort test and the abort ends the walk, so a walk
		// still in flight has not reached the persistence.
		check(w.belowBarStreak >= 0 && w.belowBarStreak < auditCrashPersistence,
			"a below-bar run is judged at the audit persistence")
		ladder := &dc.starvation
		if w.isAudit {
			ladder = &dc.audit
		}
		check(ladder == w.ladder, "a walk's ladder is the one its own layer owns")
		check(w.bestWindow >= -1 && w.bestWindow <= maximum, "a walk's best sample is a window it stood on, or unset")
		check((w.bestWindow < 0) == (w.bestRate < 0), "a walk's best sample carries the rate that position earned")
		check(w.bestRate <= 1, "a walk's best rate is a rate")
		check(w.baseProbationDensity >= 0 && isFinite(w.baseProbationDensity),
			"the frozen probation baseline is a density")
		// The baseline is rescaled to the live sample's length, so it divides.
		check(w.baseRequestCount > 0, "the frozen sample length is positive")
	}

	check(dc.starvation.farthest >= -1 && dc.starvation.farthest <= maximum, "a ladder's memory is a window or unset")
	// Audits are judged by hit rate and never consult the memory.
	check(dc.audit.farthest == -1, "the audit ladder keeps no memory")

	check(dc.audit.rung >= probeBackoffInitial && dc.audit.rung <= probeBackoffMax,
		"the audit rung stays within its schedule")
	check(dc.audit.crashStreak >= 0 && dc.starvation.crashStreak >= 0, "each ladder's crash streak is non-negative")
	check(dc.audit.crashStreak <= probeCrashEscalation && dc.starvation.crashStreak <= probeCrashEscalation,
		"each ladder's crash streak saturates once it escalates")
	check(dc.auditClock.waitSamples >= auditWaitFirst && dc.auditClock.waitSamples <= auditWaitMax,
		"the audit wait stays within its schedule")
	// The wait may pass the ladder only once the ladder is exhausted: reschedule doubles it only at
	// the deepest rung, and whatever lowers the rung resets the wait at the same time.
	check(dc.auditClock.waitSamples <= probeBackoffMax || dc.audit.rung == probeBackoffMax,
		"the audit clock outruns its ladder only at the deepest rung")
	check(dc.auditClock.stillSamples >= 0, "the stillness count is non-negative")
	check(math.IsNaN(dc.auditClock.settledRate) || isRate(dc.auditClock.settledRate), "the settled rate is a rate")

	check(dc.anchor.freshLeft == 0 || dc.anchor.held, "the fresh-park shield lives and dies with the park")
	check(dc.anchor.freshLeft >= 0 && dc.anchor.freshLeft <= auditWaitInitial,
		"the fresh-park shield stays within its schedule")
	check(dc.anchor.returnLeft >= 0, "the veto return budget is non-negative")
	check(!dc.walking || !dc.anchor.returning, "a walk and a veto return are mutually exclusive")
	check(!dc.anchor.returning || dc.anchor.held, "an in-progress return implies the park that follows it")
	check(!dc.walking || dc.undoRemaining == 0, "a walk holds no undo remainder")
	check(!dc.anchor.held || dc.anchor.isPlanted(), "a park defends a planted anchor")
	check((dc.anchor.settleLeft > 0) == (dc.anchor.retestClaim >= 0),
		"a retest's settle lives and dies with the claim it judges")
	check(dc.anchor.settleLeft <= retestSettle, "a retest's settle stays within its span")
	check(dc.anchor.retestClaim < 0 || dc.anchor.isPlanted(), "a retest judges a planted anchor")
	check(dc.anchor.retestClaim < 0 || dc.anchor.retestClaim <= 1, "a retest's frozen claim is a rate")

	check(dc.rates.deviation >= 0 && isFinite(dc.rates.deviation), "the deviation estimate is non-negative and finite")
	check(math.IsNaN(dc.rates.smoothed) || isRate(dc.rates.smoothed), "the smoothed rate is a rate")
	check(!dc.anchor.isPlanted() || isRate(dc.anchor.rate), "the anchor's claim is a rate")
	return violations
}

func newTestClimber(maximum int64) *windowClimber {
	c := newWindowClimber()
	c.resized(maximum)
	return &c
}

func makeTestClimber() *windowClimber {
	return newTestClimber(ctMaximum)
}

// climberStepOf returns the step of the climber's current tier.
func climberStepOf(c *windowClimber) *climberStep {
	if c.dense {
		return &c.density.step
	}
	return &c.reactive.step
}

func densityOf(t *testing.T, c *windowClimber) *densityClimber {
	t.Helper()

	require.True(t, c.dense, "the climber uses the density tier")
	return &c.density
}

// walkOf returns the walk in flight, asserting that there is one.
func walkOf(t *testing.T, dc *densityClimber) *probeWalk {
	t.Helper()

	require.True(t, dc.walking, "a walk is in flight")
	return &dc.walk
}

// requireSameWalk asserts that the walk is still in flight. Walks are kept in place, so the
// pointer identity holds by construction and the walking flag carries the check.
func requireSameWalk(t *testing.T, dc *densityClimber, w *probeWalk) {
	t.Helper()

	require.True(t, dc.walking, "the walk is still in flight")
	require.Same(t, w, &dc.walk)
}

func requireNegativeZero(t *testing.T, v float64) {
	t.Helper()

	require.Zero(t, v)
	require.True(t, math.Signbit(v), "the zero step keeps its negative sign")
}

// injectWalk puts a walk in flight, as an arm would have left it, so a scenario can start from a
// base the climber would need many samples to reach. The probation baseline is zero, since only an
// up probe's starvation verdict reads it.
func injectWalk(dc *densityClimber, isAudit, down bool,
	baseWindow int64, baseHitRate, baseSmoothedRate float64,
) *probeWalk {
	ladder := &dc.starvation
	if isAudit {
		ladder = &dc.audit
	}
	dc.walk = newProbeWalk(ladder, isAudit, down, baseWindow, 1000, baseHitRate, baseSmoothedRate, 0)
	dc.walking = true
	return &dc.walk
}

// rebaseReference replaces the walk in flight with one whose smoothed-rate baseline is colder, as
// when the rate lags a warming sample, keeping the progress already made.
func rebaseReference(t *testing.T, c *windowClimber, reference float64) *probeWalk {
	t.Helper()

	dc := &c.density
	armed := *walkOf(t, dc)
	w := newProbeWalk(armed.ladder, armed.isAudit, armed.down, armed.baseWindow,
		armed.baseRequestCount, armed.baseHitRate, reference, armed.baseProbationDensity)
	w.samples = armed.samples
	w.belowBarStreak = armed.belowBarStreak
	w.aboveStreak = armed.aboveStreak
	w.beatBase = armed.beatBase
	dc.walk = w
	return &dc.walk
}

// reprobeUp puts a fresh up probe from a hit rate of 0.5 in flight on a climber whose ladder
// already has a history.
func reprobeUp(c *windowClimber) *probeWalk {
	return reprobe(c, false, 1024, ctStride, 0.5)
}

// reprobeDown puts a fresh down probe from a hit rate of 0.9 in flight on a climber whose ladder
// already has a history.
func reprobeDown(c *windowClimber) {
	reprobe(c, true, 7000, -ctStride, 0.9)
}

func reprobe(c *windowClimber, down bool, baseWindow int64, stepSize, baseHitRate float64) *probeWalk {
	dc := &c.density
	w := injectWalk(dc, false, down, baseWindow, baseHitRate, 0)
	w.samples = 1
	dc.step.size = stepSize
	c.sample.previousHitRate = baseHitRate
	dc.refractoryLeft = 0
	dc.undoRemaining = 0
	return w
}

// confirmedAuditPark arms an audit at a quiet position of 5000 earning 0.70 and walks it to a
// confirmed park at an improved 0.74. The first interior audit explores down, so the park is below
// the position.
func confirmedAuditPark(t *testing.T) *windowClimber {
	t.Helper()

	const (
		windowMax  = 5000
		baseRate   = 0.70
		walkedRate = 0.74
	)
	c := makeTestClimber()
	dc := &c.density
	for range auditWaitInitial + 2 {
		steadySample(c, windowMax, baseRate)
		if dc.walking {
			break
		}
	}
	require.True(t, walkOf(t, dc).isAudit)
	require.True(t, walkOf(t, dc).down)
	walked := windowMax + c.adjustment
	for range probeWalkBudget {
		walked += steadySample(c, walked, walkedRate)
		if !dc.walking {
			break
		}
	}
	require.False(t, dc.walking)
	require.True(t, dc.anchor.held)
	require.Equal(t, walked, dc.anchor.window)
	return c
}

// armNextAudit holds the position still at the rate until the next audit arms.
func armNextAudit(t *testing.T, c *windowClimber, windowMax int64, hitRate float64) {
	t.Helper()

	dc := &c.density
	for range 2 * auditWaitInitial {
		steadySample(c, windowMax, hitRate)
		if dc.walking {
			break
		}
	}
	require.True(t, walkOf(t, dc).isAudit)
}

func probingUp(stepSize, baseHitRate, baseProbationDensity float64) *windowClimber {
	return probing(false, stepSize, baseHitRate, 1024, baseProbationDensity)
}

func probingDown(stepSize, baseHitRate, baseProbationDensity float64) *windowClimber {
	return probing(true, stepSize, baseHitRate, 7000, baseProbationDensity)
}

func probing(down bool, stepSize, baseHitRate float64, baseWindow int64, baseProbationDensity float64) *windowClimber {
	c := makeTestClimber()
	dc := &c.density
	dc.walk = newProbeWalk(&dc.starvation, false, down, baseWindow, 1000, baseHitRate, 0, baseProbationDensity)
	dc.walk.samples = 1
	dc.walking = true
	dc.step.size = stepSize
	c.sample.previousHitRate = baseHitRate
	return c
}

// failingDownProbe arms a down probe whose first sample fails on the density verdict, leaving a
// retreat longer than one capped stride so that it continues across the following samples.
func failingDownProbe() *windowClimber {
	c := makeTestClimber()
	dc := &c.density
	injectWalk(dc, false, true, 7000, 0.70, 0).samples = 1
	c.sample.previousHitRate = 0.70
	dc.rates.deviation = 0.001
	dc.rates.smoothed = 0.70
	dc.step.size = -ctStride
	return c
}

// seededShortfall returns a climber whose anchor claims 0.70 at 2000 while the smoothed rate is
// 0.66. The deviation is settled so the margin floor applies; the cold-start seed would hold the
// margin wide for several samples.
func seededShortfall() *windowClimber {
	c := makeTestClimber()
	dc := &c.density
	dc.anchor.window = 2000
	dc.anchor.rate = 0.70
	dc.rates.smoothed = 0.66
	c.sample.previousHitRate = 0.66
	dc.rates.deviation = 0.0
	return c
}

// armAudit runs steady samples at the position until an audit arms.
func armAudit(t *testing.T, windowMax int64) *windowClimber {
	t.Helper()

	return armAuditAtRung(t, windowMax, probeBackoffInitial)
}

// armAuditAtRung arms an audit at a position with the audit ladder already at the given rung.
func armAuditAtRung(t *testing.T, windowMax int64, rung int) *windowClimber {
	t.Helper()

	c := makeTestClimber()
	dc := &c.density
	dc.audit.rung = rung
	armed := false
	for range 2 * auditWaitInitial {
		steadySample(c, windowMax, 0.70)
		if dc.walking {
			armed = true
			break
		}
	}
	require.True(t, armed)
	require.True(t, walkOf(t, dc).isAudit)
	return c
}

// sampleClimber completes one sample and returns the committed adjustment.
func sampleClimber(c *windowClimber, windowMax int64, windowHits, mainHits, misses int) int64 {
	return sampleWithProbation(c, windowMax, windowHits, mainHits, 0, misses)
}

// sampleWithProbation completes one sample whose main hits include the given probation share.
func sampleWithProbation(c *windowClimber, windowMax int64, windowHits, mainHits, probationHits, misses int) int64 {
	for range windowHits {
		c.recordHit(true, false)
	}
	for range mainHits - probationHits {
		c.recordHit(false, false)
	}
	for range probationHits {
		c.recordHit(false, true)
	}
	for range misses {
		c.recordMiss()
	}
	requests := windowHits + mainHits + misses
	c.determineAdjustment(ctMaximum, windowMax, protectedMax(ctMaximum, windowMax), int64(requests))
	return c.adjustment
}

// bigSample completes a thousand-request sample with the given window hits; main earns nothing.
func bigSample(c *windowClimber, maximum, windowMax int64, hits int) int64 {
	for range hits {
		c.recordHit(true, false)
	}
	for range 1000 - hits {
		c.recordMiss()
	}
	c.determineAdjustment(maximum, windowMax, protectedMax(maximum, windowMax), 1000)
	return c.adjustment
}

// steadySample completes a thousand-request sample at the given hit rate, split between the
// regions in proportion to their capacity, so the steering error is about zero.
func steadySample(c *windowClimber, windowMax int64, hitRate float64) int64 {
	hits := int64(math.Floor(1000*hitRate + 0.5))
	windowHits := (hits * windowMax) / ctMaximum
	return sampleClimber(c, windowMax, int(windowHits), int(hits-windowHits), int(1000-hits))
}

// reactiveSampleAt completes one reactive sample at the given maximum, with a sketch period of
// 1001.
func reactiveSampleAt(c *windowClimber, maximum int64, hits, misses int) {
	for range hits {
		c.recordHit(false, false)
	}
	for range misses {
		c.recordMiss()
	}
	c.determineAdjustment(maximum, 20, protectedMax(maximum, 20), 1001)
}

// reactiveSample completes one sample of a 2048-entry reactive climber.
func reactiveSample(c *windowClimber, hits, misses int) {
	for range hits {
		c.recordHit(false, false)
	}
	for range misses {
		c.recordMiss()
	}
	c.determineAdjustment(2048, 200, protectedMax(2048, 200), 1001)
}

// protectedMax returns the protected region's maximum as 80% of the main space left by the window.
// The cache instead sets it once per resize, so its probation stays fixed while this one varies
// with the window.
func protectedMax(maximum, windowMax int64) int64 {
	return ((maximum - windowMax) * 4) / 5
}

// probationCapacity returns the probation capacity implied by protectedMax.
func probationCapacity(windowMax int64) int64 {
	return ctMaximum - windowMax - protectedMax(ctMaximum, windowMax)
}

// densityStep mirrors the density climber's proportional step to compute an expected value.
func densityStep(windowMax int64, windowHits, mainHits int) int64 {
	windowDensity := float64(windowHits) / float64(windowMax)
	mainDensity := float64(mainHits) / float64(ctMaximum-windowMax)
	err := math.Log((windowDensity + densityEpsilon) / (mainDensity + densityEpsilon))
	magnitude := math.Min(ctMaxStep, math.Abs(err)*densityGain*float64(ctMaximum))
	step := -magnitude
	if err >= 0 {
		step = magnitude
	}
	clamped := step
	if step < 0 && float64(windowMax)+step < ctFloor {
		clamped = math.Min(0.0, ctFloor-float64(windowMax))
	}
	return int64(clamped)
}
