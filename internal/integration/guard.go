//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// Errors the guards return; callers branch on them with errors.Is.
var (
	ErrOrgNotAllowed = errors.New("sandbox organization is not in CBCTL_IT_ALLOWED_ORG_IDS")
	ErrNoAllowlist   = errors.New("CBCTL_IT_ALLOWED_ORG_IDS is empty; CI must name the sandbox organization")
	ErrBadPrefix     = errors.New("CBCTL_TEST_PREFIX must match ^cbctl-ci-([a-z0-9]+-)*$ (9 to 40 characters)")
)

var rePrefix = regexp.MustCompile(`^cbctl-ci-([a-z0-9]+-)*$`)

// GuardPolicy decides whether the suite may touch a Capella organization. It exists so a
// misconfigured secret can never point the nightly at a production organization, and so a CI
// run whose credentials are missing fails instead of skipping everything and passing.
type GuardPolicy struct {
	RequireTarget bool     // CBCTL_IT_REQUIRE_TARGET=1: a skip for lack of a target is a failure
	AllowedOrgs   []string // CBCTL_IT_ALLOWED_ORG_IDS, comma-separated
	InCI          bool     // GITHUB_ACTIONS=true
}

// PolicyFromEnv reads the policy from env (os.Getenv in the suite).
func PolicyFromEnv(env func(string) string) GuardPolicy {
	var orgs []string
	for _, o := range strings.Split(env("CBCTL_IT_ALLOWED_ORG_IDS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			orgs = append(orgs, o)
		}
	}
	return GuardPolicy{
		RequireTarget: env("CBCTL_IT_REQUIRE_TARGET") == "1",
		AllowedOrgs:   orgs,
		InCI:          env("GITHUB_ACTIONS") == "true",
	}
}

// CheckOrg returns nil when orgID may be used. In CI the allowlist is mandatory; locally an
// empty allowlist allows any organization (the developer chose the credentials).
func (p GuardPolicy) CheckOrg(orgID string) error {
	if len(p.AllowedOrgs) == 0 {
		if p.InCI {
			return ErrNoAllowlist
		}
		return nil
	}
	for _, o := range p.AllowedOrgs {
		if strings.EqualFold(o, strings.TrimSpace(orgID)) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrOrgNotAllowed, orgID)
}

// VerifyOrgReachable checks with one read that the key can see orgID, so a key/organization
// mismatch fails before the first write instead of halfway through a test.
func (p GuardPolicy) VerifyOrgReachable(ctx context.Context, c *capellaapi.Client) error {
	if _, err := c.ListProjects(ctx); err != nil {
		return fmt.Errorf("the API key cannot read the sandbox organization: %w", err)
	}
	return nil
}

// ValidatePrefix returns the run prefix to use. An empty value is the default Prefix. In CI
// only the default is allowed, so the weekly janitor always matches what a run left behind.
func ValidatePrefix(p string, inCI bool) (string, error) {
	if p == "" {
		return Prefix, nil
	}
	if !strings.HasSuffix(p, "-") {
		p += "-"
	}
	if len(p) < len(Prefix) || len(p) > 40 || !rePrefix.MatchString(p) {
		return "", fmt.Errorf("%w (got %q)", ErrBadPrefix, p)
	}
	if inCI && p != Prefix {
		return "", fmt.Errorf("%w: CI must use the default %q (got %q)", ErrBadPrefix, Prefix, p)
	}
	return p, nil
}

// SkipOrFail skips the test, or fails it when the policy requires a target.
func (p GuardPolicy) SkipOrFail(tb testing.TB, reason string) {
	tb.Helper()
	if p.RequireTarget {
		noteRequiredButMissing()
		tb.Fatalf("CBCTL_IT_REQUIRE_TARGET=1 but %s", reason)
	}
	tb.Skip(reason)
}

// suitePolicy is the policy of this process, read once from the environment.
func suitePolicy() GuardPolicy { return PolicyFromEnv(os.Getenv) }
