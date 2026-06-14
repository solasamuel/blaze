# blaze — Software Architecture

**Concurrent HTTP load tester with live latency percentiles**

| Field | Value |
| --- | --- |
| Document version | 1.0 |
| Last updated | 2026-06-14 |
| Author | Sola Samuel |
| Module path | `github.com/solasamuel/blaze` |
| Related docs | [product-backlog.json](./product-backlog.json) · [test-plan.json](./test-plan.json) |

---

## 1. Overview

blaze is a single-binary command-line tool that fires a configurable number of
concurrent HTTP requests at a target endpoint, measures per-request latency, and
reports percentiles (p50/p95/p99/max), throughput, and an error breakdown. During
a run it renders a live, in-place terminal progress display.

```
$ blaze --url https://api.example.com/products \
        --concurrency 50 --requests 1000 \
        --method POST --body '{"test":true}'

  Running 1000 requests with 50 concurrent workers...
  [████████████████████░░░░] 847/1000   84.7%

  Latency:   p50  42ms   p95  118ms   p99  203ms   max  412ms
  Throughput: 1,240 req/s
  Errors:     3 (0.3%)   [2× timeout, 1× connection refused]
```

The design goal is twofold: a genuinely useful tool, and a faithful demonstration
of the canonical Go concurrency model — **fan-out / fan-in over channels**, with
`sync.WaitGroup` coordination, `context`-based cancellation, and `sync/atomic`
counters. Every architectural decision below is traceable to a backlog feature
(`F-x.y`) and validated by a test case (`TC-x.y.z`).

---

## 2. Architectural Principles

1. **Concurrency over parallelism, expressed in channels.** Coordination happens
   by passing values through channels, not by sharing memory behind locks. The one
   exception — the progress counter — is deliberately lock-free via `sync/atomic`.
2. **Channel direction is part of the type.** Functions declare `<-chan` /
   `chan<-` so the compiler forbids sending on a read channel. (F-1.2)
3. **Ownership of `close` is explicit.** A channel is closed by exactly one
   goroutine — the one that owns its sending side. This drives the double-goroutine
   shutdown in §5.4.
4. **No goroutine outlives its run.** Every goroutine has a termination condition
   tied to a closed channel or a cancelled context; `run()` returns with the
   goroutine count back at baseline. (TC-1.3.c)
5. **Errors are values, classified late.** Workers attach the raw `error` to the
   `Result`; classification into kinds happens once, in the collector. (F-4.1)
6. **Testability is a first-class constraint.** Pure functions (`percentile`,
   `classifyError`, `progressBar`) carry the logic; I/O is injected
   (`*http.Client`, `httptest.Server`) so unit tests stay Fast and Repeatable.

---

## 3. The Concurrency Model — Fan-Out / Fan-In

```
┌─────────────┐     jobs chan      ┌──────────┐
│             │ ─────────────────► │ worker 1 │ ─┐
│ coordinator │ ─────────────────► │ worker 2 │ ─┤  results chan
│             │ ─────────────────► │ worker N │ ─┤ ───────────────► collector
└─────────────┘                    └──────────┘ ─┘

  fan-out: one channel feeds many workers
  fan-in:  many workers feed one results channel
```

- **Fan-out** — one `jobs` channel is read by `N` worker goroutines. Go's runtime
  hands each buffered job to whichever worker is idle; no manual load balancing.
- **Fan-in** — every worker writes to a single shared `results` channel, which the
  collector drains in one `range` loop.

This is the pattern that recurs in nearly every production Go service, which is
why it is the heart of both the tool and its interview signal.

---

## 4. Core Types

```go
type Job struct {
    ID int
}

type Result struct {
    Latency    time.Duration
    StatusCode int
    Err        error
}

type Summary struct {
    Total      int
    Errors     int
    Latencies  []time.Duration // collected for percentile calculation
    ErrorKinds map[string]int  // "timeout" → 2, "refused" → 1
}

type RequestSpec struct {
    URL     string
    Method  string
    Headers map[string]string
    Body    func() io.Reader // a factory: each request needs a fresh reader
}
```

> **Why `Body func() io.Reader`?** An `io.Reader` is consumed once. Because every
> request re-reads the body, the spec stores a *factory* that returns a fresh
> reader per call rather than a single drained reader. (F-1.1)

---

## 5. Runtime Components

### 5.1 CLI / Configuration — `F-1.1`

The `main` package parses flags with the standard `flag` package into a
`RequestSpec` plus run parameters (`concurrency`, `requests`, `duration`,
`timeout`). `--url` is required; everything else defaults
(`concurrency=10`, `requests=100`, `method=GET`, `timeout=10s`). `--header` is a
repeatable flag implemented via a custom `flag.Value` that appends to a slice.
Validated by **TC-1.1.a/b/c**.

### 5.2 Worker — `F-1.2`

