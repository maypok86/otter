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
)

// W-TinyLFU divides capacity between an admission window, which favors recency, and a main region,
// where TinyLFU filters new arrivals by frequency. The best split depends on the workload, so the
// climber adjusts it using the cache's hit statistics.
//
// Small caches compare hit rates across samples to choose a direction. Larger caches have enough
// hits to compare the regions' hit densities (hits per unit of capacity) within a single sample,
// reducing sensitivity to workload changes between samples.
//
// Density alone cannot identify a good split: it measures only resident entries, and a region with
// few hits provides little information. The density tier uses probes to explore these starved
// regions and periodic audits to test whether a different split improves the hit rate. An anchor
// records a well-performing position to return to if later adjustments reduce the hit rate.
//
// The design and the experiments behind it are described in
// https://htmlpreview.github.io/?https://github.com/ben-manes/caffeine/blob/master/wiki/adaptive-window.html
//
// The climber computes in signed integers and float64, as the window moves in both directions. Its
// float constants are typed, so that a product of two constants rounds as it would at run time.

// climberRestartThreshold is the change in hit rate large enough to trigger adaptation to a new
// workload.
const climberRestartThreshold float64 = 0.05

// maxClimberMaximum bounds the maximum the climber computes with, so that its signed arithmetic
// cannot overflow.
const maxClimberMaximum = math.MaxInt64 - math.MaxInt32

// windowClimber adapts the size of the admission window. Its tier is chosen by the maximum and
// kept in place rather than allocated, so that the climber never allocates.
type windowClimber struct {
	sample     climberSample
	adjustment int64
	dense      bool
	reactive   reactiveClimber
	density    densityClimber
}

func newWindowClimber() windowClimber {
	var c windowClimber
	c.resized(0)
	return c
}

// resized selects the tier and resets adaptation state for the new maximum size.
func (c *windowClimber) resized(maximum int64) {
	c.dense = densityClimberAppliesTo(maximum)
	if c.dense {
		c.density = newDensityClimber(maximum)
	} else {
		c.reactive = newReactiveClimber(maximum)
	}
	c.sample.reset()
	c.adjustment = 0
}

// resetSample discards the current sample and the previous hit rate.
func (c *windowClimber) resetSample() {
	c.sample.reset()
}

// recordMiss records a cache miss.
func (c *windowClimber) recordMiss() {
	c.sample.recordMiss()
}

// recordHit records a cache hit on an entry residing in the window or main space.
func (c *windowClimber) recordHit(inWindow, inProbation bool) {
	c.sample.recordHit(inWindow, inProbation)
}

// carryOver retains the portion of the adjustment that could not be applied.
func (c *windowClimber) carryOver(remaining int64) {
	c.adjustment = remaining
}

// determineAdjustment updates the adjustment when a full sample is available, retaining any
// carry-over otherwise. A positive adjustment grows the window and a negative one shrinks it.
func (c *windowClimber) determineAdjustment(maximum, windowMaximum, mainProtectedMaximum, sketchSampleSize int64) {
	if c.sample.requestCount() < c.samplePeriod(maximum, sketchSampleSize) {
		return
	}
	if c.dense {
		c.adjustment = c.density.climb(&c.sample, maximum, windowMaximum, mainProtectedMaximum)
	} else {
		c.adjustment = c.reactive.climb(&c.sample, maximum)
	}
	c.sample.close()
}

// discardSample discards a full sample gathered while the cache was too empty to adapt from,
// keeping the hit rate from before the gap for the next comparison.
func (c *windowClimber) discardSample(maximum, sketchSampleSize int64) {
	if c.sample.requestCount() >= c.samplePeriod(maximum, sketchSampleSize) {
		c.sample.discard()
	}
}

// samplePeriod returns the number of requests in an adaptation sample.
func (c *windowClimber) samplePeriod(maximum, sketchSampleSize int64) int64 {
	if c.dense {
		return c.density.samplePeriod(maximum, sketchSampleSize)
	}
	return c.reactive.samplePeriod(maximum, sketchSampleSize)
}

const (
	// slowAdaptThreshold is the maximum size at which adaptation uses longer samples and slower
	// step decay.
	slowAdaptThreshold int64 = 512
	// slowAdaptRatioCap is the maximum factor by which the sample period may grow in the slow-adapt
	// regime.
	slowAdaptRatioCap float64 = 4
	// slowAdaptDecayRate is the slower decay rate used to preserve useful steps in very small
	// caches.
	slowAdaptDecayRate float64 = 0.995
)

// reactiveClimber is a hill climber that adjusts the window according to changes in hit rate. It
// reverses direction when the hit rate falls and reduces the step size as it converges. Large
// changes in hit rate restore the initial step size so it can adapt to a new workload.
//
// Small caches use this strategy because their per-region hit counts are too low for reliable
// density estimates. The smallest caches start by growing because their initial window contains
// only a few entries. They use longer samples and slower step decay to reduce sensitivity to noise.
type reactiveClimber struct {
	step climberStep
}

func newReactiveClimber(maximum int64) reactiveClimber {
	return reactiveClimber{step: newClimberStep(maximum, isSlowAdapting(maximum))}
}

// samplePeriod returns the number of requests in an adaptation sample.
func (rc *reactiveClimber) samplePeriod(maximum, sketchSampleSize int64) int64 {
	if !isSlowAdapting(maximum) {
		return sketchSampleSize
	}
	initialStep := stepPercent * float64(maximum)
	magnitude := math.Max(rc.step.magnitude(), initialStep/slowAdaptRatioCap)
	if magnitude == 0 {
		return sketchSampleSize
	}
	ratio := math.Max(1, math.Min(slowAdaptRatioCap, initialStep/magnitude))
	if ratio == 1 {
		return sketchSampleSize
	}
	return int64(float64(sketchSampleSize) * ratio)
}

// climb returns the amount to adapt the window by.
func (rc *reactiveClimber) climb(sample *climberSample, maximum int64) int64 {
	hitRateChange := sample.hitRateChange()
	amount := rc.step.heading(hitRateChange >= 0)
	if math.Abs(hitRateChange) >= climberRestartThreshold {
		rc.step.commit(math.Copysign(restartMagnitude(maximum), amount))
	} else {
		rc.step.commit(reactiveDecayRate(maximum) * amount)
	}
	return int64(amount)
}

// isSlowAdapting reports whether this maximum falls in the slow-adapt regime.
func isSlowAdapting(maximum int64) bool {
	return maximum <= slowAdaptThreshold
}

// reactiveDecayRate returns the step decay rate.
func reactiveDecayRate(maximum int64) float64 {
	if isSlowAdapting(maximum) {
		return slowAdaptDecayRate
	}
	return stepDecayRate
}

const (
	// densityThreshold is the cache size threshold between reactive and density feedback.
	densityThreshold int64 = 4096
	// densitySampleMultiplier makes the sample period longer to stabilize density estimates at the
	// cost of slower adaptation.
	densitySampleMultiplier int64 = 4
	// densityGain is the step per unit of log density-ratio error, as a fraction of the maximum.
	densityGain float64 = 0.03
	// retreatCover is the samples covered by a retreat stride, including its arrival sample.
	retreatCover = 2
)

