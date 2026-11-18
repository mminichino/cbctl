package secrets

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RefKind says where a reference takes its value from.
type RefKind int

// The reference forms: ${VAR}, ${file:PATH} and ${base64:VALUE}.
const (
	RefEnv RefKind = iota
	RefFile
	RefBase64
)

var (
	refRe      = regexp.MustCompile(`\$\{(file:|base64:)?([^}]+)\}`)
	wholeRefRe = regexp.MustCompile(`^\$\{(file:|base64:)?([^}]+)\}$`)
)

// Problem is one reason a reference cannot be resolved. Message never contains a secret value.
type Problem struct {
	Message string
	Fix     string
}

// RefOptions says how references resolve.
type RefOptions struct {
	// BaseDir is the directory relative ${file:PATH} references are read from.
	BaseDir string
	// Env looks up an environment variable; it must not be nil.
	Env func(string) string
	// AllowBase64 enables ${base64:VALUE}. Without it the text is an environment variable
	// name, as capella.yaml has always treated it.
	AllowBase64 bool
}

// Ref is one parsed reference.
type Ref struct {
	Kind RefKind
	// Arg is the variable name, the file path or the encoded value.
	Arg string
}

// ParseRef parses value as exactly one reference (surrounding white space is ignored). With
// allowBase64 unset, ${base64:X} is the environment variable "base64:X".
func ParseRef(value string, allowBase64 bool) (Ref, bool) {
	m := wholeRefRe.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return Ref{}, false
	}
	return newRef(m, allowBase64), true
}

func newRef(m []string, allowBase64 bool) Ref {
	arg := strings.TrimSpace(m[2])
	switch {
	case m[1] == "" && arg == "base64:" && allowBase64:
		// "${base64:}": the pattern needs a character after the colon, so the empty form arrives as the name.
		return Ref{Kind: RefBase64}
	case m[1] == "file:":
		return Ref{Kind: RefFile, Arg: arg}
	case m[1] == "base64:" && allowBase64:
		return Ref{Kind: RefBase64, Arg: arg}
	case m[1] == "base64:":
		return Ref{Kind: RefEnv, Arg: "base64:" + arg}
	}
	return Ref{Kind: RefEnv, Arg: arg}
}

// Resolve returns the value of the reference. A failure returns an empty value and one Problem.
func (r Ref) Resolve(opt RefOptions) (string, *Problem) {
	switch r.Kind {
	case RefFile:
		path := r.Arg
		if !filepath.IsAbs(path) {
			path = filepath.Join(opt.BaseDir, path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", &Problem{Message: fmt.Sprintf("file %s not found or unreadable (resolved to %s)", r.Arg, path),
				Fix: "create the file or fix the path (relative paths are relative to the config file)"}
		}
		text := trimOneNewline(string(b))
		if text == "" {
			return "", &Problem{Message: fmt.Sprintf("file %s is empty (resolved to %s)", r.Arg, path),
				Fix: "write the value into the file"}
		}
		return text, nil
	case RefBase64:
		b, err := base64.StdEncoding.Strict().DecodeString(r.Arg)
		if err != nil && r.Arg != "" {
			// The encoded value is itself the secret, so it is never echoed.
			return "", &Problem{Message: "${base64:…} is not valid standard base64 (RFC 4648, padding required)",
				Fix: "encode the value with 'printf %s \"$VALUE\" | base64'"}
		}
		if len(b) == 0 {
			return "", &Problem{Message: "${base64:…} decodes to an empty value",
				Fix: "put the encoded password between the braces"}
		}
		return string(b), nil
	}
	v := opt.Env(r.Arg)
	if v == "" {
		return "", &Problem{Message: fmt.Sprintf("environment variable %s is not set or empty", r.Arg),
			Fix: fmt.Sprintf("export %s=…", r.Arg)}
	}
	return v, nil
}

// ExpandRefs replaces every reference inside value. Unresolvable references expand to nothing
// and are returned as problems, in order. A value without "${" is returned unchanged.
func ExpandRefs(value string, opt RefOptions) (string, []Problem) {
	if !strings.Contains(value, "${") {
		return value, nil
	}
	var problems []Problem
	out := refRe.ReplaceAllStringFunc(value, func(match string) string {
		v, p := newRef(refRe.FindStringSubmatch(match), opt.AllowBase64).Resolve(opt)
		if p != nil {
			problems = append(problems, *p)
		}
		return v
	})
	return out, problems
}

// IsWholeRef reports whether value is exactly one reference of any form.
func IsWholeRef(value string) bool { return wholeRefRe.MatchString(strings.TrimSpace(value)) }

func trimOneNewline(s string) string {
	if strings.HasSuffix(s, "\r\n") {
		return strings.TrimSuffix(s, "\r\n")
	}
	return strings.TrimSuffix(s, "\n")
}
