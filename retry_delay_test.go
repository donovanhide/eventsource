package eventsource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFixedRetryDelay(t *testing.T) {
	d0 := time.Second * 10
	r := newRetryDelayStrategy(d0, 0, nil, nil)
	t0 := time.Now().Add(-time.Minute)
	d1 := r.NextRetryDelay(t0)
	d2 := r.NextRetryDelay(t0.Add(time.Second))
	d3 := r.NextRetryDelay(t0.Add(time.Second * 2))
	assert.Equal(t, d0, d1)
	assert.Equal(t, d0, d2)
	assert.Equal(t, d0, d3)
}

func TestBackoffWithoutJitter(t *testing.T) {
	d0 := time.Second * 10
	max := time.Minute
	r := newRetryDelayStrategy(d0, 0, newDefaultBackoff(max), nil)
	t0 := time.Now().Add(-time.Minute)
	d1 := r.NextRetryDelay(t0)
	d2 := r.NextRetryDelay(t0.Add(time.Second))
	d3 := r.NextRetryDelay(t0.Add(time.Second * 2))
	d4 := r.NextRetryDelay(t0.Add(time.Second * 3))
	assert.Equal(t, d0, d1)
	assert.Equal(t, d0*2, d2)
	assert.Equal(t, d0*4, d3)
	assert.Equal(t, max, d4)
}

func TestJitterWithoutBackoff(t *testing.T) {
	d0 := time.Second
	seed := int64(1000)
	r := newRetryDelayStrategy(d0, 0, nil, newDefaultJitter(0.5, seed))
	t0 := time.Now().Add(-time.Minute)
	d1 := r.NextRetryDelay(t0)
	d2 := r.NextRetryDelay(t0.Add(time.Second))
	d3 := r.NextRetryDelay(t0.Add(time.Second * 2))
	assert.Equal(t, time.Duration(985036673), d1) // these are the randomized values we expect from that fixed seed value
	assert.Equal(t, time.Duration(925004285), d2)
	assert.Equal(t, time.Duration(847349921), d3)
}

func TestJitterWithBackoff(t *testing.T) {
	d0 := time.Second
	max := time.Minute
	seed := int64(1000)
	r := newRetryDelayStrategy(d0, 0, newDefaultBackoff(max), newDefaultJitter(0.5, seed))
	t0 := time.Now().Add(-time.Minute)
	d1 := r.NextRetryDelay(t0)
	d2 := r.NextRetryDelay(t0.Add(time.Second))
	d3 := r.NextRetryDelay(t0.Add(time.Second * 2))
	assert.Equal(t, time.Duration(985036673), d1) // these are the randomized values we expect from that fixed seed value
	assert.Equal(t, time.Duration(1425004285), d2)
	assert.Equal(t, time.Duration(3347349921), d3)
}

func TestBackoffResetInterval(t *testing.T) {
	d0 := time.Second * 10
	max := time.Minute
	resetInterval := time.Second * 45
	r := newRetryDelayStrategy(d0, resetInterval, newDefaultBackoff(max), nil)
	t0 := time.Now().Add(-time.Minute)
	r.SetGoodSince(t0)

	t1 := t0.Add(time.Second)
	d1 := r.NextRetryDelay(t1)
	assert.Equal(t, d0, d1)

	t2 := t1.Add(d1)
	r.SetGoodSince(t2)

	t3 := t2.Add(time.Second * 10)
	d2 := r.NextRetryDelay(t3)
	assert.Equal(t, d0*2, d2)

	t4 := t3.Add(d2)
	r.SetGoodSince(t4)

	t5 := t4.Add(resetInterval)
	d3 := r.NextRetryDelay(t5)
	assert.Equal(t, d0, d3)
}

func TestBackoffAndJitterWorkWithHighRetryCount(t *testing.T) {
	// This test verifies that we do not get numeric overflow errors due to using a very high exponential
	// backoff number in calculations before it has been pinned to the maximum value. The jitter algorithm
	// uses 63-bit values, so it will fail if there is a time.Duration value greater than about 292 years,
	// which is unlikely to be a desirable backoff interval.
	d0 := time.Second
	max := 365 * 200 * 24 * time.Hour // 200 years
	retryCount := 35                  // 2^35 seconds exceeds a 63-bit count of nanoseconds

	backoff := newDefaultBackoff(max)
	jitter := newDefaultJitter(0.5, 1)

	d1 := backoff.applyBackoff(d0, retryCount)
	_ = jitter.applyJitter(d1)
	// No assertion - the test just needs to not panic.
}