// densityClimber is a hill climber that shifts capacity toward the region with more hits per unit
// of capacity. Comparing regions within the same sample reduces sensitivity to workload changes
// between samples.
//
// Hit density describes only resident entries and can favor a split with a poor overall hit rate.
// Probes explore regions with too few hits to measure, while audits test whether moving away from
// the current split improves the hit rate. An anchor records a known good position for recovery
// after an unproductive adjustment.
type densityClimber struct {
	step       climberStep
	auditClock auditClock
	starvation probeLadder
	anchor     climberAnchor
	audit      probeLadder
	rates      climberRates

	// walk is the walk in progress while walking is set. It is kept in place rather than allocated
	// per probe, and an ended walk is still read by the retreat that follows its ending.
	walk    probeWalk
	walking bool

	undoRemaining  int64
	refractoryLeft int
	retreatLeft    int
}

func newDensityClimber(maximum int64) densityClimber {
	return densityClimber{
		step:       newClimberStep(maximum, false),
		auditClock: newAuditClock(),
		starvation: newProbeLadder(),
		anchor:     newClimberAnchor(),
		audit:      newProbeLadder(),
		rates:      newClimberRates(),
	}
}

// densityClimberAppliesTo reports whether the cache is large enough to use density feedback.
func densityClimberAppliesTo(maximum int64) bool {
	return maximum > densityThreshold
}

// samplePeriod returns the number of requests in an adaptation sample.
func (dc *densityClimber) samplePeriod(maximum, sketchSampleSize int64) int64 {
	period := densitySampleMultiplier * maximum
	if period/densitySampleMultiplier != maximum {
		period = math.MaxInt64
	}
	return min(period, sketchSampleSize)
}

// climb returns the amount to adapt the window by.
func (dc *densityClimber) climb(sample *climberSample, maximum, windowMax, mainProtectedMax int64) int64 {
	r := newClimberReading(sample, maximum, windowMax, mainProtectedMax)
	amount := dc.route(&r)
	dc.auditClock.tick(windowMax, stableBand(maximum))
	return int64(amount)
}

// route returns the next adjustment, giving probes and recovery priority over density steering.
func (dc *densityClimber) route(r *climberReading) float64 {
	dc.ageParkShield()
	dc.ageRetreatCover()

	if dc.isWorkloadShift(r) && dc.anchor.standDown(r) {
		// Discard the old workload's rate history along with its anchor
		dc.rates.reset()
	}
	dc.updateRateReferences(r)

	switch {
	case dc.walking:
		w := &dc.walk
		ending := dc.probeEnding(w, r)
		switch {
		case ending == probeWalking:
			return dc.walkStep(w, false, r)
		case ending != probeConfirmed:
			return dc.undoProbe(w, ending, r)
		case dc.keepConfirmedPosition(w, r):
			if dc.anchor.returning {
				return dc.strideHome(r)
			}
			return 0
		}
		// A starvation confirm resumes density steering in the same sample
		return dc.steer(r.steeringError(), r)
	case dc.hasPendingUndo():
		return dc.undoStride(r)
	case dc.anchor.returning:
		return dc.strideHome(r)
	case dc.anchor.isRetestDue(r):
		dc.retestReturn(r)
		return 0
	case r.hasBlindCorner():
		if dc.isBackingOff() {
			return dc.holdOrAudit(r)
		}
		return dc.armStarvationProbe(r)
	case dc.anchor.vetoTriggered(r, &dc.rates):
		return dc.strideHome(r)
	case dc.auditClock.isDue():
		return dc.armEquilibriumAudit(r)
	case dc.anchor.held:
		// Density would move away from this better position. Wait for an audit to explore again
		return 0
	}
	return dc.steer(r.steeringError(), r)
}

// ageParkShield ages the protection against workload shifts while parked, pausing during a walk.
func (dc *densityClimber) ageParkShield() {
	if dc.isShielded() {
		dc.anchor.ageShield()
	}
}

// ageRetreatCover ages the protection against rate changes caused by a retreat. Each stride renews
// it through the arrival sample.
func (dc *densityClimber) ageRetreatCover() {
	if dc.retreatLeft > 0 {
		dc.retreatLeft--
	}
}

// isShielded reports whether a recently confirmed position is protected from workload shifts.
// Walks suspend this protection so a shift can still invalidate the position during exploration.
func (dc *densityClimber) isShielded() bool {
	return !dc.walking && dc.anchor.isShielded()
}

// isWorkloadShift reports whether the hit-rate change indicates a new workload. Audits of a parked
// position and returns can cause large rate changes themselves, so their results are judged
// separately.
func (dc *densityClimber) isWorkloadShift(r *climberReading) bool {
	return math.Abs(r.hitRateChange) >= climberRestartThreshold &&
		!dc.isShielded() && !dc.isParkTest() && !dc.isReturnTest()
}

// isReturnTest reports whether a retreat or anchor retest is suppressing workload-change
// detection.
func (dc *densityClimber) isReturnTest() bool {
	return dc.retreatLeft > 0 || (dc.anchor.retestClaim >= 0 && !dc.anchor.returning)
}

// isParkTest reports whether an audit is testing a parked position.
func (dc *densityClimber) isParkTest() bool {
	return dc.anchor.held && dc.walking && dc.walk.isAudit
}

// updateRateReferences updates the smoothed hit rate and anchor, using the first sample to
// initialize the rates.
func (dc *densityClimber) updateRateReferences(r *climberReading) {
	if dc.rates.isUnseeded() {
		dc.rates.seed(r.hitRate)
	} else {
		dc.rates.update(r.hitRate)
		dc.anchor.track(r, &dc.rates, dc.isProbing())
	}
}

// isProbing reports whether a probe or its retreat is in progress. A capped retreat may continue
// across several samples after the walk ends.
func (dc *densityClimber) isProbing() bool {
	return dc.walking || dc.hasPendingUndo()
}

/* --------------- Exceptional Scenarios --------------- */

// hasPendingUndo reports whether part of a probe's retreat remains to be applied.
func (dc *densityClimber) hasPendingUndo() bool {
	return dc.undoRemaining != 0
}

// undoStride returns the next stride toward the probe's starting position. It accounts for the
// truncated command, since subtracting fractional strides would leave the retreat short of its
// target.
func (dc *densityClimber) undoStride(r *climberReading) float64 {
	stride := r.cappedStride(float64(dc.undoRemaining))
	dc.undoRemaining -= int64(stride)
	dc.retreatLeft = retreatCover
	return dc.step.commit(stride)
}

// strideHome returns a capped stride of a return towards the anchor.
func (dc *densityClimber) strideHome(r *climberReading) float64 {
	return dc.step.commit(dc.anchor.strideHome(r))
}

// retestReturn discards the anchor after a return if its old hit rate is no longer achievable. A
// return can reach an obsolete anchor without a large enough rate change to trigger workload
// detection.
func (dc *densityClimber) retestReturn(r *climberReading) {
	if dc.anchor.retestFails(&dc.rates) {
		// Subsequent anchors must use rate measurements from the new workload
		dc.rates.reset()
		dc.anchor.standDown(r)
	}
}

