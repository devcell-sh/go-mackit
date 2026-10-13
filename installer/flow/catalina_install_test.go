package flow

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-textshot"
)

func TestDiskSizeLabel(t *testing.T) {
	require.Equal(t, "*8.6 GB", DiskSizeLabel(8))
	require.Equal(t, "*68.7 GB", DiskSizeLabel(64))
	require.Equal(t, `diskutil eraseDisk APFS "Macintosh HD" $(diskutil list | awk '/\*68.7 GB/{print $NF; exit}')`, EraseDiskCommand(64, TargetVolume))
}

func TestInstallStepsFor(t *testing.T) {
	steps, err := InstallStepsFor("catalina", 64)
	require.NoError(t, err)
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.Name
		if s.Screen != "" {
			require.NotNil(t, Catalina.Lookup(s.Screen), "step %q waits for an unknown screen %q", s.Name, s.Screen)
		}
		for _, re := range s.Reactions {
			require.NotNil(t, Catalina.Lookup(re.Screen), "reaction screen %q", re.Screen)
		}
	}
	require.Equal(t, []string{
		"open utilities menu", "choose terminal", "terminal ready",
		"erase target disk", "disk erased", "quit terminal", "back to utilities",
		"choose Reinstall macOS", "installer introduction", "license", "confirm license", "target disk",
		"select target volume", "install", "installing", "installed system booted",
	}, names)

	seqSteps, err := InstallStepsFor("sequoia", 64)
	require.NoError(t, err)
	require.True(t, len(seqSteps) > 0, "sequoia must have install steps")
	for _, s := range seqSteps {
		if s.Screen != "" {
			require.NotNil(t, Sequoia.Lookup(s.Screen), "sequoia step %q waits for unknown screen %q", s.Name, s.Screen)
		}
	}
}

func TestStepsThrough(t *testing.T) {
	steps := CatalinaInstall(64)

	all, err := StepsThrough(steps, "")
	require.NoError(t, err)
	require.Len(t, all, len(steps))

	first, err := StepsThrough(steps, "open utilities menu")
	require.NoError(t, err)
	require.Len(t, first, 1)

	upTo, err := StepsThrough(steps, "target disk")
	require.NoError(t, err)
	require.Len(t, upTo, 12)
	require.Equal(t, "target disk", upTo[11].Name)

	_, err = StepsThrough(steps, "format disk")
	require.ErrorContains(t, err, `no step named "format disk"`)
	require.ErrorContains(t, err, "open utilities menu, choose terminal")
}

// terminalLines is Recovery's Terminal window just opened, and
// terminalEraseLines the same window after diskutil finished.
var (
	terminalLines = []textshot.TextResult{
		{Text: "Terminal Shell Edit View Window Help", Box: image.Rect(0, 0, 400, 20)},
		{Text: "-bash-3.2#", Box: image.Rect(10, 40, 120, 56)},
	}
	terminalEraseLines = []textshot.TextResult{
		{Text: "Terminal Shell Edit View Window Help", Box: image.Rect(0, 0, 400, 20)},
		{Text: "Started erase on disk2", Box: image.Rect(10, 40, 300, 56)},
		{Text: "Finished erase on disk2", Box: image.Rect(10, 120, 300, 136)},
	}
)

func TestEraseDiskWaitsForTerminalText(t *testing.T) {
	// The UI changes only on input: utilities -> menu open -> Terminal ->
	// (typed command) erase finished -> (cmd-q) utilities.
	withMenu := append(append([]textshot.TextResult{}, utilitiesLines...),
		textshot.TextResult{Box: image.Rect(270, 60, 340, 80), Text: "Terminal"})
	screen := newFakeScreen(utilitiesLines, withMenu, terminalLines, terminalEraseLines, utilitiesLines)
	screen.onInput = true
	in := &fakeInput{screen: screen}
	r := newRunner(screen, in)
	steps := EraseDisk(64)
	for i := range steps {
		steps[i].Timeout = time.Second
	}
	require.NoError(t, r.Run(context.Background(), steps))
	require.Len(t, in.log, 4)
	require.Equal(t, "type:"+EraseDiskCommand(64, TargetVolume)+"\n", in.log[2])
	require.Equal(t, "keys[meta_l q]", in.log[3])
	require.Equal(t, 2, screen.screenCalls, "only the two menu clicks ran the element detector; the text wait did not")
}

func TestStepTextWaitsWithoutDetector(t *testing.T) {
	screen := newFakeScreen(terminalLines, terminalEraseLines)
	r := newRunner(screen, &fakeInput{})
	m, err := r.waitFor(context.Background(), "recovery.terminal", nil, []string{"finished erase"}, time.Second, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, "recovery.terminal", m.Name())
	require.Equal(t, 0, screen.screenCalls)
	require.Equal(t, 2, screen.textCalls)
}
