package secrets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/redact"
	"github.com/mminichino/cbctl/internal/secrets"
)

// TestSecretsOut_CAP008_AC2_FileModeAndContent: CLUSTER.NAME.json with cluster, name, id, password
// and createdAt, mode 0600 in a 0700 directory, and the password registered for redaction.
func TestSecretsOut_CAP008_AC2_FileModeAndContent(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	dir := filepath.Join(t.TempDir(), ".cbctl", "credentials")
	path, err := secrets.WriteGeneratedCredential(dir, "nova-dev-01", "web_sessions", "u-1", "Gen3rated!Pw-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "nova-dev-01.web_sessions.json" {
		t.Errorf("file name = %s", filepath.Base(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "cluster,createdAt,id,name,password" || data["password"] != "Gen3rated!Pw-1" {
		t.Errorf("content = %v", data)
	}
	if data["cluster"] != "nova-dev-01" || data["name"] != "web_sessions" || data["id"] != "u-1" {
		t.Errorf("content = %v", data)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		di, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("modes: file %o, dir %o", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	if strings.Contains(redact.String("x Gen3rated!Pw-1 y"), "Gen3rated!Pw-1") {
		t.Error("password not registered for redaction")
	}
}
