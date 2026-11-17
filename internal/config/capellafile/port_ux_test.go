package capellafile_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// handEdit matches advice that tells the reader to write a field into the file by hand, inside a
// string literal: "add password: …", "set allowAnyIp: true", "set apiVersion: cbctl/v1".
var handEdit = regexp.MustCompile(`["'` + "`" + `][^"'` + "`" + `\n]*\b(?:[Aa]dd|[Ss]et)\s+[A-Za-z]+:(?:\s|$|["'` + "`" + `])`)

// TestConfigErrors_CAP062_AC4_NoHandEditAdvice: no message, action or golden in cbctl's Go
// sources tells the user to type a field into capella.yaml; actions name a command instead.
func TestConfigErrors_CAP062_AC4_NoHandEditAdvice(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !(strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".golden")) {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for n, line := range strings.Split(string(b), "\n") {
				if strings.HasSuffix(p, ".golden") {
					line = `"` + line + `"`
				}
				// "add node: %w" is error-wrapping context, not advice.
				if handEdit.MatchString(line) && !strings.Contains(line, ": %w") {
					rel, _ := filepath.Rel(root, p)
					found = append(found, rel+":"+strconv.Itoa(n+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Fatalf("hand-edit advice:\n%s", strings.Join(found, "\n"))
	}
	if !handEdit.MatchString(`"action: set apiVersion: cbctl/v1"`) || handEdit.MatchString(`"run 'cbctl capella sync'"`) {
		t.Fatal("handEdit pattern does not discriminate")
	}
}
