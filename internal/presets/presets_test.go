package presets

import (
	"reflect"
	"testing"
)

func TestPresets(t *testing.T) {
	t.Parallel()
	got := Presets()
	if want := []string{"dev", "small", "medium", "large"}; !reflect.DeepEqual(PresetNames(), want) {
		t.Fatalf("PresetNames() = %v, want %v", PresetNames(), want)
	}
	tests := []struct {
		name      string
		nodes     int
		cpu, ram  int
		config    string
		avail     string
		serviceCt int
	}{
		{"dev", 1, 4, 16, "singleNode", "single", 3},
		{"small", 3, 4, 16, "multiNode", "multi", 3},
		{"medium", 3, 8, 32, "multiNode", "multi", 3},
		{"large", 5, 16, 64, "multiNode", "multi", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := got[tc.name]
			if !ok {
				t.Fatalf("preset %q missing", tc.name)
			}
			if p.NumOfNodes != tc.nodes || p.CPU != tc.cpu || p.RAM != tc.ram ||
				p.ConfigurationType != tc.config || p.Availability != tc.avail || len(p.Services) != tc.serviceCt {
				t.Errorf("preset %q = %+v", tc.name, p)
			}
			if p.Description == "" {
				t.Errorf("preset %q has no description", tc.name)
			}
		})
	}
}

func TestPresetsReturnsCopy(t *testing.T) {
	t.Parallel()
	a := Presets()
	delete(a, "dev")
	a["small"].Services[0] = "mutated"
	b := Presets()
	if _, ok := b["dev"]; !ok {
		t.Fatal("deleting from a returned map affected later calls")
	}
	if b["small"].Services[0] == "mutated" {
		t.Fatal("mutating a returned slice affected later calls")
	}
}

func TestComputeSizes(t *testing.T) {
	t.Parallel()
	got := ComputeSizes()
	for _, provider := range []string{"aws", "gcp", "azure"} {
		c, ok := got[provider]
		if !ok {
			t.Fatalf("provider %q missing", provider)
		}
		if len(c.Sizes) == 0 {
			t.Errorf("%s has no sizes", provider)
		}
		if !reflect.DeepEqual(c.SingleNode, []Size{{2, 8}}) {
			t.Errorf("%s singleNode = %v", provider, c.SingleNode)
		}
	}
	aws := got["aws"]
	tests := []struct {
		name   string
		size   Size
		single bool
		want   bool
	}{
		{"listed size", Size{4, 16}, false, true},
		{"single only size, multi-node", Size{2, 8}, false, false},
		{"single only size, single node", Size{2, 8}, true, true},
		{"unlisted size", Size{3, 7}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := aws.Offered(tc.size, tc.single); got != tc.want {
				t.Errorf("Offered(%v, %v) = %v, want %v", tc.size, tc.single, got, tc.want)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    Size
		wantErr bool
	}{
		{"4/16", Size{4, 16}, false},
		{" 64/512 ", Size{64, 512}, false},
		{"4", Size{}, true},
		{"x/16", Size{}, true},
		{"4/y", Size{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSize(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if err == nil {
				if round, rerr := ParseSize(got.String()); rerr != nil || round != got {
					t.Errorf("String() %q does not round-trip", got.String())
				}
			}
		})
	}
}

func TestDiskTypes(t *testing.T) {
	t.Parallel()
	got := DiskTypes()
	aws := got["aws"]
	if _, ok := aws["gp3"]; !ok {
		t.Fatalf("aws gp3 missing: %v", aws)
	}
	gp3 := aws["gp3"]
	if !reflect.DeepEqual(gp3.Size, []int{50, 16000}) || !reflect.DeepEqual(gp3.IOPS, []int{3000, 16000}) {
		t.Errorf("gp3 = %+v", gp3)
	}
	// io2 shares gp3's defaults through a YAML anchor.
	if !reflect.DeepEqual(aws["io2"].Defaults, gp3.Defaults) || len(gp3.Defaults) != 11 {
		t.Errorf("anchored defaults not shared: %v vs %v", aws["io2"].Defaults, gp3.Defaults)
	}
	if got["gcp"]["pd-ssd"].PerGB != 60 {
		t.Errorf("pd-ssd = %+v", got["gcp"]["pd-ssd"])
	}
	p6 := got["azure"]["P6"]
	if p6.FixedSize != 64 || p6.FixedIOPS != 240 {
		t.Errorf("P6 = %+v", p6)
	}
	ultra := got["azure"]["Ultra"]
	if len(ultra.Sizes) == 0 || len(ultra.IOPSCap) != 4 {
		t.Errorf("Ultra = %+v", ultra)
	}

	tests := []struct {
		name string
		disk DiskType
		size int
		want int
	}{
		{"below first step", gp3, 10, 0},
		{"first step", gp3, 50, 3000},
		{"between steps", gp3, 150, 4370},
		{"above last step", gp3, 5000, 16000},
		{"no defaults", p6, 64, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.disk.DefaultIOPS(tc.size); got != tc.want {
				t.Errorf("DefaultIOPS(%d) = %d, want %d", tc.size, got, tc.want)
			}
		})
	}
}
