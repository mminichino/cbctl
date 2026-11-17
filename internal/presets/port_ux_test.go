package presets

import "testing"

// TestComputeSizes_CAP051_AC9_LoadedFromFile: the node sizes are the ones compute.yaml lists per
// provider (Azure offers 20/160, AWS does not), single-node-only sizes count only for single node
// clusters, and changing the file changes the sizes.
func TestComputeSizes_CAP051_AC9_LoadedFromFile(t *testing.T) {
	t.Parallel()
	sizes := ComputeSizes()
	if len(sizes) != 3 {
		t.Fatalf("providers = %v, want aws, gcp and azure", sizes)
	}
	aws, azure := sizes["aws"], sizes["azure"]
	tests := []struct {
		name   string
		c      Compute
		size   Size
		single bool
		want   bool
	}{
		{"aws 8/32", aws, Size{8, 32}, false, true},
		{"aws 8/30 is not listed", aws, Size{8, 30}, false, false},
		{"aws 2/8 multi-node", aws, Size{2, 8}, false, false},
		{"aws 2/8 single node", aws, Size{2, 8}, true, true},
		{"azure 20/160", azure, Size{20, 160}, false, true},
		{"aws 20/160", aws, Size{20, 160}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.c.Offered(tc.size, tc.single); got != tc.want {
				t.Errorf("Offered(%v, single=%v) = %v, want %v", tc.size, tc.single, got, tc.want)
			}
		})
	}

	changed, err := decodeCompute([]byte("aws:\n  sizes: [8/30]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !changed["aws"].Offered(Size{8, 30}, false) || changed["aws"].Offered(Size{8, 32}, false) {
		t.Errorf("a changed compute.yaml must change the sizes: %+v", changed["aws"])
	}
	if _, err := decodeCompute([]byte("aws:\n  sizes: [eight]\n")); err == nil {
		t.Error("a size that is not cpu/ram must be rejected")
	}
}
