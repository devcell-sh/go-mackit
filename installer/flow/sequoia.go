package flow

// Sequoia is the macOS 15 Recovery screen catalog. Nothing here is
// Verified yet: modern Recovery titles its window "Recovery", lists
// "Reinstall macOS Sequoia" and "Safari", and keeps the Utilities menu with
// Terminal. Confirm against real screendumps and flip Verified.
var Sequoia = &Catalog{
	Version: "sequoia",
	Screens: []Screen{
		terminalScreen,
		{
			Name: "recovery.utilities",
			All:  []string{"reinstall macos sequoia"},
			Any:  []string{"disk utility", "time machine", "safari"},
			Elements: []Element{
				// Menu bar: "Recovery File Edit Utilities Window".
				{Name: "utilities_menu", Text: "utilities"},
				{Name: "terminal_item", Text: "terminal"},
				{Name: "restore", Text: "restore from time machine"},
				{Name: "reinstall", Text: "reinstall macos sequoia"},
				{Name: "safari", Text: "safari"},
				{Name: "disk_utility", Text: "disk utility"},
				{Name: "continue", Text: "continue"},
			},
		},
		{
			Name: "recovery.loading",
			Any:  []string{"macos recovery", "starting macos recovery"},
		},
		{
			Name: "recovery.language",
			Any:  []string{"english", "deutsch", "espanol", "francais"},
			Elements: []Element{
				{Name: "english", Text: "english"},
			},
		},
		{
			Name: "installer.target",
			All:  []string{"install macos sequoia"},
			Any:  []string{"select the disk where you want to install", "will be installed on the disk"},
			Elements: []Element{
				{Name: "install", Text: "install"},
			},
		},
		{
			Name: "installer.progress",
			Any:  []string{"installing macos", "install macos sequoia", "minutes remaining", "about remaining"},
		},
		{
			Name: "setup.welcome",
			Any:  []string{"select your country", "welcome", "country or region"},
		},
		pickerScreen,
		bootVerboseScreen,
	},
}
