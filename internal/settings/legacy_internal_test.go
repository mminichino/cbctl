package settings

import (
	"path/filepath"
	"testing"
)

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLookupWithLegacy(t *testing.T) {
	t.Parallel()
	if len(LegacyEnv) != 7 {
		t.Fatalf("LegacyEnv has %d names, want 7", len(LegacyEnv))
	}
	for legacy, name := range LegacyEnv {
		t.Run(legacy, func(t *testing.T) {
			t.Parallel()
			cases := []struct {
				name             string
				env              map[string]string
				want, wantSource string
				notice           bool
			}{
				{"legacy only", map[string]string{legacy: "old"}, "old", legacy, true},
				{"both equal", map[string]string{legacy: "same", name: "same"}, "same", name, false},
				{"both different", map[string]string{legacy: "old", name: "new"}, "new", name, true},
				{"neither", map[string]string{}, "", name, false},
			}
			for _, c := range cases {
				v, src, n := lookupWithLegacy(mapEnv(c.env), name)
				if v != c.want || src != c.wantSource || (n != nil) != c.notice {
					t.Errorf("%s: got (%q, %q, %v), want (%q, %q, notice=%v)", c.name, v, src, n, c.want, c.wantSource, c.notice)
				}
				if n != nil && (n.Legacy != legacy || n.Replacement != name) {
					t.Errorf("%s: notice = %+v", c.name, n)
				}
			}
		})
	}
	if v, src, n := lookupWithLegacy(mapEnv(map[string]string{"HOME": "/h"}), "HOME"); v != "/h" || src != "HOME" || n != nil {
		t.Errorf("unmapped name: (%q, %q, %v)", v, src, n)
	}
}

func TestLegacyShimWarnsOncePerVariable(t *testing.T) {
	t.Parallel()
	var got []string
	s := NewLegacyShim(mapEnv(map[string]string{"BEARER_TOKEN": "k", "ORG_ID": "o"}), func(m string) { got = append(got, m) })
	for range 3 {
		if s.Env("CAPELLA_API_KEY") != "k" || s.Env("CAPELLA_ORG_ID") != "o" {
			t.Fatal("legacy value not returned")
		}
	}
	if len(got) != 2 {
		t.Fatalf("warnings = %q, want one per variable", got)
	}
	if NewLegacyShim(mapEnv(nil), nil).Env("CAPELLA_API_KEY") != "" {
		t.Error("nil warn func must still work")
	}
}

func TestLegacyHomeFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, goos string
		env        map[string]string
		want       string
	}{
		{"explicit", "linux", map[string]string{"CAPELLACTL_HOME": "/x", "XDG_CONFIG_HOME": "/xdg"}, "/x"},
		{"windows appdata", "windows", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}, filepath.Join(`C:\Users\u\AppData\Roaming`, "capellactl")},
		{"appdata ignored off windows", "darwin", map[string]string{"APPDATA": "/a", "XDG_CONFIG_HOME": "/xdg"}, filepath.Join("/xdg", "capellactl")},
		{"xdg", "linux", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, filepath.Join("/xdg", "capellactl")},
	}
	for _, c := range cases {
		if got := legacyHomeFor(mapEnv(c.env), c.goos); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// macOS has no Application Support branch in capellactl (settings.py home_dir): ~/.config.
	if got := legacyHomeFor(mapEnv(nil), "darwin"); filepath.Base(filepath.Dir(got)) != ".config" || filepath.Base(got) != "capellactl" {
		t.Errorf("default: %q", got)
	}
}
