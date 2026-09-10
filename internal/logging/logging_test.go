package logging_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/mminichino/cbctl/internal/logging"
)

func TestLogFormat(t *testing.T) {
	color.NoColor = true
	var out, errBuf bytes.Buffer
	logging.SetOutputs(&out, &errBuf)

	logging.Info("Cluster created on %s", "127.0.0.1")
	logging.Warning("Collection not empty")
	logging.Error("Failed to create cluster: %s", "boom")
	logging.Println("true")

	if got := out.String(); !strings.Contains(got, "[info] - Cluster created on 127.0.0.1") {
		t.Fatalf("stdout=%q", got)
	}
	if !strings.Contains(out.String(), "true\n") {
		t.Fatalf("missing true: %q", out.String())
	}
	if got := errBuf.String(); !strings.Contains(got, "[warning] - Collection not empty") {
		t.Fatalf("stderr=%q", got)
	}
	if !strings.Contains(errBuf.String(), "[error] - Failed to create cluster: boom") {
		t.Fatalf("stderr=%q", errBuf.String())
	}
}
