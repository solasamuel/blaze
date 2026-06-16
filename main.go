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
	duration    time.Duration
	timeout     time.Duration
}

// boundFlags holds pointers to every parsed flag value. newFlagSet is the single
// source of truth for the flag list (used by parseArgs and the README doc test).
type boundFlags struct {
	url, method, body     *string
	concurrency, requests *int
	duration, timeout     *time.Duration
	headers               *headerFlag
}

func newFlagSet() (*flag.FlagSet, *boundFlags) {
	fs := flag.NewFlagSet("blaze", flag.ContinueOnError)
	b := &boundFlags{
		url:         fs.String("url", "", "target endpoint (required)"),
		concurrency: fs.Int("concurrency", 10, "number of concurrent workers"),
		requests:    fs.Int("requests", 100, "total number of requests to send"),
		duration:    fs.Duration("duration", 0, "run for a fixed duration instead of a request count"),
		method:      fs.String("method", "GET", "HTTP method"),
		body:        fs.String("body", "", "request body"),
		timeout:     fs.Duration("timeout", 10*time.Second, "per-request timeout"),
		headers:     &headerFlag{},
	}
	fs.Var(b.headers, "header", "request header 'Key: Value' (repeatable)")
	return fs, b
}

// parseArgs parses argv (without the program name) into a config. It returns an
// error rather than exiting so it can be unit-tested.
func parseArgs(args []string) (config, error) {
	fs, b := newFlagSet()

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if *b.url == "" {
		return config{}, errors.New("--url is required")
	}

	hdr := make(map[string]string, len(*b.headers))
	for _, h := range *b.headers {
		k, v, _ := strings.Cut(h, ":")
		hdr[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	bodyStr := *b.body
	spec := RequestSpec{
		URL:     *b.url,
		Method:  *b.method,
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
		concurrency: *b.concurrency,
		requests:    *b.requests,
		duration:    *b.duration,
		timeout:     *b.timeout,
	}, nil
}

func main() {
	cfg, err := parseArgs(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0) // -h/--help already printed usage; a help request is success.
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "blaze:", err)
		os.Exit(2)
	}

	if cfg.duration > 0 {
		fmt.Printf("Running for %v with %d concurrent workers...\n",
			cfg.duration, cfg.concurrency)
	} else {
		fmt.Printf("Running %d requests with %d concurrent workers...\n",
			cfg.requests, cfg.concurrency)
	}

	start := time.Now()
	s := run(runOpts{
		Req:         cfg.spec,
		Concurrency: cfg.concurrency,
		Requests:    cfg.requests,
		Duration:    cfg.duration,
		Timeout:     cfg.timeout,
	})
	elapsed := time.Since(start)

	fmt.Print(formatSummary(s, elapsed))
}
