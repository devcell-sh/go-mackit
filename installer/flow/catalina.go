package flow

// Catalina is the macOS 10.15 Recovery screen catalog. The Verified
// screens were taken from 1920x1080 screendumps of a mackit recovery build
// (test/testdata/screens/catalina).
var Catalina = &Catalog{
	Version: "catalina",
	Screens: []Screen{
		terminalScreen,
		{
			Name:     "recovery.utilities",
			All:      []string{"macos utilities"},
			Any:      []string{"reinstall macos", "disk utility", "time machine"},
			Verified: true,
			Elements: []Element{
				// Menu bar line reads "macOS Utilities File Edit Utilities Window":
				// the second "utilities" is the menu.
				{Name: "utilities_menu", Text: "utilities", Nth: 1},
				{Name: "terminal_item", Text: "terminal"},
				{Name: "restore", Text: "restore from time machine"},
				{Name: "reinstall", Text: "reinstall macos"},
				{Name: "disk_utility", Text: "disk utility"},
				{Name: "continue", Text: "continue"},
			},
		},
		{
			Name:     "recovery.loading",
			All:      []string{"macos recovery"},
			Any:      []string{"examining volumes", "starting", "language chooser"},
			Verified: true,
			Elements: []Element{
				{Name: "restart", Text: "restart"},
				{Name: "next", Text: "next"},
			},
		},
		{
			Name: "recovery.language",
			All:  []string{"language chooser"},
			Any:  []string{"english", "deutsch", "espanol", "francais"},
			Elements: []Element{
				{Name: "english", Text: "english"},
				{Name: "next", Text: "next"},
			},
		},
		{
			// License sheet on top of the license page: both say "agree", the
			// sheet adds the confirmation sentence.
			Name:     "installer.license_confirm",
			All:      []string{"i have read and agree"},
			Verified: true,
		},
		{
			Name:     "installer.license",
			All:      []string{"software license agreement"},
			Any:      []string{"agree to the terms", "please read this software license"},
			Verified: true,
		},
		{
			Name:     "installer.intro",
			All:      []string{"install macos catalina"},
			Any:      []string{"click continue", "to set up the installation"},
			Verified: true,
		},
		{
			Name:     "installer.target",
			All:      []string{"select the disk where you want to install"},
			Verified: true,
			Elements: []Element{
				{Name: "install", Text: "install"},
				{Name: "back", Text: "back"},
			},
		},
		{
			Name: "installer.progress",
			Any:  []string{"installing macos", "minutes remaining", "about remaining", "time remaining"},
		},
		{
			Name: "setup.welcome",
			Any:  []string{"select your country", "welcome", "country or region"},
		},
		pickerScreen,
		bootVerboseScreen,
	},
}
