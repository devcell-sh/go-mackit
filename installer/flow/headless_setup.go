package flow

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var userNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)

// HeadlessSetupScript is the Recovery Terminal one-liner that finishes a
// fresh Catalina install on the TargetVolume without Setup Assistant:
//
//   - creates an admin user in the installed system's local directory
//     node (dscl against the Data volume's dslocal, the way Recovery's
//     own password reset does it), with a home copied from the User
//     Template and owned by uid 501;
//   - gives the user passwordless sudo (a sudoers.d drop-in on the Data
//     volume), which build hooks and the final shutdown rely on;
//   - marks Setup Assistant done (.AppleSetupDone on the Data volume);
//   - enables Remote Login by clearing sshd's launchd disable override.
//
// It ends by echoing HeadlessSetupDone so a step can wait for it. The
// password is typed in clear; see buildopts.DefaultPassword.
func HeadlessSetupScript(user, password string) (string, error) {
	if !userNameRe.MatchString(user) {
		return "", fmt.Errorf("user %q: lowercase letters, digits, _ and -, 1-31 chars", user)
	}
	if password == "" || strings.ContainsAny(password, "\"'\\$`\n") {
		return "", fmt.Errorf("password must be non-empty and free of quotes, backslashes, $ and backticks")
	}
	return strings.Join([]string{
		`S="/Volumes/` + TargetVolume + `"`,
		`D="$S - Data"`,
		`d(){ dscl -f "$D/private/var/db/dslocal/nodes/Default" localonly "$@"; }`,
		`U=/Local/Target/Users/` + user,
		`d -create $U`,
		`d -create $U UserShell /bin/zsh`,
		`d -create $U RealName ` + user,
		`d -create $U UniqueID 501`,
		`d -create $U PrimaryGroupID 20`,
		`d -create $U NFSHomeDirectory /Users/` + user,
		`d -create $U GeneratedUID $(uuidgen)`,
		`d -passwd $U "` + password + `"`,
		`d -append /Local/Target/Groups/admin GroupMembership ` + user,
		`H="$D/Users/` + user + `"`,
		`ditto "$S/Library/User Template/Non_localized" "$H"`,
		`ditto "$S/Library/User Template/English.lproj" "$H"`,
		`chown -R 501:20 "$H"`,
		`mkdir -p "$D/private/etc/sudoers.d"`,
		`echo "` + user + ` ALL=(ALL) NOPASSWD: ALL" > "$D/private/etc/sudoers.d/` + user + `"`,
		`chmod 440 "$D/private/etc/sudoers.d/` + user + `"`,
		`touch "$D/private/var/db/.AppleSetupDone"`,
		`defaults write "$D/private/var/db/com.apple.xpc.launchd/disabled.plist" com.openssh.sshd -bool false`,
		// Prints HeadlessSetupDone; the backslash keeps the echoed command
		// line from reading as the marker before the script has run.
		`echo headless-setup-d\one`,
	}, "; "), nil
}

// HeadlessSetupDone is the line HeadlessSetupScript prints last.
const HeadlessSetupDone = "headless-setup-done"

// HeadlessSetup returns the Recovery steps that run HeadlessSetupScript
// after a completed install and shut the VM down: open Terminal from
// macOS Utilities, type the script, wait for its last line, shut down.
// The build then boots the installed disk alone and waits for SSH.
//
// Verified live on 2026-10-10 against the installed disk of run
// 20261010T121516, from the Recovery of a restarted QEMU with the
// install media attached (which is where such a VM boots, see
// CatalinaInstall): both "Macintosh HD" and "Macintosh HD - Data" are
// auto-mounted under /Volumes; Recovery has dscl, defaults, ditto, chown,
// uuidgen and PlistBuddy but no which, openssl, python or sysadminctl;
// the User Template lives at "/Volumes/Macintosh HD/Library/User
// Template"; dscl -passwd writes a SALTED-SHA512-PBKDF2 ShadowHash;
// "shutdown -h now" exits QEMU within about a minute. Typing over QMP
// runs at roughly seven characters per second, so the script takes
// about two minutes to enter. sshd's launchd label on Catalina is
// com.openssh.sshd (read from /System/Library/LaunchDaemons/ssh.plist);
// an override under com.apple.openssh.sshd is silently ignored and port
// 22 answers RST. The installed system only got an address with a
// vmxnet3 NIC: on virtio-net-pci it sent DHCP discovers from a random MAC
// and never saw slirp's offers, although Recovery downloads the installer
// over the same device.
func HeadlessSetup(user, password string) ([]Step, error) {
	script, err := HeadlessSetupScript(user, password)
	if err != nil {
		return nil, err
	}
	steps := append([]Step(nil), OpenTerminal...)
	return append(steps,
		Step{
			Name:    "headless setup",
			Screen:  "recovery.terminal",
			Actions: []Action{Type(script + "\n")},
		},
		Step{
			Name:    "headless setup done",
			Screen:  "recovery.terminal",
			Text:    []string{HeadlessSetupDone},
			Timeout: 5 * time.Minute,
		},
		Step{
			Name:    "shut down",
			Screen:  "recovery.terminal",
			Actions: []Action{Type("shutdown -h now\n")},
		},
	), nil
}
