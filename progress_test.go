package main

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TC-3.1.a — the counter incremented by all workers reaches total under -race.
func TestWorker_CounterReachesTotal(t *testing.T) {
	srv := okServer()
	defer srv.Close()

	const total = 200
	jobs := make(chan Job, total)
	results := make(chan Result, total)
	var completed int64
	var wg sync.WaitGroup

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go worker(context.Background(), jobs, results, client, specFor(srv.URL), &completed, &wg)
	}
	for i := 0; i < total; i++ {
		jobs <- Job{ID: i}
	}
	close(jobs)

	// drain results concurrently so the buffered sends never block.
	drained := make(chan int, 1)
	go func() {
		n := 0
		for range results {
			n++
		}
		drained <- n
	}()

	wg.Wait()
	close(results)
	<-drained

	if got := atomic.LoadInt64(&completed); got != total {
		t.Fatalf("completed = %d, want %d", got, total)
	}
}

// TC-3.1.b — many goroutines each adding 1 a fixed number of times lose no
// updates (lock-free correctness).
func TestAtomicCounter_NoLostUpdates(t *testing.T) {
	const goroutines, iterations = 50, 1000
	var counter int64
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				atomic.AddInt64(&counter, 1)
			}
		}()
	}
	wg.Wait()

	if want := int64(goroutines * iterations); counter != want {
		t.Fatalf("counter = %d, want %d", counter, want)
	}
}
