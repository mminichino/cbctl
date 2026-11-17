package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/mminichino/cbctl/internal/redact"
)

// MigrateOptions says what MigrateLegacy copies from capellactl into cbctl.
type MigrateOptions struct {
	// FromDir is capellactl's home (LegacyHome); ToDir is cbctl's (HomeDir).
	FromDir, ToDir string
	// Profiles limits the migration to these names; empty means every capellactl profile.
	Profiles []string
	// DryRun reports what would change and writes nothing.
	DryRun bool
	// Force overwrites cbctl profiles and keys that differ from capellactl's.
	Force bool
	// RemoveLegacy deletes each fully migrated profile and key from capellactl afterwards.
	RemoveLegacy bool
	// Keyring is cbctl's keychain; LegacyKeyring is capellactl's (nil: keys are not copied).
	Keyring, LegacyKeyring Keyring
}

// MigrationReport is the outcome of MigrateLegacy. Every name appears in at most one of
// Imported, Skipped and Conflicts; KeyMissing and Removed refine Imported.
type MigrationReport struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Imported profiles were copied (or would be, with DryRun).
	Imported []string `json:"imported"`
	// Skipped profiles already match in cbctl.
	Skipped []string `json:"skipped"`
	// KeyMissing profiles were imported without their API key: the capellactl keychain could not
	// be read or the cbctl one could not be written.
	KeyMissing []string `json:"keyMissing"`
	// Removed profiles were deleted from capellactl (RemoveLegacy).
	Removed []string `json:"removed"`
	// Conflicts are profiles that differ between the two and were left alone (no Force).
	Conflicts map[string]string `json:"conflicts"`
	// Warnings are non-fatal problems (unknown capellactl keys, cleanup failures).
	Warnings []string `json:"warnings"`
	DryRun   bool     `json:"dryRun"`
}

// Nothing reports that capellactl had nothing to migrate.
func (r *MigrationReport) Nothing() bool {
	return len(r.Imported)+len(r.Skipped)+len(r.Conflicts) == 0
}

// ErrUnknownLegacyProfile is returned when a requested profile is not in capellactl's file.
var ErrUnknownLegacyProfile = errors.New("not a capellactl profile")

// MigrateLegacy copies capellactl's profiles and stored API keys into cbctl. A profile that
// already exists in cbctl with different settings or a different key is a conflict unless Force
// is set; nothing is written for it. Keys are registered for redaction and never returned.
func MigrateLegacy(ctx context.Context, opts MigrateOptions) (*MigrationReport, error) {
	rep := &MigrationReport{From: opts.FromDir, To: opts.ToDir, Conflicts: map[string]string{}, DryRun: opts.DryRun}
	if opts.FromDir == "" || opts.ToDir == "" {
		return nil, fmt.Errorf("migrate: both the capellactl and cbctl homes are required")
	}
	// CAPELLACTL_HOME can make both homes one directory: the profiles are shared, so only the
	// keychain entries are copied and the file is neither rewritten nor pruned.
	sameHome := filepath.Clean(opts.FromDir) == filepath.Clean(opts.ToDir)
	legacy, warnings, err := LoadLegacyProfiles(opts.FromDir)
	if err != nil {
		return nil, err
	}
	rep.Warnings = append(rep.Warnings, warnings...)

	names := opts.Profiles
	if len(names) == 0 {
		for n := range legacy {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := legacy[n]; !ok {
			return nil, fmt.Errorf("profile %q: %w (%s)", n, ErrUnknownLegacyProfile, ProfilesPath(opts.FromDir))
		}
	}

	store := ProfileStore{Path: ProfilesPath(opts.ToDir)}
	current, err := store.Load()
	if err != nil {
		return nil, err
	}
	keys := map[string]string{} // profile → key to write
	for _, n := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lp := legacy[n]
		legacyKey, keyErr := readKey(opts.LegacyKeyring, n)
		curKey, _ := readKey(opts.Keyring, n)
		cur, exists := current[n]

		sameProfile := exists && cur == lp
		sameKey := legacyKey == "" || curKey == legacyKey
		switch {
		case sameProfile && sameKey && keyErr == nil:
			rep.Skipped = append(rep.Skipped, n)
			continue
		case exists && !opts.Force && !sameProfile:
			rep.Conflicts[n] = "cbctl already has this profile with different settings; pass --force to overwrite it"
			continue
		case exists && !opts.Force && curKey != "" && !sameKey:
			rep.Conflicts[n] = "cbctl already stores a different API key for this profile; pass --force to overwrite it"
			continue
		}
		current[n] = lp
		rep.Imported = append(rep.Imported, n)
		switch {
		case keyErr != nil:
			rep.KeyMissing = append(rep.KeyMissing, n)
		case legacyKey != "" && legacyKey != curKey:
			keys[n] = legacyKey
		}
	}

	if opts.DryRun || len(rep.Imported) == 0 {
		return rep, nil
	}
	if !sameHome {
		if err := store.Save(current); err != nil {
			return nil, err
		}
	}
	for _, n := range rep.Imported {
		k, ok := keys[n]
		if !ok {
			continue
		}
		if opts.Keyring == nil {
			rep.KeyMissing = append(rep.KeyMissing, n)
			continue
		}
		if err := opts.Keyring.Set(n, k); err != nil {
			rep.KeyMissing = append(rep.KeyMissing, n)
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("profile %s: cannot store the API key in the cbctl keychain: %v", n, err))
		}
	}
	sort.Strings(rep.KeyMissing)
	if opts.RemoveLegacy {
		removeLegacy(opts, rep, !sameHome)
	}
	return rep, nil
}

// readKey returns the key for profile; a missing entry is "" with no error, an unreadable
// keychain is an error.
func readKey(kr Keyring, profile string) (string, error) {
	if kr == nil {
		return "", nil
	}
	k, err := kr.Get(profile)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	k = SanitizeAPIKey(k)
	redact.Register(k)
	return k, nil
}

// removeLegacy deletes the profiles that migrated completely from capellactl's file and keychain.
func removeLegacy(opts MigrateOptions, rep *MigrationReport, profiles bool) {
	missing := map[string]bool{}
	for _, n := range rep.KeyMissing {
		missing[n] = true
	}
	var done []string
	for _, n := range rep.Imported {
		if missing[n] {
			continue
		}
		if opts.LegacyKeyring != nil {
			if err := opts.LegacyKeyring.Delete(n); err != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("profile %s: kept in capellactl: cannot delete its keychain entry: %v", n, err))
				continue
			}
		}
		done = append(done, n)
	}
	if len(done) == 0 {
		return
	}
	if !profiles {
		rep.Removed = done
		return
	}
	if err := deleteLegacyProfiles(opts.FromDir, done); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("cannot remove migrated profiles from %s: %v", ProfilesPath(opts.FromDir), err))
		return
	}
	rep.Removed = done
}

// deleteLegacyProfiles removes names from capellactl's profiles.yaml, keeping every other key
// of the file as it is (capellactl may know keys cbctl does not).
func deleteLegacyProfiles(dir string, names []string) error {
	path := ProfilesPath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	profiles, _ := doc["profiles"].(map[string]any)
	for _, n := range names {
		delete(profiles, n)
	}
	doc["profiles"] = profiles
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".profiles-*.yaml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op after a successful rename
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close() // the write error is the one reported
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close() // the chmod error is the one reported
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
