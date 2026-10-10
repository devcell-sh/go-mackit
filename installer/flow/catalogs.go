package flow

import (
	"fmt"
	"time"
)

// OpenTerminal opens Terminal from the Recovery Utilities menu and waits
// for its window. The screen names are shared by all catalogs.
var OpenTerminal = []Step{
	{
		Name:    "open utilities menu",
		Screen:  "recovery.utilities",
		Actions: []Action{Click("utilities_menu"), Sleep(time.Second)},
	},
	{
		Name:     "choose terminal",
		Screen:   "recovery.utilities",
		Elements: []string{"terminal_item"},
		Timeout:  30 * time.Second,
		Actions:  []Action{Click("terminal_item")},
	},
	{
		Name:    "terminal ready",
		Screen:  "recovery.terminal",
		Timeout: 2 * time.Minute,
	},
}

// pickerScreen is the OpenCore boot picker, identical across macOS
// versions (it is OpenCore's text picker, not Apple UI).
var pickerScreen = Screen{
	Name: "opencore.picker",
	Any:  []string{"macos base system", "opencore"},
}

// terminalScreen is Recovery's Terminal. Its menu bar replaces the Recovery
// app's while the Utilities window may stay visible behind it, so catalogs
// list it before the utilities screen.
var terminalScreen = Screen{
	Name: "recovery.terminal",
	All:  []string{"terminal shell"},
	Elements: []Element{
		{Name: "window", Text: "bash"},
	},
}

// bootVerboseScreen is the kernel's verbose console (boot-arg -v, set by
// mackit's OpenCore patch): white kext and IOKit messages on black. It
// replaces the Apple logo while the kernel starts, before Recovery draws
// its UI. Catalogs list it last so a real UI screen always wins.
var bootVerboseScreen = Screen{
	Name: "boot.verbose",
	Any: []string{
		"darwin kernel version", "appleacpiplatform", "ioapic", "applekeystore",
		"iousbhostfamily", "apfs module started", "kext", "pci configuration",
		"ioconsoleusers", "bsd root", "hfs mounted",
	},
}

var catalogs = map[string]*Catalog{
	"catalina": Catalina,
	"sequoia":  Sequoia,
}

// CatalogFor returns the screen catalog for a macOS version codename as
// used in mackit.yaml ("catalina", "sequoia").
func CatalogFor(version string) (*Catalog, error) {
	c, ok := catalogs[version]
	if !ok {
		return nil, fmt.Errorf("no installer screen catalog for macOS %q", version)
	}
	return c, nil
}
