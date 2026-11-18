// Package layout holds the cluster layout rules that both the Capella commands and the
// manifest apply share: default and recommended node disks, and the free-network picker.
package layout

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/presets"
)

func intPtr(n int) *int { return &n }

// DefaultDisk is the disk Capella gives a new service group of each provider.
func DefaultDisk(provider string) *capellaapi.Disk {
	switch provider {
	case "gcp":
		return &capellaapi.Disk{Type: "pd-ssd", Storage: intPtr(50)}
	case "azure":
		return &capellaapi.Disk{Type: "P6"}
	default:
		return &capellaapi.Disk{Type: "gp3", Storage: intPtr(50), IOPS: intPtr(3000)}
	}
}

// FixedSizeDisk reports whether the disk type sets its own size and IOPS (Azure premium disks).
func FixedSizeDisk(provider, diskType string) bool {
	return provider == "azure" && diskType != "Ultra"
}

// MakeDisk builds a group's disk: the provider default with the given values on top. Without
// IOPS, gp3, io2 and Ultra disks get Capella's recommended IOPS for their size.
func MakeDisk(provider, diskType string, size, iops *int, autoExpansion *bool) *capellaapi.Disk {
	disk := DefaultDisk(provider)
	if diskType != "" {
		disk = &capellaapi.Disk{Type: diskType}
		if !FixedSizeDisk(provider, diskType) {
			disk.Storage = intPtr(SmallestDiskSize(provider, diskType))
		}
	}
	if size != nil {
		disk.Storage = intPtr(*size)
	}
	switch {
	case iops != nil:
		disk.IOPS = intPtr(*iops)
	case disk.Storage != nil:
		if rec := RecommendedIOPS(provider, disk.Type, *disk.Storage); rec > 0 {
			disk.IOPS = intPtr(rec)
		} else {
			disk.IOPS = nil
		}
	}
	if autoExpansion != nil {
		disk.AutoExpansion = autoExpansion
	}
	return disk
}

// SmallestDiskSize is the smallest size, in GB, the provider offers for the disk type.
func SmallestDiskSize(provider, diskType string) int {
	d, ok := presets.DiskTypes()[provider][diskType]
	switch {
	case ok && len(d.Sizes) > 0:
		return d.Sizes[0]
	case ok && len(d.Size) > 0:
		return d.Size[0]
	}
	return 50
}

// RecommendedIOPS is Capella's recommended IOPS for a disk of the given type and size; 0 when
// the type has none.
func RecommendedIOPS(provider, diskType string, size int) int {
	if d, ok := presets.DiskTypes()[provider][diskType]; ok {
		return d.DefaultIOPS(size)
	}
	return 0
}

// SuggestCIDR returns the first 10.N.M.0/24, in address order, that overlaps none of used.
func SuggestCIDR(used []string) string {
	var nets []netip.Prefix
	for _, c := range used {
		if p, err := netip.ParsePrefix(strings.TrimSpace(c)); err == nil {
			nets = append(nets, p)
		}
	}
	for n := 0; n < 256; n++ {
		for m := 0; m < 256; m++ {
			cand := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(n), byte(m), 0}), 24)
			if !slices.ContainsFunc(nets, cand.Overlaps) {
				return cand.String()
			}
		}
	}
	return "10.255.255.0/24"
}
