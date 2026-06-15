package main

import (
	"io"
	"time"
)

// Job is a single unit of work fed to a worker. ID is monotonic across a run.
type Job struct {
	ID int
}

// Result is what a worker emits for each request it performs.
type Result struct {
	Latency    time.Duration
	StatusCode int
	Err        error
}

// Summary is the aggregate of every Result collected during a run.
type Summary struct {
	Total      int
	Errors     int
	Latencies  []time.Duration // successful-request latencies, sorted by collect
	ErrorKinds map[string]int  // "timeout" -> 2, "refused" -> 1
}

// RequestSpec describes the HTTP request each worker issues.
//
// Body is a factory rather than an io.Reader: a reader is consumed once, but
// every request needs its own fresh body, so we call Body() per request.
type RequestSpec struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    func() io.Reader
}
