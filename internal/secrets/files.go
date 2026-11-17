package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// git -C needs an existing directory: probe the nearest existing ancestor, so a secrets dir
	// that is not created yet is still checked against the repository it will be in.
	probe := dir
	for {
		if st, err := os.Stat(probe); err == nil && st.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", "", false
		}
		probe = parent
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

// Default and capellactl credential directories, relative to the config file's directory.
const (
	DefaultDirName = ".cbctl/credentials"
	LegacyDirName  = ".capellactl/credentials"
)

// ResolveCredentialsDir returns the directory generated passwords go to. configured is the
// file's credentialsOutput (empty: unset) and base the directory relative paths resolve against.
// With no setting, ./.cbctl/credentials is used, unless only capellactl's ./.capellactl/credentials
// exists: then that one is used, so rotations update the files capellactl wrote. note explains a
// legacy choice (or that the legacy directory is ignored because both exist).
func ResolveCredentialsDir(configured, base string) (dir string, legacy bool, note string) {
	if strings.TrimSpace(configured) != "" {
		if filepath.IsAbs(configured) {
			return filepath.Clean(configured), false, ""
		}
		return filepath.Join(base, configured), false, ""
	}
	cur := filepath.Join(base, filepath.FromSlash(DefaultDirName))
	old := filepath.Join(base, filepath.FromSlash(LegacyDirName))
	curOK, oldOK := isDir(cur), isDir(old)
	switch {
	case oldOK && !curOK:
		return old, true, fmt.Sprintf("generated passwords are saved to capellactl's %s because %s does not exist; move the files and set credentialsOutput to change it",
			old, cur)
	case oldOK && curOK:
		return cur, false, fmt.Sprintf("%s is ignored because %s exists; move its files there", old, cur)
	}
	return cur, false, ""
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// NotIgnoredError refuses to write a generated password into a directory git would commit.
type NotIgnoredError struct {
	Dir string
}

// Error implements error.
func (e *NotIgnoredError) Error() string {
	return fmt.Sprintf("refusing to save a generated password: %s is inside a git work tree and not git-ignored", e.Dir)
}

// Action is the fix printed under the error.
func (e *NotIgnoredError) Action() string {
	return "add .cbctl/ to .gitignore (or set credentialsOutput outside the repository), or pass --force-overwrite-secret to save it anyway"
}

// CheckIgnored returns NotIgnoredError when dir is in a git work tree that does not ignore it.
// force skips the check.
func CheckIgnored(dir string, force bool) error {
	if force {
		return nil
	}
	if _, _, bad := GitWarning(dir); bad {
		return &NotIgnoredError{Dir: dir}
	}
	return nil
}

// EnsureGitignore appends ".cbctl/" to dir/.gitignore when dir is inside a git work tree that does
// not ignore dir/.cbctl/credentials yet, so generated passwords are never committed. It reports
// whether the file was changed. Outside a git work tree (or without git) it does nothing.
func EnsureGitignore(dir string) (bool, error) {
	if _, _, bad := GitWarning(filepath.Join(dir, filepath.FromSlash(DefaultDirName))); !bad {
		return false, nil
	}
	path := filepath.Join(dir, ".gitignore")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	for _, line := range strings.Split(string(old), "\n") {
		if t := strings.TrimSpace(line); t == ".cbctl/" || t == ".cbctl" || t == "/.cbctl/" {
			return false, nil // present but overridden by a later rule; leave the user's file alone
		}
	}
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += ".cbctl/\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // .gitignore is not secret
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
