package logging_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/output"
)

// install puts a Printer with a fixed color decision in place for one test.
func install(t *testing.T, mode output.ColorMode) (out, errBuf *bytes.Buffer) {
	t.Helper()
	out, errBuf = &bytes.Buffer{}, &bytes.Buffer{}
	d := output.Decider{Mode: mode, Env: func(string) string { return "" }}
	prev := logging.SetPrinter(output.New(out, errBuf, d))
	t.Cleanup(func() { logging.SetPrinter(prev) })
	return out, errBuf
}

func TestLogFormat(t *testing.T) {
	out, errBuf := install(t, output.ColorNever)

	logging.Info("Cluster created on %s", "127.0.0.1")
	logging.Success("Bucket %q created", "b")
	logging.Warning("Collection not empty")
	logging.Error("Failed to create cluster: %s", "boom")
	logging.Println("true")

	want := "[info] - Cluster created on 127.0.0.1\n[ok] - Bucket \"b\" created\ntrue\n"
	if got := out.String(); got != want {
		t.Fatalf("stdout=%q, want %q", got, want)
	}
	wantErr := "[warning] - Collection not empty\n[error] - Failed to create cluster: boom\n"
	if got := errBuf.String(); got != wantErr {
		t.Fatalf("stderr=%q, want %q", got, wantErr)
	}
}

func TestLogColor(t *testing.T) {
	out, errBuf := install(t, output.ColorAlways)

	logging.Success("done")
	logging.Info("plain")
	logging.Error("bad")

	if got, want := out.String(), "\x1b[1;32m[ok]\x1b[0m - done\n[info] - plain\n"; got != want {
		t.Fatalf("stdout=%q, want %q", got, want)
	}
	if got, want := errBuf.String(), "\x1b[1;31m[error]\x1b[0m - bad\n"; got != want {
		t.Fatalf("stderr=%q, want %q", got, want)
	}
}

func TestLevelFiltering(t *testing.T) {
	tests := []struct {
		name    string
		level   logging.Level
		wantOut string
		wantErr string
	}{
		{"error", logging.LevelError, "", "[error] - e\n"},
		{"warn", logging.LevelWarn, "", "[warning] - w\n[error] - e\n"},
		{"info", logging.LevelInfo, "[ok] - o\n[info] - i\n", "[warning] - w\n[error] - e\n"},
		{"debug", logging.LevelDebug, "[ok] - o\n[info] - i\n", "[debug] - d\n[warning] - w\n[error] - e\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, errBuf := install(t, output.ColorNever)
			logging.SetLevel(tc.level)
			logging.Success("o")
			logging.Info("i")
			logging.Debug("d")
			logging.Warning("w")
			logging.Error("e")
			if out.String() != tc.wantOut {
				t.Errorf("stdout=%q, want %q", out.String(), tc.wantOut)
			}
			// stderr order follows call order: debug, warning, error.
			if got := errBuf.String(); got != tc.wantErr {
				t.Errorf("stderr=%q, want %q", got, tc.wantErr)
			}
		})
	}
}

func TestOutputsAndWithOutputs(t *testing.T) {
	_, _ = install(t, output.ColorNever)
	var a, b bytes.Buffer
	logging.WithOutputs(&a, &b, func() {
		logging.Info("x")
		logging.Warning("y")
	})
	if a.String() != "[info] - x\n" || b.String() != "[warning] - y\n" {
		t.Fatalf("WithOutputs: stdout=%q stderr=%q", a.String(), b.String())
	}
	logging.Info("after")
	out, _ := logging.Outputs()
	if out == &a {
		t.Fatal("WithOutputs did not restore the writers")
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]logging.Level{
		"error": logging.LevelError, "WARN": logging.LevelWarn, " warning ": logging.LevelWarn,
		"info": logging.LevelInfo, "debug": logging.LevelDebug,
	} {
		got, err := logging.ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := logging.ParseLevel("loud"); err == nil || !strings.Contains(err.Error(), "loud") {
		t.Errorf("ParseLevel(loud) err = %v", err)
	}
}
