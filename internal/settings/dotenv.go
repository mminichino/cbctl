package settings

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/joho/godotenv"
)

// LoadDotEnv looks for ".env" in dir, then in each parent up to and including the first
// directory containing go.mod or .git (the project root). The first file found is read with
// godotenv semantics. Variables already present according to lookup are skipped. This function
// never mutates the process environment — Execute (or a test) applies the returned values via
// ApplyEnv.
func LoadDotEnv(startDir string, lookup func(string) (string, bool)) (path string, values map[string]string, names []string, err error) {
	if lookup == nil {
		lookup = ProcessLookupEnv
	}
	if startDir == "" {
		startDir, err = ProcessGetwd()
		if err != nil {
			return "", nil, nil, nil
		}
	}
	envPath, err := findDotEnv(startDir)
	if err != nil {
		return "", nil, nil, err
	}
	if envPath == "" {
		return "", nil, nil, nil
	}
	kv, err := godotenv.Read(envPath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse %s: %w", envPath, err)
	}
	values = make(map[string]string)
	for k, v := range kv {
		if _, exists := lookup(k); exists {
			continue
		}
		values[k] = v
		names = append(names, k)
	}
	sort.Strings(names)
	return envPath, values, names, nil
}

func findDotEnv(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if st, err := osStat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
		// Stop after checking the project root (dir that contains go.mod or .git).
		isRoot := fileExists(filepath.Join(dir, "go.mod")) || dirExists(filepath.Join(dir, ".git"))
		if isRoot {
			return "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func fileExists(p string) bool {
	st, err := osStat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := osStat(p)
	return err == nil && st.IsDir()
}