// isBackingOff reports whether starvation probes are waiting after a retreat.
func (dc *densityClimber) isBackingOff() bool {
	return dc.refractoryLeft > 0
}

// holdOrAudit starts a due audit or holds the window during backoff. Starvation backoff must not
// delay audits, since a persistently starved window would otherwise remain untested.
func (dc *densityClimber) holdOrAudit(r *climberReading) float64 {
	if dc.auditClock.isDue() {
		return dc.armEquilibriumAudit(r)
	}
	return dc.holdInRefractory(r)
}

// holdInRefractory holds the window during backoff, except to raise it to the minimum size. Sparse
// hits can produce an extreme density ratio, so steering on this sample would defeat the backoff.
// The previous step is left intact for the next walk.
func (dc *densityClimber) holdInRefractory(r *climberReading) float64 {
	dc.refractoryLeft--
	return r.atLeastFloor(0)
}

// armStarvationProbe starts a starvation probe and returns its first step.
func (dc *densityClimber) armStarvationProbe(r *climberReading) float64 {
	w := dc.armProbe(r, r.shouldProbeDown(), false)
	return dc.walkStep(w, true, r)
}

// armEquilibriumAudit starts an audit and returns its first step. Audits explore stable positions
// where density steering and starvation probes would otherwise make no progress.
func (dc *densityClimber) armEquilibriumAudit(r *climberReading) float64 {
	down := dc.auditClock.chooseDirection(r, dc.audit.stride(r), dc.rates.smoothed, dc.anchor.held)
	w := dc.armProbe(r, down, true)
	dc.auditClock.restart()
	return dc.walkStep(w, true, r)
}

// keepConfirmedPosition records a confirmed position and returns whether the window should remain
// there. Audit confirmations suspend density steering, which could undo the improvement.
// Starvation probes normally resume steering; isAuditGrade identifies those with enough evidence
// to park as well.
func (dc *densityClimber) keepConfirmedPosition(w *probeWalk, r *climberReading) bool {
	park := w.isAudit || w.isAuditGrade(r)
	position := w.verdictWindow(r)
	dc.anchor.plant(position, dc.rates.smoothed)
	if park {
		dc.anchor.park(auditWaitInitial)
		if position != r.windowMax {
			dc.anchor.beginReturn()
		}
	} else {
		dc.anchor.release()
	}
	return park
}

/* --------------- Probe Walk --------------- */

// armProbe starts a probe from the current position and its smoothed hit rate. The anchor's rate
// may describe another position or an old workload, so using it could reject real improvements.
func (dc *densityClimber) armProbe(r *climberReading, down, isAudit bool) *probeWalk {
	ladder := &dc.starvation
	if isAudit {
		ladder = &dc.audit
	}
	dc.walk = newProbeWalk(ladder, isAudit, down, r.windowMax,
		r.requestCount, r.hitRate, dc.rates.smoothed, r.probationDensity)
	dc.walking = true
	return &dc.walk
}

// walkStep returns the next step of the probe. Crossing back through its starting position ends
// the probe as a failure; continuing would explore in the opposite direction.
func (dc *densityClimber) walkStep(w *probeWalk, entry bool, r *climberReading) float64 {
	stride := dc.nextStride(w, entry, r)
	if w.crossesBase(float64(r.windowMax) + stride) {
		dc.endWalk()
		w.ladder.crashStreak = 0
		return dc.undoProbe(w, probeFailed, r)
	} else if stride < 0 && float64(r.windowMax)+stride < r.floor {
		stride = r.flooredDescent()
	}
	w.samples++
	return dc.step.commit(stride)
}

// nextStride returns the next probe step. While an audit waits to distinguish a crash from noise,
// it keeps its direction and decays the step. Reversing during that wait could cross the starting
// position and turn a temporary dip into a completed failure.
func (dc *densityClimber) nextStride(w *probeWalk, entry bool, r *climberReading) float64 {
	magnitude := w.ladder.stride(r)
	if entry {
		return w.direction() * magnitude
	} else if w.isAudit && w.belowBarStreak > 0 {
		held := decayedStep(dc.step.size)
		if isFrozenStep(held) {
			return dc.step.atMinimum()
		}
		return held
	}
	bar := w.reversalBar(&dc.rates)
	hitRateChange := r.hitRateChange
	stride := dc.step.heading(hitRateChange > -bar)
	if math.Abs(hitRateChange) >= bar {
		stride = math.Copysign(magnitude, stride)
	} else {
		stride = decayedStep(stride)
	}
	if isFrozenStep(stride) {
		return w.direction() * magnitude
	}
	return stride
}

// probeEnding checks for a crash, then evaluates the probe's progress.
func (dc *densityClimber) probeEnding(w *probeWalk, r *climberReading) probeEnding {
	belowBar := r.hitRate <= w.baseHitRate-w.crashBar(&dc.rates)
	if belowBar {
		w.belowBarStreak++
	} else {
		w.belowBarStreak = 0
	}
	if w.shouldCrashAbort(belowBar) {
		// A workload change can resemble probe damage. Retry the first crash without increasing
		// backoff; repeated crashes suggest the probe itself is harmful
		dc.endWalk()
		w.ladder.crash()
		return probeCrashed
	}
	above := r.hitRate > w.baseSmoothedRate+vetoMarginMin
	if above {
		w.aboveStreak++
	} else {
		w.aboveStreak = 0
	}
	w.rememberBest(above, r)
	w.beatBase = w.beatBase || r.hitRate >= w.baseHitRate
	if w.isAudit {
		return dc.auditEnding(w)
	}
	return dc.starvationEnding(w, r)
}

// auditEnding evaluates an audit by hit rate. Density already favors the original split, so using
// it to judge the audit would reject the alternatives the audit is meant to test.
func (dc *densityClimber) auditEnding(w *probeWalk) probeEnding {
	if w.isConfirmed() {
		dc.endWalk()
		w.ladder.reset()
		dc.refractoryLeft = 0
		dc.starvation.reward()
		dc.auditClock.settle(w.down, dc.rates.smoothed)
		return probeConfirmed
	} else if w.isBudgetSpent() {
		dc.endWalk()
		w.ladder.crashStreak = 0
		return probeFailed
	}
	return probeWalking
}

// starvationEnding evaluates a starvation probe by density. A confirmation only resets the retry
// ladder if it makes new progress that steering will keep. Reversed or repeated confirmations
// escalate the ladder so later probes can explore beyond the same short excursion.
func (dc *densityClimber) starvationEnding(w *probeWalk, r *climberReading) probeEnding {
	if w.canAdjudicate(r, dc.starvation.commitmentDepth()) {
		dc.endWalk()
		w.ladder.crashStreak = 0
		if w.verdictSignal(r)*w.direction() > 0 {
			if w.isReversedBy(r) || w.ladder.isRepeat(w.down, r.windowMax, r.band) {
				w.ladder.escalate()
			} else {
				w.ladder.reward()
			}
			w.ladder.remember(w.down, r.windowMax)
			dc.refractoryLeft = 0
			return probeConfirmed
		}
		return probeFailed
	} else if w.isBudgetSpent() {
		dc.endWalk()
		w.ladder.crashStreak = 0
		return probeFailed
	}
	return probeWalking
}