```go
func worker(
    ctx context.Context,
    jobs <-chan Job,        // receive-only
    results chan<- Result,  // send-only
    client *http.Client,
    req RequestSpec,
    wg *sync.WaitGroup,
) {
    defer wg.Done()

    for range jobs { // loop until jobs is closed and drained
        start := time.Now()

        httpReq, _ := http.NewRequestWithContext(ctx, req.Method, req.URL, req.Body())
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
```

Key properties:
- **Directional channel types** (`<-chan` / `chan<-`) make misuse a compile error.
- **`defer wg.Done()`** guarantees the WaitGroup is decremented on every exit path.
- **`resp.Body.Close()`** on the success path is mandatory for keep-alive reuse
  (see §6, F-4.4). Tested by **TC-1.2.a/b/c**.

### 5.3 Coordinator — `F-1.3`

```go
func run(req RequestSpec, concurrency, total int, timeout time.Duration) Summary {
    jobs    := make(chan Job, concurrency)    // buffered to concurrency
    results := make(chan Result, concurrency)
    var wg sync.WaitGroup

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    client := &http.Client{
        Timeout: timeout,
        Transport: &http.Transport{
            MaxIdleConnsPerHost: concurrency, // reuse connections (F-4.4)
        },
    }

    // fan-out: start N workers
    for i := 0; i < concurrency; i++ {
        wg.Add(1)
        go worker(ctx, jobs, results, client, req, &wg)
    }

    // feed jobs in their own goroutine so we can collect concurrently
    go func() {
        for i := 0; i < total; i++ {
            jobs <- Job{ID: i}
        }
        close(jobs) // closing tells workers to stop after draining
    }()

    // close results once every worker has finished
    go func() {
        wg.Wait()
        close(results)
    }()

    // fan-in: range until results is closed
    return collect(results, total)
}
```

Validated by **TC-1.3.a/b/c** (counts match, n=1 edge, no goroutine leak under
`-race`).

### 5.4 The Double-Goroutine Shutdown Pattern

Two helper goroutines coordinate a clean shutdown:

1. **Feeder** owns the *send* side of `jobs`. After sending all jobs it calls
   `close(jobs)`. A closed-but-drained channel makes each worker's `for range jobs`
   loop end naturally.
2. **Closer** waits on `wg.Wait()` — every worker has returned — then calls
   `close(results)`. That lets the collector's `range results` terminate.

```
feeder ─┐ close(jobs) ─► workers drain & return ─► wg hits 0
        │                                              │
        └──────────────────────────────────────────►  closer: wg.Wait(); close(results)
                                                              │
                                              collector: range results ends ─► run() returns
```

This is the idiomatic Go pipeline shutdown. Closing is owned by exactly one
goroutine per channel (Principle §2.3), which is what makes it race-free.

### 5.5 Collector & Statistics — `F-1.4`, `F-2.1`, `F-2.2`

```go
func collect(results <-chan Result, total int) Summary {
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

func percentile(sorted []time.Duration, p float64) time.Duration {
    if len(sorted) == 0 {
        return 0
    }
    idx := int(float64(len(sorted)-1) * p)
    return sorted[idx]
}
```

`collect` runs on the main goroutine — it is the single reader, so the `Summary`
needs no locking. Percentiles use the **nearest-rank on `len-1`** index, which is
exactly the off-by-one the test plan targets (**TC-2.1.c**). Throughput is
`successfulRequests / elapsedWallClock` (**TC-2.2.a**).

### 5.6 Live Progress Display — `F-3.1`, `F-3.2`

A single `int64` counter is incremented lock-free by every worker:

```go
var completed int64 // shared atomic counter

// in the worker, after each request:
atomic.AddInt64(&completed, 1)

// in a dedicated progress goroutine:
ticker := time.NewTicker(100 * time.Millisecond)
for range ticker.C {
    done := atomic.LoadInt64(&completed)
    fmt.Printf("\r[%s] %d/%d", progressBar(done, total), done, total)
}
```

- `sync/atomic` is the right tool: a single integer hammered by 50 goroutines does
  not warrant a mutex. (TC-3.1.a/b)
- The `\r` carriage return overwrites the current line in place — no TUI dependency
  for v1. `progressBar` is a pure function (clamped at 100%), unit-tested without a
  terminal (TC-3.2.a/b).
- The progress goroutine is stopped (ticker stopped / channel closed) once the run
  completes, before the final summary prints, so the two never interleave.

### 5.7 Error Classification — `F-4.1`

```go
func classifyError(err error) string {
    if errors.Is(err, context.DeadlineExceeded) {
        return "timeout"
    }
    var netErr net.Error
    if errors.As(err, &netErr) && netErr.Timeout() {
        return "timeout"
    }
    if strings.Contains(err.Error(), "connection refused") {
        return "refused"
    }
    return "other"
}
```

