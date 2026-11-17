package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/ci/secretscan"
)

func cmdCIScanSecrets(args []string, stdout, stderr io.Writer) int {
	o, err := parseScanArgs(args)
	files, envs, secretsDirs, valueFiles := o.files, o.envs, o.dirs, o.values
	if err != nil {
		fmt.Fprintf(stderr, "cbdev ci scan-secrets: %v\n", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "cbdev ci scan-secrets: missing FILE")
		fmt.Fprintln(stderr, scanUsage)
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
	if len(secrets) < o.requireSecrets {
		// Fewer secrets than the run created means the scan would miss some: a secrets dir that
		// was not written, or an env var that went empty.
		fmt.Fprintf(stderr, "scan-log-secrets: only %d secret value(s) to check; --require-secrets %d\n", len(secrets), o.requireSecrets)
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
		if !o.allowMissing || len(texts) == 0 {
			fmt.Fprintf(stderr, "scan-log-secrets: no such file: %s\n", joinComma(missing))
			return 2
		}
		fmt.Fprintf(stderr, "scan-log-secrets: skipping missing file(s): %s\n", joinComma(missing))
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

const scanUsage = "usage: cbdev ci scan-secrets FILE… [--env NAME]… [--secrets-dir DIR]… [--value-file FILE]… [--require-secrets N] [--allow-missing]"

// scanOptions are the parsed scan-secrets arguments.
type scanOptions struct {
	files, envs, dirs, values []string
	requireSecrets            int  // at least this many secret values, else exit 2
	allowMissing              bool // missing FILEs are skipped (at least one must exist)
}

// parseScanArgs accepts flags interleaved with file operands (as the Python script does).
func parseScanArgs(args []string) (scanOptions, error) {
	var o scanOptions
	next := func(i *int, flag, what string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a %s", flag, what)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := strings.TrimPrefix(args[i], "-")
		if !strings.HasPrefix(args[i], "-") {
			o.files = append(o.files, args[i])
			continue
		}
		a = strings.TrimPrefix(a, "-")
		var err error
		var v string
		switch a {
		case "h", "help":
			return o, fmt.Errorf("%s", scanUsage)
		case "env":
			v, err = next(&i, "--env", "NAME")
			o.envs = append(o.envs, v)
		case "secrets-dir":
			v, err = next(&i, "--secrets-dir", "DIR")
			o.dirs = append(o.dirs, v)
		case "value-file":
			v, err = next(&i, "--value-file", "FILE")
			o.values = append(o.values, v)
		case "require-secrets":
			if v, err = next(&i, "--require-secrets", "number"); err == nil {
				if o.requireSecrets, err = strconv.Atoi(v); err != nil || o.requireSecrets < 0 {
					err = fmt.Errorf("--require-secrets needs a number >= 0 (got %q)", v)
				}
			}
		case "allow-missing":
			o.allowMissing = true
		default:
			return o, fmt.Errorf("unknown flag %s", args[i])
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

func joinComma(ss []string) string {
	return strings.Join(ss, ", ")
}
