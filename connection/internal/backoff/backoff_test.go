package backoff

import (
	"context"
	"testing"
	"time"
)

func TestNextStaysWithinBounds(t *testing.T) {

	b := Backoff{Min: time.Second, Max: 30 * time.Second}

	for i := 0; i < 50; i++ {

		delay := b.Next()

		if delay < b.Min {
			t.Fatalf("attempt %d: delay %s below Min %s", i, delay, b.Min)
		}

		if delay > b.Max {
			t.Fatalf("attempt %d: delay %s above Max %s", i, delay, b.Max)
		}
	}
}

func TestZeroValueUsesDefaults(t *testing.T) {

	var b Backoff

	delay := b.Next()

	if delay < DefaultMin || delay > DefaultMax {
		t.Fatalf("delay %s outside default bounds [%s, %s]", delay, DefaultMin, DefaultMax)
	}
}

func TestAttemptsStopGrowingAtCeiling(t *testing.T) {

	b := Backoff{Min: time.Second, Max: 4 * time.Second}

	for i := 0; i < 20; i++ {
		b.Next()
	}

	// Min=1s doubling to a 4s ceiling is reached on the third attempt, after
	// which the schedule stops counting up rather than overflowing.
	if b.Attempts() > 3 {
		t.Fatalf("Attempts() = %d, want it capped at 3", b.Attempts())
	}
}

func TestResetReturnsToFirstDelay(t *testing.T) {

	b := Backoff{Min: time.Second, Max: 30 * time.Second}

	for i := 0; i < 10; i++ {
		b.Next()
	}

	b.Reset()

	if b.Attempts() != 0 {
		t.Fatalf("Attempts() = %d after Reset, want 0", b.Attempts())
	}
}

func TestMaxBelowMinIsCorrected(t *testing.T) {

	b := Backoff{Min: 10 * time.Second, Max: time.Second}

	if delay := b.Next(); delay != 10*time.Second {
		t.Fatalf("Next() = %s, want Min when Max < Min", delay)
	}
}

func TestWaitReturnsFalseOnCancelledContext(t *testing.T) {

	b := Backoff{Min: 5 * time.Second, Max: 10 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()

	if b.Wait(ctx) {
		t.Fatal("Wait() = true on a cancelled context, want false")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Wait() took %s, want it to return immediately", elapsed)
	}
}

func TestWaitReturnsTrueWhenDelayElapses(t *testing.T) {

	b := Backoff{Min: time.Millisecond, Max: 2 * time.Millisecond}

	if !b.Wait(context.Background()) {
		t.Fatal("Wait() = false, want true")
	}
}