Modern Go error handling: `errors.Is` for sentinels, `errors.As` for typed
unwrapping. The function is pure and exhaustively table-tested
(**TC-4.1.a–d**).

### 5.8 Run Modes: Count vs Duration — `F-4.2`, `F-4.3`

- **Count mode (default).** The feeder loops `total` times, then closes `jobs`.
- **Duration mode (`--duration`).** The feeder loops *indefinitely* against a
  `context.WithTimeout`; when the deadline fires it stops feeding and closes
  `jobs`. The rest of the pipeline is unchanged — the only difference is the
  feeder's loop condition. (TC-4.3.a/b)

Per-request timeout is enforced both by `http.Client.Timeout` and the
request-scoped `context`, so a slow endpoint yields a `"timeout"`-classified error
(TC-4.2.a).

---

## 6. Cross-Cutting Concerns

| Concern | Decision | Backlog | Tests |
| --- | --- | --- | --- |
| Connection reuse | `Transport.MaxIdleConnsPerHost = concurrency`; bodies always closed | F-4.4 | TC-4.4.a |
| Cancellation | One `context.WithCancel`/`WithTimeout`, `defer cancel()`, passed to every request | F-4.2 | TC-4.2.b |
| Data races | Channels for all shared data; the lone counter uses `sync/atomic`; `go test -race` in CI | F-1.3, F-3.1 | TC-1.3.c, TC-3.1.a |
| Back-pressure | `jobs`/`results` buffered to `concurrency`, bounding in-flight work | F-1.3 | TC-1.3.a |
| Determinism in tests | I/O injected via `*http.Client` + `httptest.Server`; pure stats functions | — | FIRST: Fast/Repeatable |

---

## 7. Concurrency Safety Argument

- **Why no mutex on `Summary`?** It is written by a single goroutine (the
  collector). Single-owner data needs no lock.
- **Why `atomic` on `completed`?** It is written by many workers and read by the
  progress goroutine concurrently — the textbook case for an atomic integer over a
  mutex-guarded `int`.
- **Why can't `run()` leak goroutines?** Workers exit when `jobs` is drained after
  `close`; the closer exits after `wg.Wait()` and `close(results)`; the feeder
  exits after the loop; the progress goroutine exits when stopped. Each has exactly
  one termination edge tied to a close or a context. Verified empirically by
  TC-1.3.c under `-race`.
- **Why is `close` race-free?** Each channel is closed by its single sending owner
  (`jobs` by the feeder, `results` by the closer). No goroutine sends after close.

---

## 8. Project Layout

```
blaze/
├── docs/
│   ├── product-backlog.json     # epics → features
│   ├── test-plan.json           # BOC + FIRST test cases, linked to features
│   └── software-architecture.md # this document
├── main.go                      # flag parsing, RequestSpec, wiring
├── run.go                       # coordinator, worker, collect (the pipeline)
├── stats.go                     # percentile, throughput, summary formatting
├── progress.go                  # atomic counter, progressBar, \r renderer
├── errors.go                    # classifyError
├── *_test.go                    # table-driven + httptest-backed tests
├── go.mod
└── README.md
```

> Layout is indicative; for a two-evening project everything may begin in a single
> `main` package and split out as files grow. The boundaries above match the
> testable units in the test plan.

---

## 9. Extension Points (Out of Scope for v1.0)

- **Rich TUI** via `github.com/charmbracelet/bubbletea` — swap the §5.6 renderer.
- **Streaming percentiles** (t-digest / HDR histogram) to bound memory on very
  large runs instead of collecting every latency.
- **Multiple targets / weighted scenarios**, request templating, response
  assertions.
- **Output formats** (`--json`, `--csv`) for CI integration.

These are deliberately excluded to keep v1.0 to two evenings and a clean
demonstration of the core concurrency model.

---

## 10. Traceability

Every component above maps to a backlog feature and at least one test case:

| Component (§) | Feature | Representative tests |
| --- | --- | --- |
| 5.1 CLI / config | F-1.1 | TC-1.1.a/b/c |
| 5.2 Worker | F-1.2 | TC-1.2.a/b/c |
| 5.3 Coordinator | F-1.3 | TC-1.3.a/b/c |
| 5.5 Collector | F-1.4 | TC-1.4.a/b |
| 5.5 Percentiles / throughput | F-2.1, F-2.2 | TC-2.1.a–d, TC-2.2.a/b |
| 5.6 Live display | F-3.1, F-3.2 | TC-3.1.a/b, TC-3.2.a/b |
| 5.7 Error classification | F-4.1 | TC-4.1.a–d |
| 5.8 Run modes / timeout | F-4.2, F-4.3 | TC-4.2.a/b, TC-4.3.a/b |
| 6 Connection pooling | F-4.4 | TC-4.4.a |
| Distribution / docs | F-5.1, F-5.2 | TC-5.1.a, TC-5.2.a |
