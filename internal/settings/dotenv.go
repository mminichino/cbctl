package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/joho/godotenv"
)

// LoadDotEnv looks for ".env" in dir, then in each parent up to and including the first
// directory containing go.mod or .git (the project root). The first file found is loaded
// with godotenv semantics but NEVER overrides a variable already set in the process
// environment. Returns the path loaded ("" if none) and the names set.
func LoadDotEnv(startDir string) (path string, names []string, err error) {
	if startDir == "" {
		startDir, err = os.Getwd()
		if err != nil {
			return "", nil, nil
		}
	}
	envPath, err := findDotEnv(startDir)
	if err != nil {
		return "", nil, err
	}
	if envPath == "" {
		return "", nil, nil
	}
	kv, err := godotenv.Read(envPath)
	if err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", envPath, err)
	}
	for k, v := range kv {
		if _, exists := os.LookupEnv(k); exists {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return envPath, names, fmt.Errorf("set %s from .env: %w", k, err)
		}
		names = append(names, k)
	}
	sort.Strings(names)
	return envPath, names, nil
}

func findDotEnv(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
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
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
