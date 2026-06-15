package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// headerFlag implements flag.Value so --header can be repeated, each value of
// the form "Key: Value".
type headerFlag []string

func (h *headerFlag) String() string { return strings.Join(*h, ", ") }

func (h *headerFlag) Set(v string) error {
	if !strings.Contains(v, ":") {
		return fmt.Errorf("header %q must be in 'Key: Value' form", v)
	}
	*h = append(*h, v)
	return nil
}

// config holds the parsed run parameters plus the RequestSpec.
type config struct {
	spec        RequestSpec
	concurrency int
	requests    int
	timeout     time.Duration
}

// parseArgs parses argv (without the program name) into a config. It returns an
// error rather than exiting so it can be unit-tested.
func parseArgs(args []string) (config, error) {
	fs := flag.NewFlagSet("blaze", flag.ContinueOnError)

	url := fs.String("url", "", "target endpoint (required)")
	concurrency := fs.Int("concurrency", 10, "number of concurrent workers")
	requests := fs.Int("requests", 100, "total number of requests to send")
	method := fs.String("method", "GET", "HTTP method")
	body := fs.String("body", "", "request body")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	var headers headerFlag
	fs.Var(&headers, "header", "request header 'Key: Value' (repeatable)")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if *url == "" {
		return config{}, errors.New("--url is required")
	}

	hdr := make(map[string]string, len(headers))
	for _, h := range headers {
		k, v, _ := strings.Cut(h, ":")
		hdr[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	bodyStr := *body
	spec := RequestSpec{
		URL:     *url,
		Method:  *method,
		Headers: hdr,
		Body: func() io.Reader {
			if bodyStr == "" {
				return nil
			}
			return bytes.NewReader([]byte(bodyStr))
		},
	}

	return config{
		spec:        spec,
		concurrency: *concurrency,
		requests:    *requests,
		timeout:     *timeout,
	}, nil
}

func main() {
	cfg, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "blaze:", err)
		os.Exit(2)
	}

	fmt.Printf("Running %d requests with %d concurrent workers...\n",
		cfg.requests, cfg.concurrency)

	start := time.Now()
	s := run(cfg.spec, cfg.concurrency, cfg.requests, cfg.timeout)
	elapsed := time.Since(start)

	printSummary(s, elapsed)
}

// printSummary prints the basic end-of-run report. Epic 2/3 enrich this with
// throughput formatting and a live display.
func printSummary(s Summary, elapsed time.Duration) {
	p := computePercentiles(s.Latencies)
	fmt.Printf("\nLatency:   p50  %v   p95  %v   p99  %v   max  %v\n",
		p.P50, p.P95, p.P99, p.Max,
	)
	if elapsed > 0 {
		fmt.Printf("Throughput: %.0f req/s\n",
			float64(len(s.Latencies))/elapsed.Seconds())
	}
	fmt.Printf("Errors:     %d (%.1f%%)\n",
		s.Errors, 100*float64(s.Errors)/float64(max(s.Total, 1)))
}
