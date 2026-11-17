package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
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
	chmodOrWarn(dir, dir, 0o700)
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
	chmodOrWarn(dir, path, 0o600)
	return path, nil
}

// chmodWarned records the directories already warned about, so a filesystem without POSIX
// permissions (FAT, some network mounts) produces one warning per directory, not one per file.
var chmodWarned sync.Map

// chmodOrWarn tightens path's mode. A failure is not fatal (the file was created with the right
// mode where the filesystem supports it), but the user is told once per dir.
func chmodOrWarn(dir, path string, mode os.FileMode) {
	if err := os.Chmod(path, mode); err != nil {
		if _, seen := chmodWarned.LoadOrStore(dir, true); !seen {
			logging.Warning("could not restrict permissions under %s: %v; keep this directory private", dir, err)
		}
	}
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

// RestoreRotatedCredential undoes WriteRotatedCredential after the rotation PUT failed, so the
// file again holds the password Capella still has. With a backup it copies backup over path;
// without one (the file did not exist before) it removes the never-used new file. The error
// names what could not be done; the caller reports it next to the PUT error.
func RestoreRotatedCredential(path, backup string) error {
	if path == "" {
		return nil
	}
	if backup == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
		return nil
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		return fmt.Errorf("reading backup %s: %w", backup, err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("restoring %s: %w", path, err)
	}
	return nil
}

// RestoreError reports a failed rotation PUT whose password file could not be put back. Primary
// is the API error; the file at Path now holds a password Capella never accepted.
type RestoreError struct {
	Primary error
	Restore error
	Path    string
	Backup  string
}

// Error renders the primary error, then the restore failure.
func (e *RestoreError) Error() string {
	return fmt.Sprintf("%v; could not restore previous password file: %v", e.Primary, e.Restore)
}

// Unwrap exposes the primary error so exit-code mapping (API, timeout, cancel) still applies.
func (e *RestoreError) Unwrap() error { return e.Primary }

// Action tells the user how to repair the file by hand.
func (e *RestoreError) Action() string {
	if e.Backup == "" {
		return "delete " + e.Path + "; it holds a password Capella did not accept"
	}
	return "copy " + e.Backup + " to " + e.Path
}

// SavedMessage is the only user-facing line printed about a generated password.
func SavedMessage(path string) string {
	return "password saved to " + path
}

// GitWarning reports when the secrets directory is inside a git work tree and not ignored.
// git runs with -C dir so the answer is about dir's repository, not the process's cwd.
func GitWarning(dir string) (what, action string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe := dir
	if _, err := os.Stat(probe); err != nil {
		probe = filepath.Dir(dir)
	}
	if err := exec.CommandContext(ctx, "git", "-C", probe, "rev-parse", "--show-toplevel").Run(); err != nil {
		return "", "", false // not in a work tree, or git is missing
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	check := exec.CommandContext(ctx, "git", "-C", probe, "check-ignore", "-q", abs)
	if err := check.Run(); err == nil {
		return "", "", false // ignored
	}
	return dir + " is not git-ignored", "Add .cbctl/ to .gitignore so the saved password is never committed.", true
}