// undoProbe begins the retreat from a failed or aborted walk and updates its retry schedule. Only
// starvation probes reset the starvation backoff; an audit's retreat leaves an existing wait
// intact.
func (dc *densityClimber) undoProbe(w *probeWalk, ending probeEnding, r *climberReading) float64 {
	crashed := ending == probeCrashed
	failed := !crashed || w.ladder.crashEscalates()
	if failed {
		w.ladder.escalate()
	}
	if w.isAudit {
		dc.auditClock.reschedule(failed, crashed, dc.audit.rung)
	} else {
		dc.refractoryLeft = dc.starvation.rung
		dc.starvation.forget()
	}
	dc.retreatLeft = retreatCover
	return dc.returnToBase(w, r)
}

// returnToBase returns the stride back to the walk's starting window.
func (dc *densityClimber) returnToBase(w *probeWalk, r *climberReading) float64 {
	amount := w.baseWindow - r.windowMax
	stride := r.cappedStride(float64(amount))
	dc.undoRemaining = amount - int64(stride)
	return dc.step.commit(stride)
}

// endWalk ends the walk.
func (dc *densityClimber) endWalk() {
	dc.walking = false
}

// steer returns a proportional density adjustment, respecting the step cap and minimum window.
func (dc *densityClimber) steer(err float64, r *climberReading) float64 {
	magnitude := math.Min(r.maxStep(), math.Abs(err)*densityGain*float64(r.maximum))
	stride := magnitude
	if err < 0 {
		stride = -magnitude
	}
	if stride < 0 && float64(r.windowMax)+stride < r.floor {
		stride = r.flooredDescent()
	}
	return dc.step.commit(r.atLeastFloor(stride))
}

// climberSample holds the current sample's hit and miss counts, with the previous sample's hit
// rate.
type climberSample struct {
	previousHitRate float64
	probationHits   int64
	windowHits      int64
	misses          int64
	hits            int64
}

// recordHit records a cache hit. Callers must exclude zero-weight entries, whose hits would
// inflate density without consuming capacity.
func (s *climberSample) recordHit(inWindow, inProbation bool) {
	s.hits++
	if inWindow {
		s.windowHits++
	} else if inProbation {
		s.probationHits++
	}
}

// recordMiss records a cache miss, including for zero-weight entries. Their misses still represent
// demand, so excluding their hits can make the sampled hit rate lower than the user-visible rate.
func (s *climberSample) recordMiss() {
	s.misses++
}

// requestCount returns the requests seen so far.
func (s *climberSample) requestCount() int64 {
	return s.hits + s.misses
}

// hitRate returns the ratio of requests which were hits.
func (s *climberSample) hitRate() float64 {
	return float64(s.hits) / float64(s.requestCount())
}

// hitRateChange returns the change in hit rate since the previous sample.
func (s *climberSample) hitRateChange() float64 {
	return s.hitRate() - s.previousHitRate
}

// close clears the sample, retaining its hit rate for the next comparison.
func (s *climberSample) close() {
	s.closeAt(s.hitRate())
}

// discard discards the sample, retaining the previous hit rate for the next comparison.
func (s *climberSample) discard() {
	s.closeAt(s.previousHitRate)
}

// reset discards the sample and cross-sample memory.
func (s *climberSample) reset() {
	s.closeAt(0)
}

// closeAt clears the counters and sets the reference hit rate.
func (s *climberSample) closeAt(hitRate float64) {
	s.previousHitRate = hitRate
	s.probationHits = 0
	s.windowHits = 0
	s.misses = 0
	s.hits = 0
}

const (
	// steeringFloorFraction is the minimum hit count used for steering, as a fraction of the
	// starvation threshold.
	steeringFloorFraction float64 = 0.125
	// stableBandFraction is the band within which two positions count as the same, as a fraction
	// of the maximum.
	stableBandFraction float64 = 0.02
	// windowFloorFraction is the density law's lower bound on the window, as a fraction of the
	// maximum.
	windowFloorFraction float64 = 0.02
	// maxStepFraction is the density climber's largest single step, as a fraction of the maximum.
	maxStepFraction float64 = 0.30
	// densityEpsilon is the smoothing constant that keeps a starved region's density ratio defined.
	densityEpsilon float64 = 1e-9
	// minStarvationBar is the minimum hit count needed to measure a region, even for short samples.
	minStarvationBar int64 = 4
	// minSignalShift is the right-shift of the sample's request count that sets the starvation bar.
	minSignalShift = 10
)

// climberReading holds the measurements and region bounds used for one density adjustment.
type climberReading struct {
	probationDensity float64
	hitRateChange    float64
	windowDensity    float64
	mainDensity      float64
	hitRate          float64
	floor            float64

	windowStarved bool
	mainStarved   bool

	requestCount int64
	windowHits   int64
	windowMax    int64
	mainHits     int64
	maximum      int64
	band         int64
	bar          int64
}

func newClimberReading(sample *climberSample, maximum, windowMax, mainProtectedMax int64) climberReading {
	mainHits := sample.hits - sample.windowHits
	bar := max(minStarvationBar, sample.requestCount()>>minSignalShift)
	return climberReading{
		mainHits:         mainHits,
		bar:              bar,
		mainDensity:      float64(mainHits) / float64(max(1, maximum-windowMax)),
		windowDensity:    float64(sample.windowHits) / float64(max(1, windowMax)),
		probationDensity: float64(sample.probationHits) / float64(max(1, maximum-windowMax-mainProtectedMax)),
		windowStarved:    sample.windowHits < bar,
		hitRateChange:    sample.hitRateChange(),
		floor:            windowFloorFraction * float64(maximum),
		requestCount:     sample.requestCount(),
		mainStarved:      mainHits < bar,
		windowHits:       sample.windowHits,
		hitRate:          sample.hitRate(),
		band:             stableBand(maximum),
		windowMax:        windowMax,
		maximum:          maximum,
	}
}

// stableBand returns the band, in entries, within which a position counts as the same place.
func stableBand(maximum int64) int64 {
	return max(1, int64(stableBandFraction*float64(maximum)))
}

// upperCorner returns the smallest window that leaves main at most a quarter of the cache.
func (r *climberReading) upperCorner() int64 {
	return r.maximum - (r.maximum >> 2)
}

// maxStep returns the largest single move any command may make at this size.
func (r *climberReading) maxStep() float64 {
	return maxStepFraction * float64(r.maximum)
}

// cappedStride returns a stride towards a target this far away, capped at the maximum step.
func (r *climberReading) cappedStride(amount float64) float64 {
	limit := r.maxStep()
	if math.Abs(amount) > limit {
		return math.Copysign(limit, amount)
	}
	return amount
}

// restartMagnitude returns the initial step size for this cache.
func (r *climberReading) restartMagnitude() float64 {
	return restartMagnitude(r.maximum)
}

// flooredDescent returns a descent limited by the floor, preserving negative zero to retain its
// direction.
func (r *climberReading) flooredDescent() float64 {
	return math.Min(math.Copysign(0, -1), r.floor-float64(r.windowMax))
}

