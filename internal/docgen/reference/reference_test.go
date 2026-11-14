package reference

import "testing"

func TestSchemaFieldsNonEmpty(t *testing.T) {
	f, err := SchemaFields()
	if err != nil {
		t.Fatal(err)
	}
	if len(f) == 0 {
		t.Fatal("expected schema fields")
	}
	t.Logf("fields=%d", len(f))
}
