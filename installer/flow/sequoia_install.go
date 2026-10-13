package flow

import (
	"image"
	"time"
)

// SequoiaInstall is the unattended install flow from macOS Sequoia
// Recovery. The Sequoia installer UI differs from Catalina:
//
//   - Recovery starts with a language picker, not Utilities directly.
//   - The Utilities window is titled "Recovery" and lists
//     "Reinstall macOS Sequoia" (not "Reinstall macOS Catalina").
//   - The installer introduction says "Install macOS Sequoia".
//   - The installed system's window server picks a different display
//     mode than Catalina (TBD from live screendumps).
//
// This flow is unverified: built from the Catalina flow and the Sequoia
// catalog's best-guess screens. Each step will be tuned against a live
// VM with MACKIT_NO_TEARDOWN=1 and installFrontier.
func SequoiaInstall(sizeGB int) []Step {
	steps := SequoiaEraseDisk(sizeGB)
	steps = append(steps, SequoiaReinstall...)
	return append(steps,
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
			Display: SequoiaInstalledDisplay,
			Timeout: 3 * time.Hour,
		},
	)
}

// SequoiaInstalledDisplay is the display mode an installed Sequoia's
// window server picks on QEMU. TBD from live screendumps; starting with
// the same 1280x800 as Catalina since QEMU's VGA default is the same.
var SequoiaInstalledDisplay = image.Pt(1280, 800)

// SequoiaSelectLanguage handles the language picker that Sequoia Recovery
// may show before the Utilities screen. Some configurations skip it and
// land on Utilities directly, so the step is optional with DoneOn for
// recovery.utilities.
var SequoiaSelectLanguage = []Step{
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

// SequoiaEraseDisk opens Terminal from macOS Utilities, erases the disk,
// and returns to Utilities. Same logic as EraseDisk but preceded by the
// language picker.
func SequoiaEraseDisk(sizeGB int) []Step {
	steps := append([]Step(nil), SequoiaSelectLanguage...)
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

// SequoiaReinstall starts "Reinstall macOS Sequoia" from Recovery and
// clicks through to the target-disk page.
var SequoiaReinstall = []Step{
	{
		Name:     "choose Reinstall macOS",
		Screen:   "recovery.utilities",
		Elements: []string{"text:Reinstall macOS Sequoia"},
		Actions:  []Action{Click("text:Reinstall macOS Sequoia"), Sleep(500 * time.Millisecond), Click("button:Continue")},
	},
	{
		Name:     "installer introduction",
		Elements: []string{"text:Install macOS Sequoia", "button:Continue"},
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
