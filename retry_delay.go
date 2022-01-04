package eventsource

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// Encapsulation of configurable backoff/jitter behavior.
//
// - The system can either be in a "good" state or a "bad" state. The initial state is "bad"; the
// caller is responsible for indicating when it transitions to "good". When we ask for a new retry
// delay, that implies the state is now transitioning to "bad".
//
// - There is a configurable base delay, which can be changed at any time (if the SSE server sends
// us a "retry:" directive).
//
// - There are optional strategies for applying backoff and jitter to the delay.
//
// This object is meant to be used from a single goroutine once it's been created; its methods are
// not safe for concurrent use.
type retryDelayStrategy struct {
	baseDelay     time.Duration
	backoff       backoffStrategy
	jitter        jitterStrategy
	resetInterval time.Duration
	retryCount    int
	goodSince     time.Time // nonzero only if the state is currently "good"
	lock          sync.Mutex
}

// Abstraction for backoff delay behavior.
type backoffStrategy interface {
	applyBackoff(baseDelay time.Duration, retryCount int) time.Duration
}

// Abstraction for delay jitter behavior.
type jitterStrategy interface {
	applyJitter(computedDelay time.Duration) time.Duration
}

type defaultBackoffStrategy struct {
	maxDelay time.Duration
}

// Creates the default implementation of exponential backoff, which doubles the delay each time up to
// the specified maximum.
//
// If a resetInterval was specified for the retryDelayStrategy, and the system has been in a "good"
// state for at least that long, the delay is reset back to the base. This avoids perpetually increasing
// delays in a situation where failures are rare).
func newDefaultBackoff(maxDelay time.Duration) backoffStrategy {
	return defaultBackoffStrategy{maxDelay}
}

func (s defaultBackoffStrategy) applyBackoff(baseDelay time.Duration, retryCount int) time.Duration {
	d := math.Min(float64(baseDelay)*math.Pow(2, float64(retryCount)), float64(s.maxDelay))
	return time.Duration(d)
}

type defaultJitterStrategy struct {
	ratio  float64
	random *rand.Rand
}

// Creates the default implementation of jitter, which subtracts a pseudo-random amount from each delay.
// The ratio parameter should be greater than 0 and less than or equal to 1.0.
func newDefaultJitter(ratio float64, randSeed int64) jitterStrategy {
	if randSeed <= 0 {
		randSeed = time.Now().UnixNano()
	}
	if ratio > 1.0 {
		ratio = 1.0
	}
	return &defaultJitterStrategy{ratio, rand.New(rand.NewSource(randSeed))}
}

func (s *defaultJitterStrategy) applyJitter(computedDelay time.Duration) time.Duration {
	// retryCount doesn't matter here - it's included in the int
	jitter := time.Duration(s.random.Int63n(int64(float64(computedDelay) * s.ratio)))
	return computedDelay - jitter
}

// Creates a retryDelayStrategy.
func newRetryDelayStrategy(
	baseDelay time.Duration,
	resetInterval time.Duration,
	backoff backoffStrategy,
	jitter jitterStrategy,
) *retryDelayStrategy {
	return &retryDelayStrategy{
		baseDelay:     baseDelay,
		resetInterval: resetInterval,
		backoff:       backoff,
		jitter:        jitter,
	}
}

// NextRetryDelay computes the next retry interval. This also sets the current state to "bad".
//
// Note that currentTime is passed as a parameter instead of computed by this function to guarantee predictable
// behavior in tests.
func (r *retryDelayStrategy) NextRetryDelay(currentTime time.Time) time.Duration {
	r.lock.Lock()
	defer r.lock.Unlock()

	if !r.goodSince.IsZero() && r.resetInterval > 0 && (currentTime.Sub(r.goodSince) >= r.resetInterval) {
		r.retryCount = 0
	}
	r.goodSince = time.Time{}
	delay := r.baseDelay
	if r.backoff != nil {
		delay = r.backoff.applyBackoff(delay, r.retryCount)
	}
	r.retryCount++
	if r.jitter != nil {
		delay = r.jitter.applyJitter(delay)
	}
	return delay
}

// SetGoodSince marks the current state as "good" and records the time. See comments on the backoff type.
func (r *retryDelayStrategy) SetGoodSince(goodSince time.Time) {
	r.lock.Lock()
	r.goodSince = goodSince
	r.lock.Unlock()
}

// SetBaseDelay changes the initial retry delay and resets the backoff (if any) so the next retry will use
// that value.
//
// This is used to implement the optional SSE behavior where the server sends a "retry:" command to
// set the base retry to a specific value. Note that we will still apply a jitter, if jitter is enabled,
// and subsequent retries will still increase exponentially.
func (r *retryDelayStrategy) SetBaseDelay(baseDelay time.Duration) {
	r.lock.Lock()
	r.baseDelay = baseDelay
	r.retryCount = 0
	r.lock.Unlock()
}

func (r *retryDelayStrategy) hasJitter() bool { //nolint:megacheck // used only in tests
	return r.jitter != nil
}