// atLeastFloor increases the stride if needed to raise an undersized window to the floor.
func (r *climberReading) atLeastFloor(stride float64) float64 {
	if float64(r.windowMax) < r.floor {
		return math.Max(stride, r.floor-float64(r.windowMax))
	}
	return stride
}

// isDeadSample reports whether neither region has enough hits for a reliable density estimate.
func (r *climberReading) isDeadSample() bool {
	return r.windowStarved && r.mainStarved
}

// hasBlindCorner reports whether sparse hits require a starvation probe. A starved main beside a
// large window is left to equilibrium audits; probing it could shrink a productive window.
func (r *climberReading) hasBlindCorner() bool {
	return r.isDeadSample() || (r.windowStarved && r.windowMax <= (r.maximum>>2))
}

// shouldProbeDown reports whether the probe should shrink the window to move away from the nearer
// bound.
func (r *climberReading) shouldProbeDown() bool {
	return r.isDeadSample() && r.windowMax >= (r.maximum>>1)
}

// error returns the log density ratio used by probe verdicts.
func (r *climberReading) error() float64 {
	return math.Log((r.windowDensity + densityEpsilon) / (r.mainDensity + densityEpsilon))
}

// steeringError returns the log density ratio with minimum hit counts to limit sparse-sample
// steps.
func (r *climberReading) steeringError() float64 {
	windowFloor := (steeringFloorFraction * float64(r.bar)) / float64(max(1, r.windowMax))
	mainFloor := (steeringFloorFraction * float64(r.bar)) / float64(max(1, r.maximum-r.windowMax))
	return math.Log(math.Max(r.windowDensity, windowFloor) / math.Max(r.mainDensity, mainFloor))
}

const (
	// minInitialStep is the lower bound on the initial step size so that small caches have an
	// opportunity to adapt.
	minInitialStep float64 = 2
	// stepPercent is the initial step size as a fraction of the cache's maximum size.
	stepPercent float64 = 0.0625
	// stepDecayRate is the multiplier used to reduce the step size toward convergence.
	stepDecayRate float64 = 0.98
)

// climberStep is the signed adjustment used to continue, reverse, or decay the climber's next
// step.
type climberStep struct {
	size float64
}

// newClimberStep returns a step with the initial magnitude and direction.
func newClimberStep(maximum int64, grow bool) climberStep {
	magnitude := restartMagnitude(maximum)
	if grow {
		return climberStep{size: magnitude}
	}
	return climberStep{size: -magnitude}
}

// commit records and returns the step for this sample.
func (s *climberStep) commit(step float64) float64 {
	s.size = step
	return step
}

// heading returns the last step, repeated or reversed.
func (s *climberStep) heading(forward bool) float64 {
	if forward {
		return s.size
	}
	return -s.size
}

// magnitude returns the step's size, ignoring direction.
func (s *climberStep) magnitude() float64 {
	return math.Abs(s.size)
}

// atMinimum returns the minimum step size in the current direction.
func (s *climberStep) atMinimum() float64 {
	return math.Copysign(minInitialStep, s.size)
}

// restartMagnitude returns the initial step size for the given maximum.
func restartMagnitude(maximum int64) float64 {
	return math.Max(stepPercent*float64(maximum), minInitialStep)
}

// decayedStep returns the step reduced by one decay factor.
func decayedStep(step float64) float64 {
	return stepDecayRate * step
}

// isFrozenStep reports whether the step needs to be restarted. In the density tier, the floor
// clamp can reduce a step this far, but decay alone cannot do so within a walk's budget.
func isFrozenStep(step float64) bool {
	return math.Abs(step) < minInitialStep
}

// The crash and reversal thresholds test different losses: the drop from the starting hit rate,
// and the drop since the previous sample. A starvation probe scales both thresholds with the
// workload's deviation, subject to a floor and cap. The deviation stays live so the walk can
// tolerate its own transient effects; freezing it at the start aborts useful walks.
//
// An audit's crash threshold stays absolute. Widening it with the deviation allows too many audits
// to confirm and hold positions that density would reject. At low hit rates the threshold is
// capped at a fraction of the starting rate, since an absolute threshold could exceed the entire
// rate and never detect a loss.
//
// The audit's reversal threshold also accounts for deviation to avoid reversing on ordinary sample
// noise. The absolute cap prevents excessive tolerance on noisy workloads. A reversal through the
// starting window counts as a failed walk and increases the retry wait.
//
// Starvation probes abort on the first sample below the crash threshold. An audit retry after a
// crash requires consecutive such samples, allowing it to cross a temporary dip. Extending this
// duration tolerates brief losses without raising the allowed drop.
//
// Audit confirmation requires a streak of raw samples above the starting smoothed rate, after a
// minimum number of steps. Scaling this margin by deviation, as the guard rail does, would obscure
// the window's small contribution to the total rate. A false confirmation can be corrected by the
// next audit; a false veto repeatedly returns to the anchor. The walk must also match its starting
// raw rate at least once, since that sample may already exceed the smoothed reference. Equality is
// allowed so a perfect starting rate can still be confirmed.
//
// A starvation probe compares densities once the watched region has enough hits. An up-probe uses
// the initial probation density: growing the window displaces probation entries, while main's
// average overvalues that capacity by including the protected core. The baseline is frozen because
// the walk's own demotions inflate live probation density and can prevent any confirmation. A
// down-probe uses average densities; the window has no comparable subdivision.
//
// Every walk has a sample budget. Neither the density verdict nor the hit-rate thresholds
// guarantee termination, and a walk may continue making small losses below those thresholds.
const (
	// auditBarFraction is the scale shared by the audit's crash and reversal thresholds. The crash
	// threshold uses the starting rate; the reversal threshold uses the larger of that rate and the
	// noise band.
	auditBarFraction float64 = 0.15
	// auditCrashPersistence is the consecutive below-threshold samples required to abort an audit
	// retry after a crash.
	auditCrashPersistence = 3
	// auditConfirmStreak is the consecutive raw samples above the frozen reference that confirm an
	// audit.
	auditConfirmStreak = 4
	// auditCommitment is the minimum samples before an audit can confirm.
	auditCommitment = 5
	// probeExitBarMultiple is the hit count required for a density verdict, as a multiple of the
	// starvation threshold.
	probeExitBarMultiple int64 = 4
	// probeBarCap is the starvation probe's maximum crash threshold, in multiples of the restart
	// threshold.
	probeBarCap float64 = 3
	// probeWalkBudget is the maximum samples before an unconfirmed walk fails.
	probeWalkBudget = 16
)

// probeWalk is a bounded sequence of window adjustments used by a starvation probe or equilibrium
// audit.
type probeWalk struct {
	baseProbationDensity float64
	baseSmoothedRate     float64
	baseRequestCount     int64
	baseHitRate          float64
	baseWindow           int64

	isAudit bool
	ladder  *probeLadder
	down    bool

	belowBarStreak int
	aboveStreak    int
	samples        int

	beatBase   bool
	bestRate   float64
	bestWindow int64
}

