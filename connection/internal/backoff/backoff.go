// Package backoff provides the retry schedule used when redialling a broker.
//
// The schedule is exponential with full jitter: each attempt waits a random
// duration between the minimum and the current exponential ceiling. The jitter
// matters because every pod of a service loses its broker connection at the same
// instant when the broker restarts, and an unjittered schedule would march them
// all back in lockstep.
package backoff

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// Default bounds for the redial schedule.
const (
	DefaultMin = 1 * time.Second
	DefaultMax = 30 * time.Second
)

// Backoff yields successive retry delays. The zero value is valid and uses the
// default bounds.
type Backoff struct {
	Min      time.Duration
	Max      time.Duration
	attempts int
}

// applyDefaults fills in bounds left at their zero value.
func (b *Backoff) applyDefaults() {

	if b.Min <= 0 {
		b.Min = DefaultMin
	}

	if b.Max <= 0 {
		b.Max = DefaultMax
	}

	if b.Max < b.Min {
		b.Max = b.Min
	}
}

// Next returns the delay to wait before the next attempt and advances the
// schedule. Delays grow as Min, 2*Min, 4*Min … capped at Max, each one jittered
// down to somewhere in [Min, ceiling].
func (b *Backoff) Next() time.Duration {

	b.applyDefaults()

	ceiling := float64(b.Min) * math.Pow(2, float64(b.attempts))
	if ceiling > float64(b.Max) || math.IsInf(ceiling, 0) {
		ceiling = float64(b.Max)
	} else {
		b.attempts++
	}

	if ceiling <= float64(b.Min) {
		return b.Min
	}

	return b.Min + time.Duration(rand.Int63n(int64(ceiling)-int64(b.Min)+1))
}

// Attempts reports how many times Next has grown the schedule. It stops
// advancing once the ceiling is reached.
func (b *Backoff) Attempts() int {

	return b.attempts
}

// Reset returns the schedule to its first delay, and is called after a
// successful reconnection.
func (b *Backoff) Reset() {

	b.attempts = 0
}

// Wait sleeps for the next delay, returning false if ctx is cancelled first so
// a caller can abandon a retry loop on shutdown.
func (b *Backoff) Wait(ctx context.Context) bool {

	timer := time.NewTimer(b.Next())
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
