package capellafake_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

// TestFake_CAP004_AC3_DuplicateChild409: creating a bucket, scope, collection, credential or
// allowed CIDR that already exists answers 409 with Capella's "already exists" code 1010.
func TestFake_CAP004_AC3_DuplicateChild409(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"bucket", "scope", "collection", "credential", "cidr"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, c, p, cl := fixture(t)
			b := f.SeedBucket(cl, "b")
			create := map[string]func() error{
				"bucket": func() error {
					_, err := c.CreateBucket(ctx, p, cl, capellaapi.CreateBucketRequest{Name: "b", Type: "couchbase", MemoryAllocationInMb: 100})
					return err
				},
				"scope":      func() error { return c.CreateScope(ctx, p, cl, b, "s1") },
				"collection": func() error { return c.CreateCollection(ctx, p, cl, b, "_default", "c1", nil) },
				"credential": func() error {
					_, _, err := c.CreateCredential(ctx, p, cl, capellaapi.CreateCredentialRequest{Name: "u1", Access: []capellaapi.AccessEntry{}})
					return err
				},
				"cidr": func() error {
					_, err := c.CreateAllowedCIDR(ctx, p, cl, "10.0.0.0/24", "", "")
					return err
				},
			}[kind]
			if kind != "bucket" { // the bucket is seeded already
				if err := create(); err != nil {
					t.Fatalf("first create: %v", err)
				}
			}
			err := create()
			var apiErr *capellaapi.APIError
			if !errors.Is(err, capellaapi.ErrConflict) || !errors.As(err, &apiErr) {
				t.Fatalf("duplicate %s: err = %v, want a 409 APIError", kind, err)
			}
			if apiErr.Status != http.StatusConflict || apiErr.Code != 1010 {
				t.Fatalf("duplicate %s: status %d code %d, want 409 code 1010", kind, apiErr.Status, apiErr.Code)
			}
		})
	}
}
