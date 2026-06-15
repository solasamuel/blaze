package main

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// worker consumes jobs, issues one HTTP request per job, and emits a Result.
//
// Channel directions are constrained at the type level: jobs is receive-only
// and results is send-only, so the compiler rejects sending on the wrong one.
func worker(
	ctx context.Context,
	jobs <-chan Job,
	results chan<- Result,
	client *http.Client,
	req RequestSpec,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	for range jobs { // ends when jobs is closed and drained
		start := time.Now()

		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, req.Body())
		if err != nil {
			results <- Result{Latency: time.Since(start), Err: err}
			continue
		}
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}

		resp, err := client.Do(httpReq)
		latency := time.Since(start)

		if err != nil {
			results <- Result{Latency: latency, Err: err}
			continue
		}
		resp.Body.Close() // CRITICAL: close to return the connection to the pool
		results <- Result{Latency: latency, StatusCode: resp.StatusCode}
	}
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

	// fan-out: start N workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go worker(ctx, jobs, results, client, req, &wg)
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

	// fan-in: collect on the main goroutine until results is closed.
	return collect(results)
}
