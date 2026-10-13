package flow

import (
	"fmt"
	"image"
	"strings"
	"time"
)

// TargetVolume is the APFS volume name the install flows create.
const TargetVolume = "Macintosh HD"

// DiskSizeLabel is how `diskutil list` prints the size of a disk created
// with sizeGB gibibytes: decimal gigabytes to one decimal, so an 8 GiB
// qcow2 shows as "*8.6 GB" and 64 GiB as "*68.7 GB". The install flows use
// it to pick the target disk among the OpenCore and installer disks.
func DiskSizeLabel(sizeGB int) string {
	return fmt.Sprintf("*%.1f GB", float64(sizeGB)*1.073741824)
}

// EraseDiskCommand is the Terminal one-liner that formats the target disk
// as a single APFS volume. Typing it beats clicking through Disk Utility:
// one screen, one command, and the result is a line of text to wait for.
func EraseDiskCommand(sizeGB int, volume string) string {
	return fmt.Sprintf(`diskutil eraseDisk APFS "%s" $(diskutil list | awk '/\%s/{print $NF; exit}')`, volume, DiskSizeLabel(sizeGB))
}

// EraseDisk opens Terminal from macOS Utilities, erases the disk of the
// given size as an APFS volume named TargetVolume, waits for diskutil to
// report the erase finished, and quits Terminal back to Utilities.
func EraseDisk(sizeGB int) []Step {
	steps := append([]Step(nil), OpenTerminal...)
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

// CatalinaInstall is the unattended install from macOS Recovery for a
// target disk of sizeGB: erase it from Terminal, start Reinstall macOS,
// pick the new volume, and wait through the installer's download,
// install, and reboots until Setup Assistant's first screen. Setup
// Assistant itself is the next flow to add; until then the build waits
// for SSH after these steps.
//
// Verified live on Catalina Recovery (1920x1080) on 2026-10-10, driving
// the same QMP input the runner uses; the coordinates are what the
// selectors resolved to, for reference, not what the steps click:
//
//   - Reinstall macOS without an erased disk reaches the target page with
//     only the greyed "macOS Base System" on it and Install disabled, so
//     the erase has to come first. After the erase the page lists
//     "Macintosh HD, 68.51 GB total" next to the Base System.
//   - Cmd-Q in the installer returns to macOS Utilities with no dialog,
//     the Reinstall row still selected.
//   - Utilities: "Reinstall macOS" row at (804,353); Continue (1201,564)
//     is disabled until a row is selected.
//   - Intro: Continue at (960,722) is not the default button; Enter does
//     nothing there, it has to be clicked. The license page follows after
//     up to two minutes under TCG.
//   - License: Agree (1005,722); its confirmation sheet: Agree (1110,255).
//   - Target: "Macintosh HD" at (880,618); Install (1005,722) is disabled
//     until the volume is clicked, enabled right after.
//   - Install: within a minute the page reads "macOS Catalina will be
//     installed on the disk "Macintosh HD"" and "About 19 minutes
//     remaining" (installer.progress).
//   - First reboot after that bar completes. OpenCore boots the installed
//     volume's "macOS Install Data" on its own (no picker, verbose console
//     still on); the second kernel is the installed build (xnu-6153.141.2.2
//     against Recovery's 141.2). The screen is then the Apple logo and a
//     bar with no text for about ten minutes, then "About N minutes
//     remaining..." (installer.progress again), before the next reboot.
//   - Third boot is the installed system: verbose console, then the
//     window server at 1280x800 (not Recovery's 1920x1080). In a 3 GB
//     guest under TCG it stayed a grey desktop with a cursor and never
//     drew Setup Assistant (window server alive, CPU idle, no jetsam, an
//     explicit VGA device with 64 MB made no difference). The build does
//     not need it: HeadlessSetup finishes the install from Recovery.
//   - A VM restarted with the install media still attached boots Recovery
//     again: the installer's bless does not survive a new QEMU process,
//     and OpenCore takes the first bootable volume (with the picker
//     shown, "macOS Base System" is first, "Macintosh HD" second). The
//     build relies on this to get back to Recovery for HeadlessSetup;
//     mackit start attaches the installed disk only.
func CatalinaInstall(sizeGB int) []Step {
	steps := EraseDisk(sizeGB)
	steps = append(steps, CatalinaReinstall...)
	return append(steps,
		Step{
			Name:     "select target volume",
			Screen:   "installer.target",
			Elements: []string{"text:" + TargetVolume},
			Timeout:  time.Minute,
			Actions:  []Action{Click("text:" + TargetVolume)},
		},
		Step{
			// Install only resolves once a volume is selected (a greyed-out
			// element never matches), so this waits for the click to land.
			// The macOS installer renders Install as text + icon, not a
			// button widget, so textshot classifies it as KindText.
			Name:     "install",
			Screen:   "installer.target",
			Elements: []string{"text:Install"},
			Timeout:  time.Minute,
			Actions:  []Action{Click("text:Install")},
		},
		Step{
			// Reinstall from Recovery downloads the full installer from
			// Apple first; the progress page covers both download and
			// install.
			Name:    "installing",
			Screen:  "installer.progress",
			Timeout: 30 * time.Minute,
		},
		Step{
			// The installer reboots (twice on Catalina) through OpenCore,
			// which boots Apple's chosen entry with the picker hidden. The
			// installer phases keep Recovery's 1920x1080; the installed
			// system's window server switches QEMU's display to
			// InstalledDisplay, which ends the step (Setup Assistant itself
			// is skipped by HeadlessSetup, so its screens are never read).
			Name:    "installed system booted",
			Display: InstalledDisplay,
			Timeout: 3 * time.Hour,
		},
	)
}

// InstalledDisplay is the display mode an installed Catalina's window
// server picks on QEMU's VGA (Recovery and the installer run at
// 1920x1080, set by OpenCore). Every boot passes through it for a few
// seconds before OpenCore sets its mode, hence the step's hold.
var InstalledDisplay = image.Pt(1280, 800)

// InstallStepsFor returns the unattended install flow for a macOS version
// codename and the target disk size, or an error when none exists yet.
func InstallStepsFor(version string, sizeGB int) ([]Step, error) {
	switch version {
	case "catalina":
		return CatalinaInstall(sizeGB), nil
	case "sequoia":
		return SequoiaInstall(sizeGB), nil
	}
	return nil, fmt.Errorf("no unattended install flow for macOS %q yet (see installer/flow)", version)
}

// StepsThrough returns the steps up to and including the one named
// stop, so a flow can be run one step further at a time against a live
// VM. An empty stop returns all steps; an unknown name is an error.
func StepsThrough(steps []Step, stop string) ([]Step, error) {
	if stop == "" {
		return steps, nil
	}
	for i, s := range steps {
		if s.Name == stop {
			return steps[:i+1], nil
		}
	}
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
	}
	return nil, fmt.Errorf("no step named %q in the flow (steps: %s)", stop, strings.Join(names, ", "))
}
