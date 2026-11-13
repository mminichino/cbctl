package capellafile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/settings"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("apiVersion: cbctl/v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDiscoverOrder(t *testing.T) {
	cwd := t.TempDir()
	touch(t, filepath.Join(cwd, "capella.yaml"))
	touch(t, filepath.Join(cwd, "flag.yaml"))
	touch(t, filepath.Join(cwd, "env.yaml"))
	touch(t, filepath.Join(cwd, "sub", "profile.yaml"))
	profile := settings.Profile{ConfigFile: "sub/profile.yaml"}
	env := envOf(map[string]string{"CBCTL_CONFIG": "env.yaml"})

	tests := []struct {
		name    string
		flag    string
		env     func(string) string
		profile settings.Profile
		want    string
		source  string
	}{
		{"flag wins", "flag.yaml", env, profile, "flag.yaml", SourceFlag},
		{"env beats profile and cwd", "", env, profile, "env.yaml", SourceEnv},
		{"profile beats cwd", "", envOf(nil), profile, "sub/profile.yaml", SourceProfile},
		{"cwd capella.yaml", "", envOf(nil), settings.Profile{}, "capella.yaml", SourceYAML},
		{"nil env", "", nil, settings.Profile{}, "capella.yaml", SourceYAML},
		{"blank flag is ignored", "  ", envOf(nil), settings.Profile{}, "capella.yaml", SourceYAML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Discover(tt.flag, tt.env, tt.profile, cwd)
			if err != nil {
				t.Fatal(err)
			}
			if d.Path != filepath.Join(cwd, filepath.FromSlash(tt.want)) || d.Source != tt.source || !d.Exists {
				t.Errorf("Discover() = %+v", d)
			}
		})
	}
}

func TestDiscoverYml(t *testing.T) {
	cwd := t.TempDir()
	touch(t, filepath.Join(cwd, "capella.yml"))
	d, err := Discover("", nil, settings.Profile{}, cwd)
	if err != nil || d.Path != filepath.Join(cwd, "capella.yml") || d.Source != SourceYML || !d.Exists || d.Explicit() || !d.Managed() {
		t.Errorf("Discover() = %+v, %v", d, err)
	}
}

func TestDiscoverBothFilesIsAConfigError(t *testing.T) {
	cwd := t.TempDir()
	touch(t, filepath.Join(cwd, "capella.yaml"))
	touch(t, filepath.Join(cwd, "capella.yml"))
	_, err := Discover("", nil, settings.Profile{}, cwd)
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(ce.Error(), "both capella.yaml and capella.yml found; pass --config") {
		t.Fatalf("err = %v", err)
	}
	// --config resolves the ambiguity.
	d, err := Discover("capella.yml", nil, settings.Profile{}, cwd)
	if err != nil || d.Path != filepath.Join(cwd, "capella.yml") {
		t.Errorf("Discover(--config) = %+v, %v", d, err)
	}
}

func TestDiscoverNothingExists(t *testing.T) {
	cwd := t.TempDir()
	d, err := Discover("", nil, settings.Profile{}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != filepath.Join(cwd, "capella.yaml") || d.Exists || d.Managed() || d.Explicit() || d.Source != SourceDefault {
		t.Errorf("Discover() = %+v", d)
	}
}

func TestDiscoverExplicitPathThatDoesNotExistIsStillManaged(t *testing.T) {
	cwd := t.TempDir()
	tests := []struct {
		name    string
		flag    string
		env     map[string]string
		profile settings.Profile
	}{
		{"flag", "nope.yaml", nil, settings.Profile{}},
		{"env", "", map[string]string{"CBCTL_CONFIG": "nope.yaml"}, settings.Profile{}},
		{"profile", "", nil, settings.Profile{ConfigFile: "nope.yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Discover(tt.flag, envOf(tt.env), tt.profile, cwd)
			if err != nil {
				t.Fatal(err)
			}
			if d.Exists || !d.Explicit() || !d.Managed() || d.Path != filepath.Join(cwd, "nope.yaml") {
				t.Errorf("Discover() = %+v", d)
			}
		})
	}
}

func TestDiscoverAbsolutePathAndDirectory(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	abs := filepath.Join(other, "x.yaml")
	touch(t, abs)
	d, err := Discover(abs, nil, settings.Profile{}, cwd)
	if err != nil || d.Path != abs || !d.Exists {
		t.Errorf("Discover(abs) = %+v, %v", d, err)
	}
	if err := os.Mkdir(filepath.Join(cwd, "capella.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err = Discover("", nil, settings.Profile{}, cwd)
	if err != nil || d.Exists {
		t.Errorf("a directory named capella.yaml is not a config file: %+v, %v", d, err)
	}
}
