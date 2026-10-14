package flow

// Tahoe is the macOS 26 Recovery and Setup Assistant screen catalog.
// Nothing here is Verified yet: screens are adapted from the Sequoia
// catalog with version-specific text updated. Confirm against real
// screendumps and flip Verified as each screen is validated.
//
// Setup Assistant screens (setup.*) are placeholders to be filled during
// interactive learning against a live VM. Unlike Catalina and Sequoia,
// this flow drives through Setup Assistant instead of bypassing it with
// HeadlessSetup.
var Tahoe = &Catalog{
	Version: "tahoe",
	Screens: []Screen{
		terminalScreen,
		{
			Name: "recovery.utilities",
			All:  []string{"reinstall macos tahoe"},
			Any:  []string{"disk utility", "time machine", "safari"},
			Elements: []Element{
				{Name: "utilities_menu", Text: "utilities"},
				{Name: "terminal_item", Text: "terminal"},
				{Name: "restore", Text: "restore from time machine"},
				{Name: "reinstall", Text: "reinstall macos tahoe"},
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
			All:  []string{"install macos tahoe"},
			Any:  []string{"select the disk where you want to install", "will be installed on the disk"},
			Elements: []Element{
				{Name: "install", Text: "install"},
			},
		},
		{
			Name: "installer.progress",
			Any:  []string{"installing macos", "install macos tahoe", "minutes remaining", "about remaining"},
		},
		// Setup Assistant screens, to be filled during interactive learning.
		{
			Name: "setup.welcome",
			Any:  []string{"select your country", "welcome", "country or region"},
		},
		{
			Name: "setup.keyboard",
			Any:  []string{"select your keyboard", "keyboard layout", "choose a keyboard layout"},
		},
		{
			Name: "setup.network",
			Any:  []string{"how do you connect", "wi-fi", "select a wi-fi network", "local network"},
		},
		{
			Name: "setup.appleid",
			Any:  []string{"sign in with your apple", "apple id", "apple account"},
		},
		{
			Name: "setup.terms",
			Any:  []string{"terms and conditions"},
		},
		{
			Name: "setup.account",
			Any:  []string{"create a computer account", "full name"},
		},
		{
			Name: "setup.analytics",
			Any:  []string{"analytics", "help apple improve", "share crash data"},
		},
		{
			Name: "setup.screentime",
			Any:  []string{"screen time"},
		},
		{
			Name: "setup.appearance",
			Any:  []string{"choose your look", "light", "dark", "appearance"},
		},
		{
			Name: "desktop",
			Any:  []string{"finder", "file edit view"},
		},
		pickerScreen,
		bootVerboseScreen,
	},
}
