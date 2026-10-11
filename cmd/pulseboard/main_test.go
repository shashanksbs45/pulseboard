package main

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles the pulseboard command into a temp dir.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pulseboard")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestUnknownCommand(t *testing.T) {
	bin := buildBinary(t)
	var stderr bytes.Buffer
	cmd := exec.Command(bin, "frobnicate")
	cmd.Stderr = &stderr
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("expected non-zero exit, got %v", err)
	}
	if !strings.Contains(stderr.String(), "usage: pulseboard") {
		t.Errorf("stderr does not contain usage:\n%s", stderr.String())
	}
}