func newProbeWalk(ladder *probeLadder, isAudit, down bool, baseWindow, baseRequestCount int64,
	baseHitRate, baseSmoothedRate, baseProbationDensity float64,
) probeWalk {
	return probeWalk{
		baseProbationDensity: baseProbationDensity,
		baseSmoothedRate:     baseSmoothedRate,
		baseRequestCount:     baseRequestCount,
		baseHitRate:          baseHitRate,
		baseWindow:           baseWindow,
		isAudit:              isAudit,
		ladder:               ladder,
		bestWindow:           -1,
		bestRate:             -1,
		down:                 down,
	}
}

// rememberBest records the best position within the current confirmation streak. A broken streak
// clears the record, and ties favor the later position to avoid unnecessary backtracking.
func (w *probeWalk) rememberBest(above bool, r *climberReading) {
	if !above {
		w.bestWindow = -1
		w.bestRate = -1
	} else if r.hitRate >= w.bestRate {
		w.bestWindow = r.windowMax
		w.bestRate = r.hitRate
	}
}

// verdictWindow returns the confirmed position. An audit may have passed a better position while
// collecting its confirmation streak, so it returns there if the difference exceeds the
// confirmation margin. A starvation probe evaluates the current sample and keeps its position.
func (w *probeWalk) verdictWindow(r *climberReading) int64 {
	better := w.isAudit && w.bestWindow >= 0 && w.bestRate > r.hitRate+vetoMarginMin
	if better {
		return w.bestWindow
	}
	return r.windowMax
}

// direction returns the sign of the walk's direction.
func (w *probeWalk) direction() float64 {
	if w.down {
		return -1
	}
	return 1
}

// isBudgetSpent reports whether the walk has exhausted its sample budget.
func (w *probeWalk) isBudgetSpent() bool {
	return w.samples >= probeWalkBudget
}

// crossesBase reports whether the proposed position crosses back through the starting window.
func (w *probeWalk) crossesBase(position float64) bool {
	if w.down {
		return position > float64(w.baseWindow)
	}
	return position < float64(w.baseWindow)
}

// crashBar returns the drop from the starting hit rate that makes a sample count as a crash.
func (w *probeWalk) crashBar(rates *climberRates) float64 {
	if w.isAudit {
		return math.Min(climberRestartThreshold, auditBarFraction*w.baseHitRate)
	}
	return math.Min(probeBarCap*climberRestartThreshold, math.Max(climberRestartThreshold, rates.noiseBand()))
}

// reversalBar returns the drop since the previous sample that reverses the walk's step.
func (w *probeWalk) reversalBar(rates *climberRates) float64 {
	if w.isAudit {
		return math.Min(climberRestartThreshold, auditBarFraction*math.Max(w.baseHitRate, rates.noiseBand()))
	}
	return w.crashBar(rates)
}

// canAdjudicate reports whether a starvation probe has enough hits and has reached the required
// depth for a density verdict.
func (w *probeWalk) canAdjudicate(r *climberReading, commitment int) bool {
	watched := r.windowHits
	if w.down {
		watched = r.mainHits
	}
	return watched >= probeExitBarMultiple*r.bar && w.samples >= commitment
}

// verdictSignal returns the density comparison, scaling the frozen baseline to the current sample
// length.
func (w *probeWalk) verdictSignal(r *climberReading) float64 {
	if w.down {
		return r.error()
	}
	baseline := w.baseProbationDensity * (float64(r.requestCount) / float64(max(1, w.baseRequestCount)))
	return math.Log((r.windowDensity + densityEpsilon) / (baseline + densityEpsilon))
}

// shouldCrashAbort reports whether to abort on this loss, allowing an audit retry to tolerate
// brief dips.
func (w *probeWalk) shouldCrashAbort(belowBar bool) bool {
	tolerant := w.isAudit && w.ladder.hasCrashed()
	return belowBar && (!tolerant || w.belowBarStreak >= auditCrashPersistence)
}

// isConfirmed reports whether the walk meets the audit's depth, streak, and starting-rate tests.
func (w *probeWalk) isConfirmed() bool {
	return w.samples >= auditCommitment && w.aboveStreak >= auditConfirmStreak && w.beatBase
}

// isReversedBy reports whether density steering would immediately move back from the confirmed
// position.
func (w *probeWalk) isReversedBy(r *climberReading) bool {
	return r.steeringError()*w.direction() < 0
}

// isAuditGrade reports whether a deep starvation walk meets the audit's confirmation tests but
// would be reversed by density steering. This position must be held to retain the improvement.
func (w *probeWalk) isAuditGrade(r *climberReading) bool {
	return w.samples >= probeCommitmentDeep && w.isReversedBy(r) && w.isConfirmed()
}

const (
	// probeStrideScaleMid is the stride multiplier after one backoff doubling.
	probeStrideScaleMid float64 = 2
	// probeStrideScaleDeep is the stride multiplier at the maximum backoff.
	probeStrideScaleDeep float64 = 4
	// probeCommitmentMid is the minimum samples before a density verdict after one backoff
	// doubling.
	probeCommitmentMid = 2
	// probeCommitmentDeep is the minimum samples before a density verdict at the maximum backoff.
	probeCommitmentDeep = 10
	// probeCrashEscalation is the consecutive crashes before a retry escalates as it would after a
	// failed walk.
	probeCrashEscalation = 2
	// probeBackoffInitial is the initial backoff, in samples, before escalation.
	probeBackoffInitial = 16
	// probeBackoffMax is the maximum backoff rung, in samples.
	probeBackoffMax = 64
)

// probeLadder is the retry state for starvation probes or audits. Repeated unsuccessful walks
// increase both the wait and the extent of exploration. Each layer has its own ladder so failures
// in one do not delay exploration by the other.
type probeLadder struct {
	farthestDown bool
	crashStreak  int
	farthest     int64
	rung         int
}

func newProbeLadder() probeLadder {
	var l probeLadder
	l.reset()
	return l
}

// reset restores the initial backoff and clears the walk history.
func (l *probeLadder) reset() {
	l.rung = probeBackoffInitial
	l.crashStreak = 0
	l.forget()
}

// forget clears the farthest confirmed position.
func (l *probeLadder) forget() {
	l.farthest = -1
}

// isRepeat reports whether this confirmation makes no further progress than an earlier walk in
// the same direction. Repeatedly recovering a lost position should not reset the backoff.
func (l *probeLadder) isRepeat(down bool, window, band int64) bool {
	if l.farthest < 0 || down != l.farthestDown {
		return false
	}
	if down {
		return window >= l.farthest-band
	}
	return window <= l.farthest+band
}

// remember records a confirmed window that lies beyond the farthest, or in the other direction.
func (l *probeLadder) remember(down bool, window int64) {
	farther := l.farthest < 0 || down != l.farthestDown ||
		(down && window < l.farthest) || (!down && window > l.farthest)
	if farther {
		l.farthestDown = down
		l.farthest = window
	}
}

// escalate doubles the backoff, up to its maximum.
func (l *probeLadder) escalate() {
	l.rung = min(probeBackoffMax, 2*l.rung)
}

// crash records a consecutive crash, saturating at the escalation threshold.
func (l *probeLadder) crash() {
	l.crashStreak = min(probeCrashEscalation, l.crashStreak+1)
}

// reward resets the crash streak and shortens the next retry after a successful walk.
func (l *probeLadder) reward() {
	l.crashStreak = 0
	l.rung = 1
}

