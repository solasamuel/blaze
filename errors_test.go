package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// fakeNetErr is a net.Error whose Timeout() is configurable.
type fakeNetErr struct{ timeout bool }

func (e fakeNetErr) Error() string   { return "fake net error" }
func (e fakeNetErr) Timeout() bool   { return e.timeout }
func (e fakeNetErr) Temporary() bool { return false }

// TC-4.1.a/b/c/d — classifyError maps errors to kinds.
func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"deadline exceeded -> timeout", context.DeadlineExceeded, errTimeout},                      // TC-4.1.a
		{"wrapped deadline -> timeout", fmt.Errorf("do: %w", context.DeadlineExceeded), errTimeout}, // TC-4.1.a
		{"net.Error timeout -> timeout", fakeNetErr{timeout: true}, errTimeout},                     // TC-4.1.b
		{"connection refused -> refused", errors.New("dial tcp: connection refused"), errRefused},   // TC-4.1.c
		{"unknown -> other", errors.New("something else"), errOther},                                // TC-4.1.d
		{"non-timeout net.Error -> other", fakeNetErr{timeout: false}, errOther},                    // boundary
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyError(tc.err); got != tc.want {
				t.Errorf("classifyError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// nil error yields the empty kind (never counted).
func TestClassifyError_Nil(t *testing.T) {
	if got := classifyError(nil); got != "" {
		t.Errorf("classifyError(nil) = %q, want \"\"", got)
	}
}
