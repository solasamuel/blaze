package main

import (
	"context"
	"errors"
	"net"
	"strings"
)

// Error kinds reported in the summary breakdown.
const (
	errTimeout = "timeout"
	errRefused = "refused"
	errOther   = "other"
)

// classifyError maps a request error to a coarse kind for the error breakdown.
//
// It uses Go 1.13+ error inspection: errors.Is matches the context deadline
// sentinel, errors.As unwraps to a net.Error to check its Timeout() flag, and a
// final string check catches connection-refused. Anything unrecognised is
// "other".
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errTimeout
	}
	if strings.Contains(err.Error(), "connection refused") {
		return errRefused
	}
	return errOther
}
