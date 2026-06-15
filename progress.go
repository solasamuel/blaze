package main

import (
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"
)

const (
	barWidth     = 24
	barFilled    = '█'
	barEmpty     = '░'
	tickInterval = 100 * time.Millisecond
)

// progressBar renders a fixed-width bar for done/total. The filled cell count is
// floor(width*done/total); it clamps at full so done >= total never overflows
// the bar, and an empty/zero total renders an empty bar rather than dividing by
// zero.
func progressBar(done, total, width int) string {
	filled := 0
	if total > 0 {
		filled = width * done / total
		if filled > width {
			filled = width
		}
	} else if done > 0 {
		filled = width
	}
	return strings.Repeat(string(barFilled), filled) +
		strings.Repeat(string(barEmpty), width-filled)
}

// renderProgress writes a single in-place progress line to w using \r.
func renderProgress(w io.Writer, done, total int) {
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(done) / float64(total)
	}
	fmt.Fprintf(w, "\r  [%s] %d/%d   %.1f%%", progressBar(done, total, barWidth), done, total, pct)
}

// startProgress launches a goroutine that, every tickInterval, reads the shared
// counter and redraws the progress line. It returns a stop function that halts
// the ticker, paints a final 100%-complete frame, and returns — call it once
// before printing the summary so the two never interleave.
func startProgress(w io.Writer, completed *int64, total int) (stop func()) {
	ticker := time.NewTicker(tickInterval)
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		for {
			select {
			case <-ticker.C:
				renderProgress(w, int(atomic.LoadInt64(completed)), total)
			case <-done:
				return
			}
		}
	}()

	return func() {
		ticker.Stop()
		close(done)
		<-finished // ensure the goroutine has stopped writing
		renderProgress(w, int(atomic.LoadInt64(completed)), total)
		fmt.Fprintln(w) // newline so the summary starts on a fresh line
	}
}
