package settings

import "os"

// ProcessEnv reads a process environment variable. Callers that need hermetic tests pass their
// own Env func instead; this is the production default used when Inputs.Env is nil.
func ProcessEnv(k string) string { return os.Getenv(k) }

// ProcessLookupEnv reports whether a process environment variable is set.
func ProcessLookupEnv(k string) (string, bool) { return os.LookupEnv(k) }

// ProcessGetwd returns the process working directory.
func ProcessGetwd() (string, error) { return os.Getwd() }

// ApplyEnv sets process environment variables (used by Execute after LoadDotEnv).
func ApplyEnv(values map[string]string) error {
	for k, v := range values {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func osStat(name string) (os.FileInfo, error) { return os.Stat(name) }
