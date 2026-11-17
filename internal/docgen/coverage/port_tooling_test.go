package coverage

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// backlogDir is docs/backlog of this repository.
func backlogDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "docs", "backlog")
}

func readReport(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(backlogDir(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAPICoverage_ReportsUpToDate: the three API-COVERAGE reports are exactly what the operation
// snapshot and the story statuses generate (cbdev docs coverage --check).
func TestAPICoverage_ReportsUpToDate(t *testing.T) {
	if err := Generate(backlogDir(t), true); err != nil {
		t.Fatal(err)
	}
}

// TestAPICoverage_BreakdownAddsUpOnEveryReport: each report's "Where the spec's operations go"
// table adds up to the spec total, and the table is the same on all three reports.
func TestAPICoverage_BreakdownAddsUpOnEveryReport(t *testing.T) {
	reRow := regexp.MustCompile(`(?m)^\| [^|*][^|]* \| (\d+) \|`)
	reTotal := regexp.MustCompile(`\| \*\*Total in the spec\*\* \| \*\*(\d+)\*\*`)
	var tables []string
	for _, rel := range []string{"release-1.0.0/API-COVERAGE.md", "release-1.5.0/API-COVERAGE.md", "release-2.0.0/API-COVERAGE-APP-SERVICES.md"} {
		text := readReport(t, rel)
		start := strings.Index(text, "**Where the spec's operations go**")
		end := strings.Index(text, "| **Total in the spec** |")
		if start < 0 || end < start {
			t.Fatalf("%s: no breakdown table", rel)
		}
		table := text[start:end]
		sum := 0
		for _, m := range reRow.FindAllStringSubmatch(table, -1) {
			sum += atoi(t, m[1])
		}
		m := reTotal.FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("%s: no spec total", rel)
		}
		if total := atoi(t, m[1]); sum != total || total == 0 {
			t.Errorf("%s: groups add up to %d, spec total %d", rel, sum, total)
		}
		tables = append(tables, table)
	}
	if tables[0] != tables[1] || tables[1] != tables[2] {
		t.Error("the breakdown table differs between the reports")
	}
}

// TestAPICoverage_Release15PlansEveryOperationLeftOutOf10: every operational-database operation
// that is not in 1.0.0 is planned in the 1.5.0 report (custom database roles among them).
func TestAPICoverage_Release15PlansEveryOperationLeftOutOf10(t *testing.T) {
	db := readReport(t, "release-1.0.0/API-COVERAGE.md")
	r15 := readReport(t, "release-1.5.0/API-COVERAGE.md")
	later := regexp.MustCompile(`\| All operational-database operations \| \d+ \| \d+ \| \d+ \| \d+ \| \d+ \| (\d+) \|`).FindStringSubmatch(db)
	planned := regexp.MustCompile(`\| \*\*API operations planned for 1\.5\.0\*\* \| \*\*(\d+)\*\*`).FindStringSubmatch(r15)
	if later == nil || planned == nil {
		t.Fatalf("totals not found: 1.0.0 %v, 1.5.0 %v", later, planned)
	}
	if atoi(t, later[1]) != atoi(t, planned[1]) {
		t.Errorf("%s operations are not in 1.0.0 but %s are planned for 1.5.0", later[1], planned[1])
	}
	if !strings.Contains(r15, "| `cbctl capella db-role create` |") {
		t.Error("1.5.0 report does not plan db-role create")
	}
	if !strings.Contains(db, "](../release-1.5.0/CAP-064-custom-database-roles.md)") {
		t.Error("1.0.0 report does not link CAP-064 for the operations left to 1.5.0")
	}
}

// TestAPICoverage_DatabaseCredentialsSection: the 1.0.0 report has the Database credentials
// section with its five operations, each mapped to its cbctl credential command. (Their status
// follows docs/backlog/release-1.0.0/stories.yaml, where CAP-027/028/029/046 are not yet flipped.)
func TestAPICoverage_DatabaseCredentialsSection(t *testing.T) {
	text := readReport(t, "release-1.0.0/API-COVERAGE.md")
	if !regexp.MustCompile(`### Database credentials — 5 ops \(`).MatchString(text) {
		t.Fatal("no 'Database credentials — 5 ops' section")
	}
	for _, verb := range []string{"list", "get", "create", "update", "delete"} {
		if !strings.Contains(text, "| `cbctl capella credential "+verb+"` |") {
			t.Errorf("no row for cbctl capella credential %s", verb)
		}
	}
}
