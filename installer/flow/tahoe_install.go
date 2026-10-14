package flow

import (
	"image"
	"time"
)

// TahoeInstall is the unattended install flow from macOS Tahoe Recovery
// through Setup Assistant to the desktop. Unlike Catalina and Sequoia,
// this flow does NOT use HeadlessSetup: it drives through Setup Assistant
// to create the user, then enables Remote Login from the desktop.
//
// This flow is unverified: built from the Sequoia flow with Setup
// Assistant screens added as placeholders. Each step will be tuned
// against a live VM with MACKIT_NO_TEARDOWN=1 and tahoeFrontier.
func TahoeInstall(sizeGB int) []Step {
	steps := TahoeEraseDisk(sizeGB)
	steps = append(steps, TahoeReinstall...)
	steps = append(steps,
		Step{
			Name:     "select target volume",
			Screen:   "installer.target",
			Elements: []string{"text:" + TargetVolume},
			Timeout:  time.Minute,
			Actions:  []Action{Click("text:" + TargetVolume)},
		},
		Step{
			Name:     "install",
			Screen:   "installer.target",
			Elements: []string{"text:Install"},
			Timeout:  time.Minute,
			Actions:  []Action{Click("text:Install")},
		},
		Step{
			Name:    "installing",
			Screen:  "installer.progress",
			Timeout: 30 * time.Minute,
		},
		Step{
			Name:    "installed system booted",
			Display: TahoeInstalledDisplay,
			Timeout: 3 * time.Hour,
		},
	)
	// Setup Assistant steps follow. Each will be filled during
	// interactive learning against a live VM. The screens and actions
	// below are best guesses from known macOS Setup Assistant flow.
	steps = append(steps, TahoeSetupAssistant...)
	return steps
}

// TahoeInstalledDisplay is the display mode an installed Tahoe's window
// server picks on QEMU. TBD from live screendumps; starting with the
// same 1280x800 as Catalina/Sequoia.
var TahoeInstalledDisplay = image.Pt(1280, 800)

// TahoeSelectLanguage handles the language picker that modern Recovery
// may show before the Utilities screen.
var TahoeSelectLanguage = []Step{
	{
		Name:     "select language",
		Screen:   "recovery.language",
		Elements: []string{"text:English"},
		Timeout:  2 * time.Minute,
		Optional: true,
		DoneOn:   []string{"recovery.utilities"},
		Actions:  []Action{Click("text:English")},
	},
}

// TahoeEraseDisk opens Terminal from macOS Utilities, erases the disk,
// and returns to Utilities. Same logic as SequoiaEraseDisk.
func TahoeEraseDisk(sizeGB int) []Step {
	steps := append([]Step(nil), TahoeSelectLanguage...)
	steps = append(steps, OpenTerminal...)
	return append(steps,
		Step{
			Name:    "erase target disk",
			Screen:  "recovery.terminal",
			Actions: []Action{Type(EraseDiskCommand(sizeGB, TargetVolume) + "\n")},
		},
		Step{
			Name:    "disk erased",
			Screen:  "recovery.terminal",
			Text:    []string{"finished erase"},
			Timeout: 3 * time.Minute,
		},
		Step{
			Name:    "quit terminal",
			Screen:  "recovery.terminal",
			Actions: []Action{Chord("meta_l", "q")},
		},
		Step{
			Name:    "back to utilities",
			Screen:  "recovery.utilities",
			Timeout: time.Minute,
		},
	)
}

// TahoeReinstall starts "Reinstall macOS Tahoe" from Recovery and clicks
// through to the target-disk page.
var TahoeReinstall = []Step{
	{
		Name:     "choose Reinstall macOS",
		Screen:   "recovery.utilities",
		Elements: []string{"text:Reinstall macOS Tahoe"},
		Actions:  []Action{Click("text:Reinstall macOS Tahoe"), Sleep(500 * time.Millisecond), Click("button:Continue")},
	},
	{
		Name:     "installer introduction",
		Elements: []string{"text:Install macOS Tahoe", "button:Continue"},
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
		Name:    "target disk",
		Screen:  "installer.target",
		Text:    []string{"select the disk where you want to install"},
		Timeout: 2 * time.Minute,
	},
}

// TahoeSetupAssistant drives through macOS Setup Assistant after the
// installed system boots. These are placeholder steps to be refined
// during interactive learning. Each step's Screen, Elements, and Actions
// will be updated from real screendumps and OCR readings.
var TahoeSetupAssistant = []Step{
	{
		Name:    "setup country",
		Screen:  "setup.welcome",
		Timeout: 5 * time.Minute,
		Actions: []Action{
			// Select country and continue. Exact action TBD from live VM.
		},
	},
	{
		Name:     "setup keyboard",
		Screen:   "setup.keyboard",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:     "setup network",
		Screen:   "setup.network",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:     "setup apple id",
		Screen:   "setup.appleid",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:     "setup terms",
		Screen:   "setup.terms",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:    "setup account",
		Screen:  "setup.account",
		Timeout: 2 * time.Minute,
		Actions: []Action{},
	},
	{
		Name:     "setup analytics",
		Screen:   "setup.analytics",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:     "setup screen time",
		Screen:   "setup.screentime",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:     "setup appearance",
		Screen:   "setup.appearance",
		Optional: true,
		Timeout:  2 * time.Minute,
		Actions:  []Action{},
	},
	{
		Name:    "desktop",
		Screen:  "desktop",
		Timeout: 5 * time.Minute,
	},
}