// commitmentDepth returns the minimum samples before a density verdict. Early retries can stop
// cheaply, while deeper retries continue past incidental hits that would otherwise end
// exploration too soon.
func (l *probeLadder) commitmentDepth() int {
	switch {
	case l.rung >= probeBackoffMax:
		return probeCommitmentDeep
	case l.rung >= 2*probeBackoffInitial:
		return probeCommitmentMid
	default:
		return 0
	}
}

// stride returns the starting stride for this rung. The audit's direction check uses this same
// distance so it does not choose a direction with too little room for the first step.
func (l *probeLadder) stride(r *climberReading) float64 {
	return math.Min(r.maxStep(), l.strideScale()*r.restartMagnitude())
}

// strideScale returns the stride multiplier. Deeper retries need larger steps as well as more
// samples to reach beyond broad regions of incidental hits.
func (l *probeLadder) strideScale() float64 {
	switch {
	case l.rung >= probeBackoffMax:
		return probeStrideScaleDeep
	case l.rung >= 2*probeBackoffInitial:
		return probeStrideScaleMid
	default:
		return 1
	}
}

// hasCrashed reports whether this layer's last walk crashed.
func (l *probeLadder) hasCrashed() bool {
	return l.crashStreak >= 1
}

// crashEscalates reports whether consecutive crashes require the same backoff as a failed walk.
func (l *probeLadder) crashEscalates() bool {
	return l.crashStreak >= probeCrashEscalation
}

const (
	// auditWaitInitial is the stillness samples before a routine equilibrium audit.
	auditWaitInitial = 32
	// auditWaitFirst is the shorter wait for the first audit after a resize.
	auditWaitFirst = 4
	// auditWaitMax is the longest wait between audits.
	auditWaitMax = 512
)

// auditClock holds the timing and direction of equilibrium audits. Stillness is measured by the
// window's actual position, since changing hit rates or adjustments blocked by a boundary can
// conceal a stable allocation that needs testing.
type auditClock struct {
	settledRate  float64
	stillSamples int
	waitSamples  int
	lastWindow   int64
	down         bool
}

func newAuditClock() auditClock {
	a := auditClock{down: true}
	a.reset()
	return a
}

// reset restores the initial schedule without changing the preferred direction.
func (a *auditClock) reset() {
	a.waitSamples = auditWaitFirst
	a.settledRate = math.NaN()
	a.stillSamples = 0
	a.lastWindow = -1
}

// restart restarts the stillness count when an audit begins.
func (a *auditClock) restart() {
	a.stillSamples = 0
}

// settle restores the standard wait and continues in the confirmed walk's direction. Confirmation
// stopped the walk while it was improving, so the next audit tests for further improvement.
func (a *auditClock) settle(down bool, rate float64) {
	a.waitSamples = auditWaitInitial
	a.settledRate = rate
	a.down = down
}

// tick updates the stillness count. A moving sample decrements the count rather than resetting
// it, so occasional movement cannot suppress audits indefinitely. The first sample counts as still
// because no adjustment precedes it.
func (a *auditClock) tick(windowMax, band int64) {
	samePlace := a.lastWindow < 0 || absInt64(windowMax-a.lastWindow) <= band
	if samePlace {
		a.stillSamples = min(math.MaxInt32-1, a.stillSamples) + 1
	} else {
		a.stillSamples = max(0, a.stillSamples-1)
	}
	a.lastWindow = windowMax
}

// isDue reports whether the position has been still long enough for an audit.
func (a *auditClock) isDue() bool {
	return a.stillSamples >= a.waitSamples
}

// reschedule sets the next wait from the audit's backoff. A completed failure at the deepest rung
// doubles the wait. A crash uses the rung directly, since a workload shift calls for another audit
// without that extra delay.
func (a *auditClock) reschedule(failed, crashed bool, rung int) {
	if failed && !crashed && rung >= probeBackoffMax {
		a.waitSamples = min(auditWaitMax, 2*max(a.waitSamples, rung))
	} else {
		a.waitSamples = max(probeBackoffInitial, rung)
	}
}

// chooseDirection returns an audit direction with room for the first stride. The first audit
// after a confirmation continues the walk only while the position is held and its smoothed rate
// remains close to the confirmed rate. Otherwise, exploration alternates directions. A
// corner-forced direction leaves the alternation unchanged to avoid retracing it later.
func (a *auditClock) chooseDirection(r *climberReading, stride, rate float64, parked bool) bool {
	if !math.IsNaN(a.settledRate) {
		if !parked || math.Abs(rate-a.settledRate) >= climberRestartThreshold {
			a.down = !a.down
		}
		a.settledRate = math.NaN()
	}
	if r.windowMax <= int64(2*r.floor) {
		return false
	} else if r.windowMax >= r.upperCorner() {
		return true
	}
	var room float64
	if a.down {
		room = float64(r.windowMax) - r.floor
	} else {
		room = float64(r.upperCorner() - r.windowMax)
	}
	if room < stride {
		a.down = !a.down
	}
	chosen := a.down
	a.down = !chosen
	return chosen
}

const (
	// vetoReturnBudget is the maximum samples allowed for a return to the anchor.
	vetoReturnBudget = 8
	// retestSettle is the settling samples at the anchor before its reference rate is retested.
	retestSettle = 2
	// vetoStreak is the shortfall samples that sustain a guard-rail veto.
	vetoStreak = 4
)

// climberAnchor is a reference position and hit rate used to reject sustained regressions. It can
// hold a confirmed position or return to it after a loss. This supplements density steering, whose
// equilibrium need not maximize the hit rate.
//
// A confirmed position has a grace period for large hit-rate changes. That period belongs to its
// hold and ends when the hold is released. A veto also holds the window, but does not renew the
// grace period because returning to an old position is not new evidence for it.
//
// A return rechecks the reference rate on arrival. The retest freezes the rate at departure and is
// valid only for that position; discarding or replacing the anchor cancels it.
type climberAnchor struct {
	shortfallStreak int
	retestClaim     float64
	returning       bool
	returnLeft      int
	settleLeft      int
	freshLeft       int
	held            bool
	window          int64
	rate            float64
}

func newClimberAnchor() climberAnchor {
	var a climberAnchor
	a.reset()
	return a
}

// reset clears the reference position and any pending return.
func (a *climberAnchor) reset() {
	a.shortfallStreak = 0
	a.returnLeft = 0
	a.endReturn()
	a.discard()
}

// discard discards the reference position, releasing its hold and cancelling any pending retest.
func (a *climberAnchor) discard() {
	a.release()
	a.endRetest()
	a.window = -1
}

// isPlanted reports whether a reference position has been recorded.
func (a *climberAnchor) isPlanted() bool {
	return a.window >= 0
}

// isAt reports whether the window is within the tolerance band of the anchor.
func (a *climberAnchor) isAt(windowMax, band int64) bool {
	return a.isPlanted() && absInt64(windowMax-a.window) <= band
}

// isAwayFrom reports whether the window is outside the tolerance band of an existing anchor.
func (a *climberAnchor) isAwayFrom(windowMax, band int64) bool {
	return a.isPlanted() && !a.isAt(windowMax, band)
}

