package flow

import "time"

// CatalinaReinstall starts "Reinstall macOS" from macOS Utilities and
// clicks through the installer up to the target-disk page. Each step
// reads: wait until the element is on screen, then click it. Verified on a
// live VM (test/testdata/screens/catalina/install-*.png). The sequence
// stops at the disk page because the installer needs a formatted APFS
// volume; formatting through Disk Utility is the next flow to add.
var CatalinaReinstall = []Step{
	{
		Name:     "choose Reinstall macOS",
		Screen:   "recovery.utilities",
		Elements: []string{"text:Reinstall macOS"},
		Actions:  []Action{Click("text:Reinstall macOS"), Sleep(500 * time.Millisecond), Click("button:Continue")},
	},
	{
		Name:     "installer introduction",
		Elements: []string{"text:Install macOS Catalina", "button:Continue"},
		Timeout:  3 * time.Minute,
		Actions:  []Action{Click("button:Continue")},
	},
	{
		Name:     "license",
		Elements: []string{"button:Agree"},
		Timeout:  2 * time.Minute,
		Actions:  []Action{Click("button:Agree")},
	},
	{
		Name:     "confirm license",
		Elements: []string{"text:I have read and agree", "button:Agree"},
		Timeout:  time.Minute,
		Actions:  []Action{Click("button:Agree")},
	},
	{
		// The page itself: Install stays disabled until a volume is
		// clicked (CatalinaInstall's next steps), so this waits for the
		// page text, not for the button.
		Name:    "target disk",
		Screen:  "installer.target",
		Text:    []string{"select the disk where you want to install"},
		Timeout: 2 * time.Minute,
	},
}
