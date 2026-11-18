package cli

import "testing"

// UseApplyFakes points the Server apply of the test at an in-memory cluster. A join of the
// node failJoin fails; empty means no failure.
func UseApplyFakes(t *testing.T, failJoin string) {
	t.Helper()
	useApplyFakes(t, &applyFakeProv{failJoin: failJoin}, newApplyFakeData())
}
