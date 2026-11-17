package port

import (
	"fmt"
	"strings"
)

// override replaces one passage of a ported operator guide that no rule can translate, such as
// capellactl's Python install steps. Old is matched against the rewritten text; a missing Old
// is an error, so a change in capellactl's text is noticed instead of silently left behind.
type override struct {
	Old, New string
}

// userOverrides are the passages of release "user-docs" that cbctl words differently, by file.
var userOverrides = map[string][]override{
	"getting-started.md": {
		{
			Old: "cbctl --version               # cbctl 1.0.0.devN+g… (Management API v4.0) before the first release",
			New: "cbctl --version               # cbctl version X.Y.Z (commit=… date=…)",
		},
		{
			Old: "- Windows: `cbctl capella is not recognized`: this PowerShell window is not activated. Run\n" +
				"  `.venv\\Scripts\\Activate.ps1` in the `cbctl` folder, or type the full path `…\\cbctl\\.venv\\Scripts\\cbctl.exe`.\n" +
				"  `running scripts is disabled on this system`: run `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned` once, or\n" +
				"  use the full path ([Install on Windows](../../README.md#on-windows-powershell)).",
			New: "- Windows: `cbctl is not recognized`: the folder that holds `cbctl.exe` is not on `PATH`. Add it, or type the\n" +
				"  full path to `cbctl.exe` ([Install](../../README.md#install)).",
		},
	},
}

// applyOverrides applies the overrides of file name to text.
func applyOverrides(name, text string) (string, error) {
	for _, o := range userOverrides[name] {
		if !strings.Contains(text, o.Old) {
			return "", fmt.Errorf("port: %s: override no longer matches; update userOverrides for: %.60q", name, o.Old)
		}
		text = strings.Replace(text, o.Old, o.New, 1)
	}
	return text, nil
}
