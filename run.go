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
