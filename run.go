package main

import (
	"context"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// workerDeps bundles everything a worker needs, keeping the goroutine signature
// small and the shared state explicit. Channel directions are constrained at the
// type level: jobs is receive-only and results is send-only, so the compiler
// rejects sending on the wrong one.
type workerDeps struct {
	ctx       context.Context
	jobs      <-chan Job    // receive-only
	results   chan<- Result // send-only
	client    *http.Client
	req       RequestSpec
	timeout   time.Duration
	completed *int64 // lock-free counter, incremented once per finished request
	wg        *sync.WaitGroup
}

// worker consumes jobs and issues one HTTP request per job until jobs is closed.
func worker(d workerDeps) {
	defer d.wg.Done()

	for range d.jobs { // ends when jobs is closed and drained
		d.doRequest()
	}
}

// doRequest performs a single request and emits its Result. It is a method so
// the per-request context cancel can be deferred and released on every path.
func (d workerDeps) doRequest() {
	// Per-request deadline derived from the run context: cancelling the run (or
	// hitting --timeout) aborts the in-flight request. errors.Is then sees
	// context.DeadlineExceeded for a precise "timeout" classification.
	reqCtx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()
	defer atomic.AddInt64(d.completed, 1)

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(reqCtx, d.req.Method, d.req.URL, d.req.Body())
	if err != nil {
		d.results <- Result{Latency: time.Since(start), Err: err}
		return
	}
	for k, v := range d.req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := d.client.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		d.results <- Result{Latency: latency, Err: err}
		return
	}
	resp.Body.Close() // CRITICAL: close to return the connection to the pool
	d.results <- Result{Latency: latency, StatusCode: resp.StatusCode}
}

// run executes a fixed-count load test using the fan-out/fan-in pattern and
// returns the aggregated Summary.
func run(req RequestSpec, concurrency, total int, timeout time.Duration) Summary {
	jobs := make(chan Job, concurrency)
	results := make(chan Result, concurrency)
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transport := &http.Transport{
		MaxIdleConnsPerHost: concurrency, // reuse keep-alive connections
	}
	// Release pooled idle connections (and their background goroutines) when the
	// run ends, so run() owns no resources after it returns.
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: timeout, Transport: transport}

	var completed int64 // lock-free, shared by all workers and the progress loop

	// fan-out: start N workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go worker(workerDeps{
			ctx:       ctx,
			jobs:      jobs,
			results:   results,
			client:    client,
			req:       req,
			timeout:   timeout,
			completed: &completed,
			wg:        &wg,
		})
	}

	// feeder owns the send side of jobs: feed all, then close.
	go func() {
		for i := 0; i < total; i++ {
			jobs <- Job{ID: i}
		}
		close(jobs)
	}()

	// closer owns the send side of results: once every worker is done, close.
	go func() {
		wg.Wait()
		close(results)
	}()

	// live progress: redraw from the atomic counter until the run completes.
	stopProgress := startProgress(os.Stdout, &completed, total)

	// fan-in: collect on the main goroutine until results is closed.
	s := collect(results)
	stopProgress() // halt and paint the final frame before the summary prints
	return s
}

// collect drains the results channel into a Summary. It is the single reader,
// so the Summary needs no locking.
func collect(results <-chan Result) Summary {
	s := Summary{ErrorKinds: make(map[string]int)}
	for r := range results {
		s.Total++
		if r.Err != nil {
			s.Errors++
			s.ErrorKinds[classifyError(r.Err)]++
			continue
		}
		s.Latencies = append(s.Latencies, r.Latency)
	}
	sort.Slice(s.Latencies, func(i, j int) bool {
		return s.Latencies[i] < s.Latencies[j]
	})
	return s
}
