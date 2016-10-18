package eventsource

import (
	"fmt"
	"testing"
	"time"
)

// This particular "benchmark" exists to spit out various jitter values. It's structured
// as a benchmark so we can see the output, since tests suppress output
func BenchmarkJitterValues(b *testing.B) {
	str := Stream{
		retry:               1 * time.Second,
		maxReconnectionTime: 30 * time.Second,
	}

	for i := 0; i < 300; i++ {
		fmt.Printf("Jittered backoff: %v\n", str.backoffWithJitter(i))
	}
}
