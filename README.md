# blaze

**Concurrent HTTP load tester with live latency percentiles.**

blaze fires a configurable number of concurrent requests at a target endpoint,
measures the latency of each, and reports percentiles (p50/p95/p99/max),
throughput, and an error breakdown. While running it shows a live, in-place
terminal progress display.

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

## Install

```sh
go install github.com/solasamuel/blaze@latest
```

This places a `blaze` binary in `$(go env GOPATH)/bin` (ensure it is on your
`PATH`). Requires Go 1.21+.

Or build from source:

```sh
git clone https://github.com/solasamuel/blaze
cd blaze
go build -o blaze .
```

## Usage

Fixed request count:

```sh
blaze --url https://example.com --concurrency 50 --requests 1000
```

Fixed duration instead of a count:

```sh
blaze --url https://example.com --concurrency 50 --duration 30s
```

POST with a body and custom headers:

```sh
blaze --url https://api.example.com/orders \
      --method POST \
      --body '{"sku":"ABC","qty":2}' \
      --header 'Content-Type: application/json' \
      --header 'Authorization: Bearer TOKEN' \
      --concurrency 20 --requests 500
```

## Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--url` | _(required)_ | Target endpoint. blaze exits non-zero if omitted. |
| `--concurrency` | `10` | Number of concurrent workers. |
| `--requests` | `100` | Total number of requests to send (count mode). |
| `--duration` | `0` | Run for a fixed duration (e.g. `30s`) instead of a request count. When set, takes precedence over `--requests`. |
| `--method` | `GET` | HTTP method. |
| `--body` | _(empty)_ | Request body sent with every request. |
| `--header` | _(none)_ | Request header in `Key: Value` form. Repeatable — pass `--header` multiple times. |
| `--timeout` | `10s` | Per-request timeout. Requests exceeding it are recorded as `timeout` errors. |

The final summary reports latency percentiles (p50/p95/p99/max), throughput
(`req/s`), and an error breakdown grouped by kind (`timeout`,
`connection refused`, `other`).

## How it works — the fan-out / fan-in concurrency model

blaze is built on the canonical Go worker-pool pattern. A coordinator feeds jobs
into a channel; `N` worker goroutines read from it concurrently (fan-out); each
worker sends its result into a shared results channel; a single collector drains
that channel and aggregates the summary (fan-in).

```
┌─────────────┐     jobs chan      ┌──────────┐
│             │ ─────────────────► │ worker 1 │ ─┐
│ coordinator │ ─────────────────► │ worker 2 │ ─┤  results chan
│             │ ─────────────────► │ worker N │ ─┤ ───────────────► collector
└─────────────┘                    └──────────┘ ─┘

  fan-out: one channel feeds many workers
  fan-in:  many workers feed one results channel
```

Key pieces:

- **Workers** read a receive-only `<-chan Job` and write a send-only
  `chan<- Result`; channel direction is enforced at the type level.
- **Shutdown** uses the idiomatic double-goroutine pattern: a feeder closes the
  jobs channel when it is done feeding; a second goroutine calls `wg.Wait()` then
  closes the results channel, which lets the collector's `range` loop end
  naturally.
- **Per-request timeout** comes from `context.WithTimeout` on each request, so a
  slow endpoint is cancelled and classified as a `timeout`.
- **Progress** is driven by a single `sync/atomic` counter incremented by every
  worker and read by a 100 ms ticker — no mutex for a single integer.
- **Connection reuse** is enabled via `Transport.MaxIdleConnsPerHost =
  concurrency`; workers always close response bodies so connections return to the
  pool.

For a deeper write-up see [docs/software-architecture.md](docs/software-architecture.md).

## Compared to hey / vegeta / k6

| Tool | Niche | Notes |
| --- | --- | --- |
| **blaze** | Minimal, dependency-free load tester | Single static binary, live percentiles, count or duration mode. Intentionally small. |
| [hey](https://github.com/rakyll/hey) | Quick ad-hoc benchmarking | Similar scope; mature and widely used. |
| [vegeta](https://github.com/tsenart/vegeta) | Constant-rate attacks & reporting | Rate-based load (req/s targets), rich reports, usable as a library. |
| [k6](https://k6.io) | Scriptable load testing | JavaScript scenarios, thresholds, cloud/CI integrations — a full platform. |

Reach for blaze when you want a tiny, fast, no-config tool to get latency
percentiles for an endpoint. Reach for vegeta/k6 when you need rate control,
scripted scenarios, or long-term reporting.

## Development

```sh
go test ./...          # run the suite
go test -race ./...    # run with the race detector (the concurrency tests rely on it)
go vet ./...
```

## Project docs

- [docs/product-backlog.json](docs/product-backlog.json) — epics → features
- [docs/test-plan.json](docs/test-plan.json) — BOC + FIRST test cases
- [docs/software-architecture.md](docs/software-architecture.md) — architecture & traceability

## License

See [LICENSE](LICENSE).
