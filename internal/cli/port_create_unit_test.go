package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/config/capellafile"
	"github.com/mminichino/cbctl/internal/presets"
)

func gp3(storage, iops int) capellafile.Obj {
	return capellafile.Obj{"type": "gp3", "storage": int64(storage), "iops": int64(iops)}
}

func groupObj(cpu, ram, nodes int, disk capellafile.Obj, services ...string) capellafile.Obj {
	return capellafile.Obj{
		"node":       capellafile.Obj{"compute": capellafile.Obj{"cpu": int64(cpu), "ram": int64(ram)}, "disk": disk},
		"numOfNodes": int64(nodes),
		"services":   toAnyList(services),
	}
}

func TestParseGroupSpec_CAP051_AC2_ParseGroupSpec(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want capellafile.Obj
	}{
		{"data:3x8/32", groupObj(8, 32, 3, gp3(50, 3000), "data")},
		{"query,data,index:3x8/32:size=100,iops=5000", groupObj(8, 32, 3, gp3(100, 5000), "data", "index", "query")},
		{"search:2 x 4 / 16:type=io2", groupObj(4, 16, 2,
			capellafile.Obj{"type": "io2", "storage": int64(50), "iops": int64(3000)}, "search")},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := parseGroupSpec(tc.spec, "aws", diskChange{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseGroupSpec(%q) =\n  %v\nwant\n  %v", tc.spec, got, tc.want)
			}
		})
	}
}

// TestParseGroupSpec covers every usage error of parseGroupSpec: each exits 3 and names the spec.
func TestParseGroupSpec(t *testing.T) {
	for _, tc := range []struct{ spec, msg string }{
		{"data:3x8", "expected NODESxCPU/RAM, e.g. 3x8/32"},
		{"data:three x8/32", "expected NODESxCPU/RAM"},
		{"data:3x8/32x", "expected NODESxCPU/RAM"},
		{"data:3*8/32", "expected NODESxCPU/RAM"},
		{"data:3x8/32:size=abc", "size must be a whole number (got 'abc')"},
		{"data:3x8/32:iops=-5", "iops must be a whole number (got '-5')"},
		{"data:3x8/32:speed=1", "unknown disk option 'speed'"},
		{"data:3x8/32:size", "unknown disk option 'size'"},
		{"data:3x8/32:type=", "type= has no value"},
		{"data:3x8/32:auto-expansion=maybe", "auto-expansion is true or false"},
		{"cache:3x8/32", "unknown service 'cache'"},
		{"dat:3x8/32", "unknown service 'dat'; did you mean data?"},
		{":3x8/32", "no services"},
		{"data", "expected SERVICES:NODESxCPU/RAM[:DISK]"},
		{"data:3x8/32:size=1:x", "expected SERVICES:NODESxCPU/RAM[:DISK]"},
		{"data:99999999999999999999x8/32", "node count, CPU and RAM must be whole numbers"},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			_, err := parseGroupSpec(tc.spec, "aws", diskChange{})
			var ce *CLIError
			if !errors.As(err, &ce) || ce.Code != ExitConfigInvalid {
				t.Fatalf("parseGroupSpec(%q) error = %v, want a usage error (exit 3)", tc.spec, err)
			}
			if !strings.Contains(ce.Message, tc.msg) || !strings.HasPrefix(ce.Message, "--group "+tc.spec) {
				t.Errorf("message = %q, want it to start with '--group %s' and contain %q", ce.Message, tc.spec, tc.msg)
			}
			if ce.Action == "" {
				t.Errorf("%q: no action hint", tc.spec)
			}
		})
	}
}

func TestParseGroupSpec_CAP051_AC5_DiskDefaultsPerProvider(t *testing.T) {
	disk := func(spec, provider string, base diskChange) any {
		t.Helper()
		g, err := parseGroupSpec(spec, provider, base)
		if err != nil {
			t.Fatal(err)
		}
		return g["node"].(capellafile.Obj)["disk"]
	}
	for _, tc := range []struct {
		name string
		got  any
		want capellafile.Obj
	}{
		{"gcp", disk("data:3x8/32", "gcp", diskChange{}), capellafile.Obj{"type": "pd-ssd", "storage": int64(50)}},
		{"azure", disk("data:3x8/32", "azure", diskChange{}), capellafile.Obj{"type": "P6"}},
		{"azure auto-expansion", disk("data:3x8/32:auto-expansion=true", "azure", diskChange{}),
			capellafile.Obj{"type": "P6", "autoExpansion": true}},
	} {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s: disk = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	base := diskChange{size: intPtr(100)}
	if got := disk("data:3x8/32", "aws", base).(capellafile.Obj)["storage"]; got != int64(100) {
		t.Errorf("--disk-size 100 inherited: storage = %v", got)
	}
	if got := disk("data:3x8/32:size=200", "aws", base).(capellafile.Obj)["storage"]; got != int64(200) {
		t.Errorf("size=200 wins over --disk-size: storage = %v", got)
	}
}

func TestMatchChoice_CAP051_AC6_ChoiceAcceptsStartOfAnswer(t *testing.T) {
	choices := []string{"one group", "multiple groups", "basic", "developer pro", "enterprise"}
	for answer, want := range map[string]string{
		"multiple": "multiple groups", "m": "multiple groups", "ONE": "one group",
		"multiple groups": "multiple groups", "developer": "developer pro", "e": "enterprise",
	} {
		got, err := matchChoice(choices, answer)
		if err != nil || got != want {
			t.Errorf("matchChoice(%q) = %q, %v; want %q", answer, got, err, want)
		}
	}
}

func TestMatchChoice_CAP051_AC6_ChoiceRefusesAmbiguousOrUnknown(t *testing.T) {
	choices := []string{"small", "single", "multiple groups"}
	for answer, msg := range map[string]string{
		"":     "choose one of small, single, multiple groups",
		"many": "choose one of",
		"s":    "'s' could be small or single", // "s" starts both
	} {
		if _, err := matchChoice(choices, answer); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("matchChoice(%q) error = %v, want %q", answer, err, msg)
		}
	}
}

