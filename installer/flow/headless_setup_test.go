package flow

import (
	"strings"
	"testing"
)

func TestHeadlessSetupScript(t *testing.T) {
	s, err := HeadlessSetupScript("admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`dscl -f "$D/private/var/db/dslocal/nodes/Default" localonly`,
		`U=/Local/Target/Users/admin; d -create $U;`,
		`-passwd $U "admin"`,
		`-append /Local/Target/Groups/admin GroupMembership admin`,
		`touch "$D/private/var/db/.AppleSetupDone"`,
		`com.openssh.sshd -bool false`,
		`echo "admin ALL=(ALL) NOPASSWD: ALL" > "$D/private/etc/sudoers.d/admin"`,
		`echo headless-setup-d\one`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, HeadlessSetupDone) {
		t.Errorf("script must not contain the done marker verbatim (it would match while typed):\n%s", s)
	}
	if strings.Contains(s, "\n") {
		t.Error("script must be a single line")
	}
	for _, bad := range [][2]string{{"Admin", "x"}, {"a b", "x"}, {"", "x"}, {"admin", ""}, {"admin", `pa"ss`}, {"admin", "pa$s"}} {
		if _, err := HeadlessSetupScript(bad[0], bad[1]); err == nil {
			t.Errorf("user %q password %q: want error", bad[0], bad[1])
		}
	}
}

func TestHeadlessSetupSteps(t *testing.T) {
	steps, err := HeadlessSetup("admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
	}
	want := "open utilities menu,choose terminal,terminal ready,headless setup,headless setup done,shut down"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("steps %s, want %s", got, want)
	}
	done := steps[len(steps)-2]
	if done.Screen != "recovery.terminal" || len(done.Text) != 1 || done.Text[0] != HeadlessSetupDone {
		t.Errorf("done step waits for %q on %q", done.Text, done.Screen)
	}
}
