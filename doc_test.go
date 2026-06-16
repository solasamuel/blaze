package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// TC-5.1.a — the README documents every CLI flag and includes the fan-out/fan-in
// diagram. This guards against flag drift: add a flag, document it.
func TestREADME_DocumentsEveryFlag(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	readme := string(data)

	// Derive the flag list from the real FlagSet so adding a flag without
	// documenting it fails the build rather than silently drifting.
	fs, _ := newFlagSet()
	fs.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(readme, "--"+f.Name) {
			t.Errorf("README is missing flag --%s", f.Name)
		}
	})

	if !strings.Contains(readme, "fan-out: one channel feeds many workers") {
		t.Error("README is missing the fan-out/fan-in diagram")
	}
	if !strings.Contains(readme, "go install github.com/solasamuel/blaze@latest") {
		t.Error("README is missing the go install instruction (F-5.2)")
	}
}
