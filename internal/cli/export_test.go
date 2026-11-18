package cli

import (
	"testing"

	"github.com/mminichino/cbctl/internal/capellafake"
	"github.com/mminichino/cbctl/internal/manifest/capellaapply"
)

// UseApplyFakes points the Server apply of the test at an in-memory cluster. A join of the
// node failJoin fails; empty means no failure.
func UseApplyFakes(t *testing.T, failJoin string) {
	t.Helper()
	useApplyFakes(t, &applyFakeProv{failJoin: failJoin}, newApplyFakeData())
}

// UseCapellaApplyFake points the Capella apply of the test at f and returns the environment
// (API key, base URL, organization) the command needs. connect replaces the SDK connection of
// imports; nil keeps the default.
func UseCapellaApplyFake(t *testing.T, f *capellafake.Fake, connect capellaapply.Connector) map[string]string {
	t.Helper()
	old := capellaConnector
	capellaConnector = connect
	t.Cleanup(func() { capellaConnector = old })
	return map[string]string{
		"CAPELLA_API_BASE_URL": f.URL,
		"CAPELLA_API_KEY":      f.APIKey,
		"CAPELLA_ORG_ID":       f.OrgID,
	}
}
