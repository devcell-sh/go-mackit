package installer

import (
	"fmt"
	"regexp"
	"strings"
)

// OpenCore Misc.Debug.Target bits (OpenCore Configuration.pdf, Misc/Debug).
const (
	DebugTargetEnable  = 0x01 // enable logging at all
	DebugTargetConsole = 0x02 // print to the on-screen console
	DebugTargetDataHub = 0x04
	DebugTargetSerial  = 0x08 // print to the serial port (QEMU -serial)
	DebugTargetNVRAM   = 0x10 // log to a UEFI variable
	DebugTargetFile    = 0x40 // write opencore-<date>.txt to the ESP
)

// DebugTarget is what mackit sets: OpenCore's own log goes to the screen
// and to the serial port, where QEMU writes it to serial.log for the build
// logger to pick up. The OpenCore image mackit uses ships RELEASE
// binaries, which only log warnings and errors; the kernel's -v console
// (boot-args below) is the bulk of what reaches the log.
const DebugTarget = DebugTargetEnable | DebugTargetConsole | DebugTargetSerial

// DebugDisplayLevel is EDK2 DEBUG_ERROR|DEBUG_WARN|DEBUG_INFO (0x80000042):
// OpenCore's stock config logs errors and warnings only, which hides the
// boot progress lines (kernel loaded, kexts injected) we want to see.
const DebugDisplayLevel = 0x80000042

// PatchOpenCorePlist modifies an OpenCore config.plist XML for an
// unattended, observable boot: sets Timeout, disables ShowPicker, routes
// OpenCore's own log to the screen and the serial port, and adds the -v
// and serial=1 boot-args so the kernel does the same.
//
// ScanPolicy is left as shipped. Setting it to 0 (scan everything) makes
// OpenCore's own ESP the first boot entry, and with the picker hidden
// OpenCore then chainloads itself ("Failed to start image - Already
// started") and hangs on a black screen. The shipped policy already
// covers SATA, APFS, HFS+ and ESP, which is all the install media needs.
func PatchOpenCorePlist(data []byte, timeout int) ([]byte, error) {
	reTimeout := regexp.MustCompile(`(<key>Timeout</key>\s*<integer>)\d+(</integer>)`)
	if !reTimeout.Match(data) {
		return nil, fmt.Errorf("Timeout key not found in config.plist")
	}
	data = reTimeout.ReplaceAll(data, []byte(fmt.Sprintf("${1}%d${2}", timeout)))

	reShowPicker := regexp.MustCompile(`(<key>ShowPicker</key>\s*)<true/>`)
	data = reShowPicker.ReplaceAll(data, []byte("${1}<false/>"))

	data = patchBootArgs(data)
	data = patchDebug(data)

	return data, nil
}

// patchDebug sets Misc.Debug.Target and DisplayLevel and turns on
// Misc.Serial.Init so OpenCore writes its log to the console and the
// serial port. Keys live in their own dicts, so each match is anchored on
// the enclosing dict's key (lazy, so it stops at the first Target after
// Debug, which is the one inside the Debug dict).
func patchDebug(data []byte) []byte {
	reTarget := regexp.MustCompile(`(<key>Debug</key>\s*<dict>[\s\S]*?<key>Target</key>\s*<integer>)\d+(</integer>)`)
	data = reTarget.ReplaceAll(data, []byte(fmt.Sprintf("${1}%d${2}", DebugTarget)))

	reLevel := regexp.MustCompile(`(<key>Debug</key>\s*<dict>[\s\S]*?<key>DisplayLevel</key>\s*<integer>)\d+(</integer>)`)
	data = reLevel.ReplaceAll(data, []byte(fmt.Sprintf("${1}%d${2}", DebugDisplayLevel)))

	reSerialInit := regexp.MustCompile(`(<key>Serial</key>\s*<dict>\s*<key>Init</key>\s*)<false/>`)
	data = reSerialInit.ReplaceAll(data, []byte("${1}<true/>"))
	return data
}

// patchBootArgs ensures boot-args contains -v (verbose) and serial=1
// so macOS kernel messages are sent to QEMU's serial port.
func patchBootArgs(data []byte) []byte {
	re := regexp.MustCompile(`(<key>boot-args</key>\s*<string>)([^<]*)(</string>)`)
	return re.ReplaceAllFunc(data, func(match []byte) []byte {
		subs := re.FindSubmatch(match)
		if len(subs) != 4 {
			return match
		}
		args := string(subs[2])
		if !strings.Contains(args, "-v") {
			args = strings.TrimSpace(args + " -v")
		}
		if !strings.Contains(args, "serial=1") {
			args = strings.TrimSpace(args + " serial=1")
		}
		return []byte(string(subs[1]) + args + string(subs[3]))
	})
}
