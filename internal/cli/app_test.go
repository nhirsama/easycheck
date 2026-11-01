package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func run(args []string, input io.Reader, output, diagnostics io.Writer) error {
	return Run(context.Background(), args, input, output, diagnostics)
}

func TestExecuteExitCodesAndStreams(t *testing.T) {
	t.Setenv("EASYCHECK_BASE_URL", "https://example.invalid/qdgj/")
	t.Setenv("EASYCHECK_SESSION_FILE", filepath.Join(t.TempDir(), "missing-session.json"))
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "empty shows help"},
		{name: "root help", args: []string{"--help"}},
		{name: "command help", args: []string{"sign", "--help"}},
		{name: "unknown command", args: []string{"unknown"}, code: 2},
		{name: "unknown help target", args: []string{"help", "unknown"}, code: 2},
		{name: "unknown option", args: []string{"list", "--unknown-option"}, code: 2},
		{name: "invalid option value", args: []string{"list", "--page", "text"}, code: 2},
		{name: "invalid pagination", args: []string{"list", "--page", "0"}, code: 2},
		{name: "missing batch", args: []string{"detail"}, code: 2},
		{name: "missing session", args: []string{"list"}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			code := Execute(context.Background(), tc.args, strings.NewReader(""), &output, &diagnostics)
			if code != tc.code {
				t.Fatalf("exit code: got %d, want %d", code, tc.code)
			}
			if code == 0 {
				if output.Len() == 0 || diagnostics.Len() != 0 {
					t.Fatal("help must use stdout")
				}
			} else {
				if output.Len() != 0 || strings.Count(diagnostics.String(), "错误：") != 1 {
					t.Fatal("failure must use stderr and print the error once")
				}
				if code == 2 && !strings.Contains(diagnostics.String(), "--help") {
					t.Fatal("usage error is missing a help hint")
				}
			}
		})
	}
}

func TestCommandHelpFormsAgree(t *testing.T) {
	for _, spec := range commands {
		t.Run(spec.name, func(t *testing.T) {
			var expected string
			for _, args := range [][]string{{"help", spec.name}, {spec.name, "--help"}, {spec.name, "-h"}, {spec.name, "-help"}} {
				var output, diagnostics bytes.Buffer
				if err := run(args, strings.NewReader(""), &output, &diagnostics); err != nil {
					t.Fatal(err)
				}
				if diagnostics.Len() != 0 {
					t.Fatal("help wrote to stderr")
				}
				if expected == "" {
					expected = output.String()
				} else if output.String() != expected {
					t.Fatalf("different help for %v", args)
				}
			}
		})
	}
}

func TestCanceledContextStopsSubmission(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err := Run(ctx, []string{"sign", "--base-url", base, "--session", session, "--pcid", "batch"}, strings.NewReader(""), &output, io.Discard)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 || output.Len() != 0 {
		t.Fatalf("canceled command: err=%v requests=%d output=%q", err, calls.Load(), output.String())
	}
}
