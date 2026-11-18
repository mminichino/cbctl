package capellafile

import (
	"regexp"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/secrets"
)

var (
	refRe = regexp.MustCompile(`\$\{(file:)?([^}]+)\}`)
)

// isPasswordPath reports whether p is …databaseCredentials[i].password.
func isPasswordPath(p Path) bool {
	if len(p) < 3 {
		return false
	}
	last, _ := p[len(p)-1].(string)
	_, isIdx := p[len(p)-2].(int)
	list, _ := p[len(p)-3].(string)
	return last == "password" && isIdx && list == "databaseCredentials"
}

// resolveSecrets replaces ${VAR} and ${file:PATH} in every string of data and reports the
// references that cannot be resolved and the passwords that are literals. Resolved passwords are
// registered with the redactor immediately. A missing organizationId reference is dropped
// instead of reported: the organization may come from a profile or the environment at run time.
func resolveSecrets(data Obj, baseDir string, lines lineIndex, env func(string) string) []Issue {
	r := &secretResolver{baseDir: baseDir, lines: lines, env: env}
	r.walkObj(data, nil)
	return r.issues
}

type secretResolver struct {
	baseDir string
	lines   lineIndex
	env     func(string) string
	issues  []Issue
}

func (r *secretResolver) walkObj(m Obj, p Path) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := append(append(Path{}, p...), k)
		switch v := m[k].(type) {
		case string:
			if out, keep := r.resolveString(v, child); keep {
				m[k] = out
			} else {
				delete(m, k)
			}
		case Obj:
			r.walkObj(v, child)
		case []any:
			r.walkList(v, child)
		}
	}
}

func (r *secretResolver) walkList(l []any, p Path) {
	for i, item := range l {
		child := append(append(Path{}, p...), i)
		switch v := item.(type) {
		case string:
			if out, keep := r.resolveString(v, child); keep {
				l[i] = out
			}
		case Obj:
			r.walkObj(v, child)
		case []any:
			r.walkList(v, child)
		}
	}
}

// resolveString returns the resolved value; keep is false when the value must be removed from
// its parent.
func (r *secretResolver) resolveString(value string, p Path) (out string, keep bool) {
	password := isPasswordPath(p)
	topic := "config"
	if password {
		topic = "credentials"
	}
	add := func(msg, fix string) {
		r.issues = append(r.issues, Issue{Path: FormatPath(p), Message: msg, Line: r.lines.of(p), Topic: topic, Fix: fix})
	}
	if password {
		value = strings.TrimSpace(value)
		if value == "" {
			add("password must not be empty", "omit password to have Capella generate one, or reference a secret: password: ${VAR}")
			return value, true
		}
		if !secrets.IsWholeRef(value) {
			add("password must be a ${VAR} or ${file:PATH} reference, not a literal",
				"replace the literal with ${NAME} and export NAME, or use ${file:PATH}; omit password to have one generated")
			return value, true
		}
	}
	if !strings.Contains(value, "${") {
		return value, true
	}

	resolved, found := secrets.ExpandRefs(value, secrets.RefOptions{BaseDir: r.baseDir, Env: r.env})
	problems := make([]Issue, len(found))
	for i, f := range found {
		problems[i] = Issue{Message: f.Message, Fix: f.Fix}
	}
	if len(problems) > 0 {
		if len(p) == 1 && p[0] == "organizationId" {
			return "", false
		}
		for _, pr := range problems {
			add(pr.Message, pr.Fix)
		}
		return value, true
	}
	if password {
		redact.Register(resolved)
	}
	return resolved, true
}

// expandRefs resolves ${VAR} references in value without reporting problems; ok is false when a
// reference is a file reference or its variable is unset. It never touches the file system.
func expandRefs(value string, env func(string) string) (string, bool) {
	ok := true
	out := refRe.ReplaceAllStringFunc(value, func(match string) string {
		m := refRe.FindStringSubmatch(match)
		if m[1] != "" {
			ok = false
			return ""
		}
		v := env(strings.TrimSpace(m[2]))
		if v == "" {
			ok = false
		}
		return v
	})
	return out, ok
}
