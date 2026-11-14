package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/redact"
)

// FileExistsError is returned when Preflight refuses to overwrite an existing secret file.
type FileExistsError struct {
	Path string
}

// Error implements error.
func (e *FileExistsError) Error() string {
	return fmt.Sprintf("refusing to overwrite %s; move it or pass --force-overwrite-secret", e.Path)
}

// SecretPath returns <dir>/<cluster>.<name>.json.
func SecretPath(dir, cluster, name string) string {
	return filepath.Join(dir, cluster+"."+name+".json")
}

// Preflight returns the secret path, or FileExistsError when the file exists and force is false.
func Preflight(dir, cluster, name string, force bool) (string, error) {
	path := SecretPath(dir, cluster, name)
	if force {
		return path, nil
	}
	if _, err := os.Stat(path); err == nil {
		return "", &FileExistsError{Path: path}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

type credentialFile struct {
	Cluster   string `json:"cluster"`
	Name      string `json:"name"`
	ID        string `json:"id"`
	Password  string `json:"password"`
	CreatedAt string `json:"createdAt"`
}

// WriteGeneratedCredential writes a 0600 JSON secret file under a 0700 directory.
func WriteGeneratedCredential(dir, cluster, name, credID, password string, force bool) (string, error) {
	path, err := Preflight(dir, cluster, name, force)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create secrets dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	redact.Register(password)
	body, err := json.MarshalIndent(credentialFile{
		Cluster:   cluster,
		Name:      name,
		ID:        credID,
		Password:  password,
		CreatedAt: engine.RealClock{}.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, ".cred-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	_ = os.Chmod(path, 0o600)
	return path, nil
}

// WriteRotatedCredential writes a new secret and keeps the previous content as .bak.
func WriteRotatedCredential(dir, cluster, name, credID, password string) (path, backup string, err error) {
	path = SecretPath(dir, cluster, name)
	if raw, rerr := os.ReadFile(path); rerr == nil {
		backup = path + ".bak"
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return "", "", err
		}
	}
	written, err := WriteGeneratedCredential(dir, cluster, name, credID, password, true)
	return written, backup, err
}

// RestoreRotatedCredential restores path from backup after a failed rotation send.
func RestoreRotatedCredential(path, backup string) {
	if backup == "" || path == "" {
		return
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}

// SavedMessage is the only user-facing line printed about a generated password.
func SavedMessage(path string) string {
	return "password saved to " + path
}

// GitWarning reports when the secrets directory is inside a git work tree and not ignored.
func GitWarning(dir string) (what, action string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	top, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", "", false
	}
	_ = strings.TrimSpace(string(top))
	check := exec.CommandContext(ctx, "git", "check-ignore", "-q", dir)
	if err := check.Run(); err == nil {
		return "", "", false // ignored
	}
	return dir + " is not git-ignored", "Add .cbctl/ to .gitignore so the saved password is never committed.", true
}
