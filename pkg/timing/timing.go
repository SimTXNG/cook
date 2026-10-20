// Package timing provides optional phase timing output for build commands.
package timing

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var enabled atomic.Bool

var outputMu sync.Mutex

// SetEnabled toggles timing output.
func SetEnabled(on bool) {
	enabled.Store(on)
}

// Enabled reports whether timing output is active.
func Enabled() bool {
	return enabled.Load()
}

// Start records the start of a phase and returns a function that prints its
// duration when called.
func Start(label string) func() {
	start := time.Now()
	return func() {
		if !enabled.Load() {
			return
		}
		outputMu.Lock()
		defer outputMu.Unlock()
		fmt.Fprintf(os.Stdout, "==> [timing] %-28s %s\n", label, time.Since(start).Round(time.Millisecond))
	}
}

// Since returns a rounded duration for callers that manage their own reporting.
func Since(start time.Time) time.Duration {
	return time.Since(start).Round(time.Millisecond)
}
