package apisnap

import (
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
)

func TestBuildFromMinimalSpec(t *testing.T) {
	spec := map[string]any{
		"info": map[string]any{"version": "v4.0"},
		"paths": map[string]any{
			"/v4/organizations": map[string]any{
				"get": map[string]any{
					"summary": "List Organizations",
					"responses": map[string]any{
						"200": map[string]any{
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"data": map[string]any{"type": "array"},
										},
										"required": []any{"data"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	doc := buildFromSpec(spec, "test://spec", "2026-01-01", []capellaapi.Endpoint{
		{Method: "GET", Path: "/v4/organizations"},
		{Method: "POST", Path: "/v4/nowhere"},
	})
	if doc.APIVersion != "v4.0" || doc.Retrieved != "2026-01-01" {
		t.Fatalf("meta = %+v", doc)
	}
	if len(doc.Operations) != 2 {
		t.Fatalf("ops = %d", len(doc.Operations))
	}
	if !doc.Operations[0].Documented || doc.Operations[0].Summary != "List Organizations" {
		t.Fatalf("op0 = %+v", doc.Operations[0])
	}
	if doc.Operations[1].Documented {
		t.Fatalf("missing path should be undocumented: %+v", doc.Operations[1])
	}
	raw, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"documented": true`) {
		t.Fatalf("encode missing documented:\n%s", raw)
	}
	if miss := Missing(doc); len(miss) != 1 || miss[0] != "POST /v4/nowhere" {
		t.Fatalf("missing = %v", miss)
	}
}

func TestResolveRef(t *testing.T) {
	spec := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Id": map[string]any{"type": "string"},
			},
		},
	}
	got := resolve(map[string]any{"$ref": "#/components/schemas/Id"}, spec, nil)
	m, _ := got.(map[string]any)
	if m["type"] != "string" {
		t.Fatalf("got %#v", got)
	}
}
