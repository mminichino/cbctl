package clitest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/clitest"
)

// recordTB captures a Fatalf instead of ending the test, so the failing path can be asserted.
type recordTB struct {
	testing.TB
	msg string
}

type fatal struct{}

func (r *recordTB) Helper() {}

func (r *recordTB) Fatalf(format string, args ...any) {
	r.msg = fmt.Sprintf(format, args...)
	panic(fatal{})
}

func failsWith(t *testing.T, fn func(tb testing.TB)) string {
	t.Helper()
	r := &recordTB{TB: t}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(fatal); !ok {
					panic(v)
				}
			}
		}()
		fn(r)
	}()
	if r.msg == "" {
		t.Fatal("assertion passed, want a failure")
	}
	return r.msg
}

func TestAssertJSONSchema(t *testing.T) {
	scale := `{"cluster":"c1","group":"data","from":3,"to":4,"status":"scaled"}`
	t.Run("named schemas", func(t *testing.T) {
		clitest.AssertJSONSchema(t, "scale", []byte(scale))
		clitest.AssertJSONSchema(t, "scale.schema.json", []byte("["+scale+"]"))
		clitest.AssertJSONSchema(t, "plan", []byte(`{"plan":{"actions":[]}}`))
		clitest.AssertJSONSchema(t, "apply", []byte(`{"plan":{"actions":[]},"result":{"status":"ok","actions":[]}}`))
	})
	t.Run("inline schema", func(t *testing.T) {
		clitest.AssertJSONSchema(t, `{"type":"object","required":["a"]}`, []byte(`{"a":1}`))
	})
	cases := []struct {
		name, schema, doc, want string
	}{
		{"bad status", "scale", strings.Replace(scale, "scaled", "exploded", 1), "does not match"},
		{"extra field", "scale", strings.Replace(scale, `"from"`, `"extra":1,"from"`, 1), "does not match"},
		{"apply refs plan", "apply", `{"plan":{"actions":[{"kind":"k","key":"x","action":"explode","changes":[]}]},"result":{"status":"ok","actions":[]}}`, "does not match"},
		{"not json", "plan", `not json`, "not one JSON document"},
		{"unknown schema", "nope", `{}`, "unknown schema"},
		{"inline mismatch", `{"type":"array"}`, `{}`, "does not match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := failsWith(t, func(tb testing.TB) { clitest.AssertJSONSchema(tb, tc.schema, []byte(tc.doc)) })
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("failure %q does not contain %q", msg, tc.want)
			}
		})
	}
}
