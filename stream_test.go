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

func TestIntPow(t *testing.T) {
	if pow(2, 4) != 16 {
		t.Errorf("2^4 == 16, got %d", pow(2, 4))
	}

	if pow(2, 12) != 4096 {
		t.Errorf("2^12 == 4096, got %d", pow(2, 12))
	}

	if pow(2, 31) != 2147483648 {
		t.Errorf("2^31 == 2147483648, got %d", pow(2, 31))
	}

	if pow(2, 34) != 17179869184 {
		t.Errorf("2^34 == 17179869184, got %d", pow(2, 34))
	}

	if pow(2, 300) != 0 {
		t.Errorf("2^300 overflows, expected 0, got %d", pow(2, 300))
	}

}
