package main

import (
	"errors"
	"flag"
	"testing"
	"time"
)

// TC-1.1.a — defaults applied when optional flags omitted.
func TestParseArgs_Defaults(t *testing.T) {
	cfg, err := parseArgs([]string{"--url", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.concurrency != 10 {
		t.Errorf("concurrency = %d, want 10", cfg.concurrency)
	}
	if cfg.requests != 100 {
		t.Errorf("requests = %d, want 100", cfg.requests)
	}
	if cfg.spec.Method != "GET" {
		t.Errorf("method = %q, want GET", cfg.spec.Method)
	}
	if cfg.timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", cfg.timeout)
	}
}

// TC-1.1.b — missing --url is rejected.
func TestParseArgs_MissingURL(t *testing.T) {
	if _, err := parseArgs([]string{"--concurrency", "5"}); err == nil {
		t.Fatal("expected error for missing --url, got nil")
	}
}

// TC-1.1.c — repeatable --header flags accumulate (N, not N-1 or N+1).
func TestParseArgs_RepeatableHeaders(t *testing.T) {
	cfg, err := parseArgs([]string{
		"--url", "http://example.com",
		"--header", "Authorization: Bearer t",
		"--header", "X-Test: 1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(cfg.spec.Headers); got != 2 {
		t.Fatalf("len(Headers) = %d, want 2", got)
	}
	if cfg.spec.Headers["Authorization"] != "Bearer t" {
		t.Errorf("Authorization = %q", cfg.spec.Headers["Authorization"])
	}
	if cfg.spec.Headers["X-Test"] != "1" {
		t.Errorf("X-Test = %q", cfg.spec.Headers["X-Test"])
	}
}

// --duration flag parses into config.
func TestParseArgs_Duration(t *testing.T) {
	cfg, err := parseArgs([]string{"--url", "http://example.com", "--duration", "5s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.duration != 5*time.Second {
		t.Errorf("duration = %v, want 5s", cfg.duration)
	}
}

// TC-5.2.a — `blaze --help` is a success: parseArgs surfaces flag.ErrHelp so
// main can exit 0 after the usage text is printed.
func TestParseArgs_HelpReturnsErrHelp(t *testing.T) {
	_, err := parseArgs([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseArgs(--help) err = %v, want flag.ErrHelp", err)
	}
}
