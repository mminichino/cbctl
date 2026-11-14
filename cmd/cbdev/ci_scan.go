package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mminichino/cbctl/internal/ci/secretscan"
)

func cmdCIScanSecrets(args []string, stdout, stderr io.Writer) int {
	files, envs, secretsDirs, valueFiles, err := parseScanArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "cbdev ci scan-secrets: %v\n", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "cbdev ci scan-secrets: missing FILE")
		fmt.Fprintln(stderr, "usage: cbdev ci scan-secrets FILE… [--env NAME]… [--secrets-dir DIR]… [--value-file FILE]…")
		return 2
	}

	secrets := map[string]string{}
	for _, name := range envs {
		value := os.Getenv(name)
		if value == "" {
			fmt.Fprintf(stderr, "scan-log-secrets: %s is not set\n", name)
			return 2
		}
		secrets["$"+name] = value
	}
	for _, d := range secretsDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			for k, v := range secretscan.SecretsInDir(d) {
				secrets[k] = v
			}
		}
	}
	for _, f := range valueFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "scan-log-secrets: read %s: %v\n", f, err)
			return 2
		}
		secrets["contents of "+filepath.Base(f)] = strings.TrimSpace(string(raw))
	}

	for k, v := range secrets {
		if len(v) < secretscan.MinLength {
			fmt.Fprintf(stderr, "scan-log-secrets: not checking %s: shorter than %d characters\n", k, secretscan.MinLength)
			delete(secrets, k)
		}
	}
	if len(secrets) == 0 {
		fmt.Fprintln(stderr, "scan-log-secrets: no secret values to check for")
		return 2
	}

	texts := map[string]string{}
	var missing []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			missing = append(missing, f)
			continue
		}
		// Match Python errors="replace" for non-UTF8 bytes.
		texts[f] = strings.ToValidUTF8(string(raw), "\uFFFD")
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "scan-log-secrets: no such file: %s\n", joinComma(missing))
		return 2
	}

	hits := secretscan.Scan(texts, secrets)
	for _, h := range hits {
		fmt.Fprintf(stdout, "%s:%d: contains %s\n", h.File, h.Line, h.Source)
	}
	if len(hits) > 0 {
		fmt.Fprintf(stderr, "FAILED: %d line(s) contain a secret\n", len(hits))
		return 1
	}
	fmt.Fprintf(stdout, "no secrets in %d file(s); %d secret value(s) checked\n", len(texts), len(secrets))
	return 0
}

// parseScanArgs accepts flags interleaved with file operands (as the Python script does).
func parseScanArgs(args []string) (files, envs, dirs, values []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return nil, nil, nil, nil, fmt.Errorf("usage: cbdev ci scan-secrets FILE… [--env NAME]… [--secrets-dir DIR]… [--value-file FILE]…")
		case a == "--env" || a == "-env":
			if i+1 >= len(args) {
				return nil, nil, nil, nil, fmt.Errorf("--env needs a NAME")
			}
			i++
			envs = append(envs, args[i])
		case a == "--secrets-dir" || a == "-secrets-dir":
			if i+1 >= len(args) {
				return nil, nil, nil, nil, fmt.Errorf("--secrets-dir needs a DIR")
			}
			i++
			dirs = append(dirs, args[i])
		case a == "--value-file" || a == "-value-file":
			if i+1 >= len(args) {
				return nil, nil, nil, nil, fmt.Errorf("--value-file needs a FILE")
			}
			i++
			values = append(values, args[i])
		case strings.HasPrefix(a, "-"):
			return nil, nil, nil, nil, fmt.Errorf("unknown flag %s", a)
		default:
			files = append(files, a)
		}
	}
	return files, envs, dirs, values, nil
}

func joinComma(ss []string) string {
	return strings.Join(ss, ", ")
}
