// Package secretscan finds secret values in CI logs without printing the values
// themselves (CAP-032 AC4).
package secretscan

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// MinLength is the shortest secret value that is checked. Shorter values would
// match ordinary words.
const MinLength = 8

// Hit is one line that contains a secret. The value itself is never included.
type Hit struct {
	File   string
	Line   int
	Source string
}

// Variants returns the forms of v that a secret may take in a log: the raw
// string, its JSON-escaped form (without surrounding quotes), and URL path and
// query encodings.
func Variants(v string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		if _, ok := seen[s]; ok || s == "" {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(v)
	if b, err := json.Marshal(v); err == nil && len(b) >= 2 {
		add(string(b[1 : len(b)-1]))
	}
	add(url.PathEscape(v))
	add(url.QueryEscape(v))
	return out
}

// SecretsInDir walks dir for *.json files and collects every string "password"
// field. Keys are "password in <basename>"; values are the passwords.
func SecretsInDir(dir string) map[string]string {
	found := map[string]string{}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil
		}
		pw, ok := data["password"].(string)
		if !ok {
			return nil
		}
		found["password in "+d.Name()] = pw
		return nil
	})
	return found
}

// Scan reports every line in texts that contains any form of a secret in
// secrets. texts maps a display name (usually a path) to file contents.
// secrets maps a source label (e.g. "$CAPELLA_SANDBOX_API_KEY") to the value.
// Hits never include the secret value.
func Scan(texts map[string]string, secrets map[string]string) []Hit {
	type prepared struct {
		label    string
		variants []string
	}
	prepped := make([]prepared, 0, len(secrets))
	for label, value := range secrets {
		prepped = append(prepped, prepared{label: label, variants: Variants(value)})
	}
	var hits []Hit
	for name, text := range texts {
		for n, line := range splitLines(text) {
			for _, p := range prepped {
				for _, v := range p.variants {
					if strings.Contains(line, v) {
						hits = append(hits, Hit{File: name, Line: n + 1, Source: p.label})
						break
					}
				}
			}
		}
	}
	return hits
}

// splitLines matches Python str.splitlines: no trailing empty entry from a final newline.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}
