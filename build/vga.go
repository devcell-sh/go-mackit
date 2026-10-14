package build

// GuestVGA returns the QEMU VGA adapter for a macOS version codename.
// macOS Tahoe (26) dropped the Bochs VGA driver that earlier versions
// used on the q35 default display. vmware-svga (PCI 15ad:0405) works
// because macOS ships a VMware SVGA driver. Older versions work fine
// with the q35 default (empty string).
func GuestVGA(version string) string {
	switch version {
	case "tahoe":
		return "vmware"
	}
	return ""
}