// plant records a position and its reference rate. Any pending retest is cancelled because its
// frozen rate belongs to the previous anchor, even if the new anchor is at the current window.
func (a *climberAnchor) plant(windowMax int64, claimed float64) {
	a.window = windowMax
	a.rate = claimed
	a.endRetest()
}

// resync refreshes the reference rate from a measurement at the anchor.
func (a *climberAnchor) resync(claimed float64) {
	a.rate = claimed
}

// track refreshes the rate at the anchor or records a better position. A new position must be
// settled so its reference rate does not come from a walk or return still in progress. Refreshing
// at the anchor is always allowed; later samples gradually remove any transient measurements.
func (a *climberAnchor) track(r *climberReading, rates *climberRates, probing bool) {
	settled := !probing && !a.returning
	switch {
	case !a.isPlanted():
		if settled {
			a.plant(r.windowMax, rates.smoothed)
		}
	case a.isAt(r.windowMax, r.band):
		a.resync(rates.smoothed)
	case settled && rates.smoothed > a.rate+rates.vetoMargin():
		a.plant(r.windowMax, rates.smoothed)
		a.release()
	}
}

// park starts a hold with a grace period for large hit-rate changes after confirmation.
func (a *climberAnchor) park(shield int) {
	a.freshLeft = shield
	a.held = true
}

// hold starts a guard-rail hold without changing the remaining grace period.
func (a *climberAnchor) hold() {
	a.held = true
}

// release releases the hold and ends its grace period.
func (a *climberAnchor) release() {
	a.freshLeft = 0
	a.held = false
}

// isShielded reports whether a confirmed position is still held within its grace period.
func (a *climberAnchor) isShielded() bool {
	return a.held && a.freshLeft > 0
}

// ageShield consumes one sample of the grace period.
func (a *climberAnchor) ageShield() {
	a.freshLeft--
}

// vetoTriggered begins a return and reports a veto after a sustained shortfall exceeds the noise
// margin.
func (a *climberAnchor) vetoTriggered(r *climberReading, rates *climberRates) bool {
	if a.isAwayFrom(r.windowMax, r.band) && rates.smoothed < a.rate-rates.vetoMargin() {
		a.shortfallStreak++
		if a.shortfallStreak >= vetoStreak {
			a.shortfallStreak = 0
			a.beginReturn()
			return true
		}
	} else {
		a.shortfallStreak = 0
	}
	return false
}

// standDown releases the hold and stops any return, reporting whether the anchor was discarded.
// Only a change at the anchor invalidates its rate; a change elsewhere may result from the
// controller's own movement.
func (a *climberAnchor) standDown(r *climberReading) bool {
	discarded := a.isAt(r.windowMax, r.band)
	if discarded {
		a.discard()
	}
	a.release()
	a.endReturn()
	a.shortfallStreak = 0
	return discarded
}

// beginReturn begins a bounded return to the anchor and freezes its rate for the retest on
// arrival.
func (a *climberAnchor) beginReturn() {
	a.returnLeft = vetoReturnBudget
	a.settleLeft = retestSettle
	a.retestClaim = a.rate
	a.returning = true
	a.hold()
}

// strideHome returns a capped step toward the anchor. The return ends when the step reaches the
// anchor or uses the last budgeted sample.
func (a *climberAnchor) strideHome(r *climberReading) float64 {
	a.returnLeft--
	remaining := float64(a.window - r.windowMax)
	if math.Abs(remaining) <= r.maxStep() || a.returnLeft <= 0 {
		a.endReturn()
	}
	return r.cappedStride(remaining)
}

// endReturn ends the return, including one that exhausted its budget before arrival.
func (a *climberAnchor) endReturn() {
	a.returning = false
}

// isRetestDue reports whether the return has ended at the anchor with a retest pending. An
// incomplete return cannot test the reference rate and cancels the retest.
func (a *climberAnchor) isRetestDue(r *climberReading) bool {
	if a.retestClaim < 0 || a.returning {
		return false
	} else if !a.isAt(r.windowMax, r.band) {
		a.endRetest()
		return false
	}
	return true
}

// retestFails reports whether the settled rate falls short of the reference frozen at departure.
// Using the live reference would hide the shortfall as measurements at the anchor refresh it.
func (a *climberAnchor) retestFails(rates *climberRates) bool {
	a.settleLeft--
	if a.settleLeft > 0 {
		return false
	}
	claimed := a.retestClaim
	a.endRetest()
	return rates.smoothed < claimed-rates.vetoMargin()
}

// endRetest clears a completed or cancelled retest.
func (a *climberAnchor) endRetest() {
	a.retestClaim = -1
	a.settleLeft = 0
}

const (
	// vetoMarginScale is the shortfall margin as a multiple of the smoothed hit-rate deviation.
	vetoMarginScale float64 = 3
	// vetoMarginMin is the floor on the guard rail's shortfall margin, in absolute hit rate.
	vetoMarginMin float64 = 0.01
	// deviationSeed is the initial deviation estimate, allowing for cold-start variability.
	deviationSeed float64 = 0.05
	// rateSmoothing is the smoothing constant for the goal-metric references (~5 sample memory).
	rateSmoothing float64 = 0.2
)

// climberRates holds a smoothed hit rate and mean absolute deviation. The deviation determines the
// noise margin for guard-rail vetoes and starvation probes; audit confirmation uses a fixed margin
// instead.
type climberRates struct {
	deviation float64
	smoothed  float64
}

func newClimberRates() climberRates {
	var r climberRates
	r.reset()
	return r
}

// reset clears the smoothed rate and restores the initial deviation.
func (r *climberRates) reset() {
	r.smoothed = math.NaN()
	r.deviation = deviationSeed
}

// isUnseeded reports whether the initial hit rate has yet to be recorded.
func (r *climberRates) isUnseeded() bool {
	return math.IsNaN(r.smoothed)
}

// seed starts the smoothed rate at this sample.
func (r *climberRates) seed(hitRate float64) {
	r.smoothed = hitRate
}

// update updates both estimates, measuring deviation from the previous smoothed rate.
func (r *climberRates) update(hitRate float64) {
	r.deviation += rateSmoothing * (math.Abs(hitRate-r.smoothed) - r.deviation)
	r.smoothed += rateSmoothing * (hitRate - r.smoothed)
}

// noiseBand returns a noise margin of three current deviations. Walks use the live estimate to
// tolerate their own transient effects; see the notes on the walk's thresholds.
func (r *climberRates) noiseBand() float64 {
	return vetoMarginScale * r.deviation
}

// vetoMargin returns the guard rail's noise margin, with a minimum for quiet workloads.
func (r *climberRates) vetoMargin() float64 {
	return math.Max(vetoMarginMin, r.noiseBand())
}

// probeEnding is the outcome of a walk sample.
type probeEnding uint8

const (
	// probeWalking means the walk continues with another step.
	probeWalking probeEnding = iota
	// probeConfirmed means the walk's confirmation criteria were met.
	probeConfirmed
	// probeCrashed means a hit-rate loss requires returning to the starting position.
	probeCrashed
	// probeFailed means the walk failed to confirm an improvement and requires a longer backoff.
	probeFailed
)

func absInt64(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}