func diskOf(d *capellaapi.Disk) map[string]any {
	out := map[string]any{"type": d.Type}
	if d.Storage != nil {
		out["storage"] = *d.Storage
	}
	if d.IOPS != nil {
		out["iops"] = *d.IOPS
	}
	return out
}

func TestMakeDisk_CAP060_AC3_IOPSDefault(t *testing.T) {
	for _, tc := range []struct {
		provider, diskType string
		size, iops         int
	}{
		{"aws", "gp3", 50, 3000}, {"aws", "gp3", 99, 3000}, {"aws", "gp3", 100, 4370}, {"aws", "io2", 200, 5740},
		{"aws", "gp3", 500, 9850}, {"aws", "gp3", 999, 15330}, {"aws", "io2", 1000, 16000}, {"aws", "gp3", 4000, 16000},
		{"azure", "Ultra", 64, 3000}, {"azure", "Ultra", 256, 6000}, {"azure", "Ultra", 2048, 16000},
	} {
		got := diskOf(makeDisk(tc.provider, tc.diskType, intPtr(tc.size), nil, nil))
		want := map[string]any{"type": tc.diskType, "storage": tc.size, "iops": tc.iops}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("makeDisk(%s %s %d) = %v, want %v", tc.provider, tc.diskType, tc.size, got, want)
		}
	}
}

func TestMakeDisk_CAP060_AC3_DefaultsPerProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  *capellaapi.Disk
		want map[string]any
	}{
		{"aws default", makeDisk("aws", "", nil, nil, nil), map[string]any{"type": "gp3", "storage": 50, "iops": 3000}},
		{"aws size 300", makeDisk("aws", "", intPtr(300), nil, nil), map[string]any{"type": "gp3", "storage": 300, "iops": 7110}},
		{"given IOPS win", makeDisk("aws", "io2", intPtr(300), intPtr(20000), nil), map[string]any{"type": "io2", "storage": 300, "iops": 20000}},
		{"gcp IOPS follow size", makeDisk("gcp", "", intPtr(100), nil, nil), map[string]any{"type": "pd-ssd", "storage": 100}},
		{"azure set by the type", makeDisk("azure", "P15", nil, nil, nil), map[string]any{"type": "P15"}},
		{"azure smallest Ultra", makeDisk("azure", "Ultra", nil, nil, nil), map[string]any{"type": "Ultra", "storage": 64, "iops": 3000}},
	} {
		if got := diskOf(tc.got); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClusterDefaults_CAP038_AC3_ProviderAndPresets(t *testing.T) {
	if want := map[string]string{"aws": "us-east-1", "gcp": "us-east1", "azure": "eastus"}; !reflect.DeepEqual(regionDefaults, want) {
		t.Errorf("regionDefaults = %v, want %v", regionDefaults, want)
	}
	if got := diskOf(defaultDisk("aws")); !reflect.DeepEqual(got, map[string]any{"type": "gp3", "storage": 50, "iops": 3000}) {
		t.Errorf("aws disk = %v", got)
	}
	if defaultDisk("gcp").Type != "pd-ssd" || defaultDisk("azure").Type != "P6" {
		t.Errorf("gcp/azure disk types = %s/%s", defaultDisk("gcp").Type, defaultDisk("azure").Type)
	}
	p := presets.Presets()
	if got := [3]int{p["small"].NumOfNodes, p["medium"].CPU, p["large"].RAM}; got != [3]int{3, 8, 64} {
		t.Errorf("small nodes, medium cpu, large ram = %v", got)
	}
	if p["dev"].ConfigurationType != "singleNode" || p["dev"].NumOfNodes != 1 {
		t.Errorf("dev = %+v, want a 1-node singleNode preset", p["dev"])
	}
	// A new cluster with no flags gets the aws default region and the small preset.
	if got, err := sizePreset("", false, false); err != nil || got.NumOfNodes != 3 || got.CPU != 4 || got.RAM != 16 {
		t.Errorf("default size = %+v, %v; want small (3 × 4/16)", got, err)
	}
}

func initAnswersFor(preset, provider, region string) *initAnswers {
	return &initAnswers{orgID: "org-1", project: "nova-dev", cluster: "nova-dev-01", provider: provider,
		region: region, networkCIDR: "10.0.0.0/24", preset: preset, nodes: 3, cpu: 4, ram: 16,
		supportPlan: "developer pro", timezone: initTimezone, bucketMemory: 1024, storageBackend: "magma", access: "read-write"}
}

func TestInitPresets_CAP035_AC3_DevPresetAnyProvider(t *testing.T) {
	for _, provider := range []string{"gcp", "azure"} {
		for _, i := range initAnswersFor("dev", provider, "r1").issues() {
			if i.Path == "clusters[0].cloudProvider.type" || strings.HasSuffix(i.Path, "cloudProvider.type") {
				t.Errorf("%s: dev preset refused for the provider: %s", provider, i.Message)
			}
		}
	}
	if found := initAnswersFor("dev", "aws", "us-east-1").issues(); len(found) != 0 {
		t.Errorf("dev on aws: %v", found)
	}
}
