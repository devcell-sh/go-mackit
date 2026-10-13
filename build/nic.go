package build

import "github.com/devcell-sh/go-mackit/vm/qemu"

// GuestNIC is the QEMU network device model for a macOS version codename
// ("catalina", "sequoia"; "" for unknown media). macOS 11 and later drive
// virtio-net-pci natively; 10.15 and earlier get vmxnet3, because an
// installed Catalina on virtio-net-pci never received slirp's DHCP offers
// (see qemu.DefaultNIC). Builds and mackit start share this so the
// installed system has the device it was provisioned with.
func GuestNIC(version string) string {
	switch version {
	case "catalina", "mojave", "highsierra":
		return qemu.NICCatalina
	}
	return qemu.DefaultNIC
}
