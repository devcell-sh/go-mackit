package flow

import (
	"image"
	"testing"

	"github.com/devcell-sh/go-textshot"
	"github.com/stretchr/testify/require"
)

// OCR output recorded from the Sequoia fixtures (go-textshot e2e).
var (
	loadingLines = []textshot.TextResult{
		{Box: image.Rect(0, 0, 190, 27), Text: "Language Chooser", Score: 0.999},
		{Box: image.Rect(806, 160, 1110, 213), Text: "macOS Recovery", Score: 0.999},
		{Box: image.Rect(880, 450, 1040, 476), Text: " Examining volumes...", Score: 0.98},
		{Box: image.Rect(880, 702, 930, 724), Text: "Restart", Score: 0.99},
		{Box: image.Rect(995, 702, 1035, 724), Text: "Next", Score: 0.99},
	}
	utilitiesLines = []textshot.TextResult{
		{Box: image.Rect(0, 0, 390, 22), Text: " macOs Utilities File Edit Utilities Window", Score: 0.97},
		{Box: image.Rect(830, 236, 1086, 282), Text: "macOS Utilities", Score: 0.999},
		{Box: image.Rect(746, 306, 996, 328), Text: "Restore From Time Machine Backup", Score: 0.99},
		{Box: image.Rect(746, 326, 1084, 344), Text: "You have a backup of your system that you want to restore.", Score: 0.98},
		{Box: image.Rect(746, 344, 862, 366), Text: "Reinstall macOS", Score: 0.99},
		{Box: image.Rect(746, 364, 926, 382), Text: "Reinstall a new copy of macOS.", Score: 0.98},
		{Box: image.Rect(746, 420, 826, 442), Text: "Disk Utility", Score: 0.99},
		{Box: image.Rect(746, 440, 974, 458), Text: "Repair or erase a disk using Disk Utility.", Score: 0.98},
		{Box: image.Rect(1160, 555, 1242, 575), Text: "Continue", Score: 0.99},
	}
	hd = image.Rect(0, 0, 1920, 1080)
)

func TestNormalize(t *testing.T) {
	require.Equal(t, "macos utilities", Normalize("  macOs Utilities! "))
	require.Equal(t, "examining volumes", Normalize("Examining volumes..."))
	require.Equal(t, "", Normalize("..."))
}

func TestIdentifySequoiaFixtures(t *testing.T) {
	m, ok := Catalina.Identify(loadingLines, hd)
	require.True(t, ok)
	require.Equal(t, "recovery.loading", m.Name())
	_, found := m.Find("next")
	require.True(t, found)

	m, ok = Catalina.Identify(utilitiesLines, hd)
	require.True(t, ok)
	require.Equal(t, "recovery.utilities", m.Name())
	require.Equal(t, hd, m.Bounds)
	box, found := m.Find("reinstall")
	require.True(t, found)
	require.Equal(t, image.Rect(746, 344, 862, 366), box, "whole line box when the text is the whole line")
	_, hasTerminal := m.Find("terminal_item")
	require.False(t, hasTerminal, "menu is closed, no Terminal item visible")
}

func TestIdentifyUnknownAndEmpty(t *testing.T) {
	_, ok := Catalina.Identify(nil, hd)
	require.False(t, ok)
	m, ok := Catalina.Identify([]textshot.TextResult{{Text: "Guest has not initialized the display (yet)"}}, image.Rect(0, 0, 640, 480))
	require.False(t, ok)
	require.Equal(t, "", m.Name())
}

func TestIdentifyTerminalBeatsUtilitiesWindow(t *testing.T) {
	// Terminal open in front of the still-visible Utilities window.
	lines := append([]textshot.TextResult{
		{Box: image.Rect(0, 0, 420, 22), Text: "Terminal Shell Edit View Window Help"},
		{Box: image.Rect(700, 300, 1100, 322), Text: "Terminal - -bash - 80x24"},
	}, utilitiesLines[1:]...)
	m, ok := Catalina.Identify(lines, hd)
	require.True(t, ok)
	require.Equal(t, "recovery.terminal", m.Name())
}

func TestLocateNthOccurrenceInMenuBar(t *testing.T) {
	m, _ := Catalina.Identify(utilitiesLines, hd)
	menu, ok := m.Find("utilities_menu")
	require.True(t, ok)
	app := subBox(utilitiesLines[0].Box, Normalize(utilitiesLines[0].Text), 6, 15)
	require.Greater(t, menu.Min.X, app.Max.X, "second occurrence is right of the app name")
	require.Less(t, menu.Max.X, 390)
	pt, ok := m.Center("utilities_menu")
	require.True(t, ok)
	require.Equal(t, 11, pt.Y)

	// With the menu open, the Terminal item appears as its own line.
	open := append(append([]textshot.TextResult{}, utilitiesLines...),
		textshot.TextResult{Box: image.Rect(270, 60, 340, 80), Text: "Terminal"})
	m, _ = Catalina.Identify(open, hd)
	require.Equal(t, "recovery.utilities", m.Name())
	item, _ := m.Find("terminal_item")
	require.Equal(t, image.Rect(270, 60, 340, 80), item)
}

func TestFindSelectors(t *testing.T) {
	m, _ := Catalina.Identify(utilitiesLines, hd)
	m.Elements = []textshot.Element{
		{Key: "text:Reinstall macOS", Kind: textshot.KindText, Box: image.Rect(745, 343, 863, 364), Text: "Reinstall macOS"},
		{Key: "text:Reinstall a new copy of macOS.", Kind: textshot.KindText, Box: image.Rect(745, 363, 927, 381), Text: "Reinstall a new copy of macOS."},
		{Key: "button:Continue", Kind: textshot.KindButton, Box: image.Rect(1160, 554, 1243, 575), Text: "Continue"},
		{Key: "icon@262,3", Kind: textshot.KindIcon, Box: image.Rect(262, 3, 311, 20)},
	}
	box, ok := m.Find("text:Reinstall macOS")
	require.True(t, ok)
	require.Equal(t, image.Rect(745, 343, 863, 364), box, "exact match beats the substring match in the description")
	box, ok = m.Find("text:reinstall")
	require.True(t, ok)
	require.Equal(t, image.Rect(745, 343, 863, 364), box, "substring: first in reading order")
	box, ok = m.Find("button:Continue")
	require.True(t, ok)
	require.Equal(t, image.Rect(1160, 554, 1243, 575), box)
	_, ok = m.Find("button:Reinstall macOS")
	require.False(t, ok, "text lines are not buttons")
	box, ok = m.Find("text:Continue")
	require.True(t, ok, "text: also matches labelled buttons")
	box, ok = m.Find("icon@262,3")
	require.True(t, ok)
	require.Equal(t, image.Rect(262, 3, 311, 20), box)
	_, ok = m.Find("icon@1,1")
	require.False(t, ok)
	_, ok = m.Find("nope")
	require.False(t, ok)

	// Catalog element inside the menu-bar line snaps to the detected label.
	box, ok = m.Find("utilities_menu")
	require.True(t, ok)
	require.Equal(t, image.Rect(262, 3, 311, 20), box)
	pt, _ := m.Center("utilities_menu")
	require.Equal(t, image.Pt(286, 11), pt)

	// Without elements, text selectors fall back to OCR lines.
	m.Elements = nil
	box, ok = m.Find("text:Disk Utility")
	require.True(t, ok)
	require.Equal(t, image.Rect(746, 420, 826, 442), box)
}

func TestNthIndexAndSubBox(t *testing.T) {
	require.Equal(t, 0, nthIndex("a b a", "a", 0))
	require.Equal(t, 4, nthIndex("a b a", "a", 1))
	require.Equal(t, -1, nthIndex("a b a", "a", 2))
	require.Equal(t, -1, nthIndex("abc", "x", 0))
	box := image.Rect(100, 0, 200, 10)
	require.Equal(t, box, subBox(box, "abcd", 0, 4))
	require.Equal(t, image.Rect(150, 0, 200, 10), subBox(box, "abcd", 2, 4))
	require.Equal(t, image.Rect(100, 0, 101, 10), subBox(box, "a", 0, 0), "never empty")
}

func TestCatalogFor(t *testing.T) {
	c, err := CatalogFor("catalina")
	require.NoError(t, err)
	require.Same(t, Catalina, c)
	c, err = CatalogFor("sequoia")
	require.NoError(t, err)
	require.Same(t, Sequoia, c)
	_, err = CatalogFor("mojave")
	require.Error(t, err)

	for _, cat := range []*Catalog{Catalina, Sequoia} {
		seen := map[string]bool{}
		for _, s := range cat.Screens {
			require.False(t, seen[s.Name], "%s: duplicate screen %s", cat.Version, s.Name)
			seen[s.Name] = true
			require.True(t, len(s.All) > 0 || len(s.Any) > 0, "%s: screen %s has no phrases", cat.Version, s.Name)
		}
		// Every screen OpenTerminal needs, with its elements, exists in every catalog.
		for _, st := range OpenTerminal {
			sc := cat.Lookup(st.Screen)
			require.NotNil(t, sc, "%s: OpenTerminal needs screen %s", cat.Version, st.Screen)
			for _, a := range st.Actions {
				if a.Click == "" {
					continue
				}
				found := false
				for _, e := range sc.Elements {
					found = found || e.Name == a.Click
				}
				require.True(t, found, "%s: screen %s lacks element %s", cat.Version, st.Screen, a.Click)
			}
		}
	}
	require.Nil(t, Catalina.Lookup("nope"))
}

// OCR output recorded from the Catalina installer fixtures.
var (
	introLines = []textshot.TextResult{
		{Box: image.Rect(20, 1, 325, 22), Text: "Install macOS Catalina Edit Window"},
		{Box: image.Rect(890, 134, 1030, 152), Text: "Install macOS Catalina"},
		{Box: image.Rect(820, 560, 1098, 602), Text: "macOS Catalina"},
		{Box: image.Rect(765, 611, 1154, 632), Text: "To set up the installation of macOS Catalina, click Continue."},
		{Box: image.Rect(930, 713, 990, 731), Text: "Continue"},
	}
	licenseLines = []textshot.TextResult{
		{Text: "Install macOS Catalina"},
		{Text: "macOS Catalina"},
		{Text: "To continue installing the software, you must agree to the terms of the software license agreement."},
		{Text: "SOFTWARE LICENSE AGREEMENT FOR macOS Catalina"},
		{Text: "Agree"},
	}
	targetLines = []textshot.TextResult{
		{Text: "Install macOS Catalina Edit Window"},
		{Text: "Install macOS Catalina"},
		{Text: "macOS Catalina"},
		{Text: "Select the disk where you want to install macOS."},
		{Text: "macOS Base System"},
		{Text: "2.01 GB total"},
		{Text: "Back"},
		{Text: "Install"},
	}
	// targetSelectedLines is the same page once Macintosh HD was clicked:
	// the headline changes, Install is enabled.
	targetSelectedLines = []textshot.TextResult{
		{Text: "Install macOS Catalina Edit Window"},
		{Text: "Install macOS Catalina"},
		{Text: "macOS Catalina"},
		{Text: "macOS Catalina will be installed on the disk \"Macintosh HD\"."},
		{Text: "Macintosh HD"},
		{Text: "68.51 GB total"},
		{Text: "macOS Base System"},
		{Text: "Back"},
		{Text: "Install"},
	}
	// progressPhase1Lines is the page right after Install, progressPhase2Lines
	// the Apple logo after the first reboot.
	progressPhase1Lines = []textshot.TextResult{
		{Text: "Install macOS Catalina Edit Window"},
		{Text: "Install macOS Catalina"},
		{Text: "macOS Catalina will be installed on the disk \"Macintosh HD\"."},
		{Text: "Macintosh HD"},
		{Text: "About 19 minutes remaining"},
		{Text: "Cancel"},
	}
	progressPhase2Lines = []textshot.TextResult{{Text: "About a minute remaining..."}}
	licenseConfirmLines = append([]textshot.TextResult{
		{Text: "I have read and agree to the terms of the"},
		{Text: "software license agreement."},
		{Text: "Disagree"},
		{Text: "Agree"},
	}, licenseLines...)
)

func TestIdentifyCatalinaInstallerScreens(t *testing.T) {
	cases := map[string][]textshot.TextResult{
		"installer.intro":           introLines,
		"installer.license":         licenseLines,
		"installer.license_confirm": licenseConfirmLines,
		"installer.target":          targetLines,
	}
	// Distinct lines, not a map, where the same line set must not be
	// mistaken for a neighbour.
	for _, c := range []struct {
		want  string
		lines []textshot.TextResult
	}{
		{"installer.target", targetSelectedLines},
		{"installer.progress", progressPhase1Lines},
		{"installer.progress", progressPhase2Lines},
	} {
		m, ok := Catalina.Identify(c.lines, hd)
		require.True(t, ok, c.want)
		require.Equal(t, c.want, m.Name())
	}
	for want, lines := range cases {
		m, ok := Catalina.Identify(lines, hd)
		require.True(t, ok, want)
		require.Equal(t, want, m.Name())
	}
}

func TestFindButtonMergedWithGlyph(t *testing.T) {
	// The detector merges the arrow glyph and its label: "Continue ☑".
	m, _ := Catalina.Identify(introLines, hd)
	m.Elements = []textshot.Element{
		{Key: "button:Continue ☑", Kind: textshot.KindButton, Box: image.Rect(930, 682, 990, 731), Text: "Continue ☑"},
	}
	box, ok := m.Find("button:Continue")
	require.True(t, ok)
	require.Equal(t, image.Rect(930, 682, 990, 731), box)
}

// verboseLines are OCR lines from the kernel's -v console: fragments of
// kext and IOKit chatter as PP-OCR reads them from the text console.
var verboseLines = []textshot.TextResult{
	{Text: "AppleACPIPlatform: Found 6 CPUs", Box: image.Rect(0, 0, 400, 16)},
	{Text: "IOAPIC: Version 0x20 Vectors 64:87", Box: image.Rect(0, 16, 400, 32)},
	{Text: "pci (build 14:01:23 Jul 2020), flags 0x63008", Box: image.Rect(0, 32, 500, 48)},
	{Text: "Kext com.apple.driver.AppleIntelCPUPowerManagement", Box: image.Rect(0, 48, 600, 64)},
}

func TestIdentify_BootVerbose(t *testing.T) {
	for _, c := range []*Catalog{Catalina, Sequoia} {
		m, ok := c.Identify(verboseLines, image.Rect(0, 0, 1024, 768))
		require.True(t, ok, c.Version)
		require.Equal(t, "boot.verbose", m.Name(), c.Version)
	}
	// Real UI screens still win over the verbose catch-all.
	m, ok := Catalina.Identify(utilitiesLines, image.Rect(0, 0, 1920, 1080))
	require.True(t, ok)
	require.Equal(t, "recovery.utilities", m.Name())
}

func TestFindAnchorsButtonToItsLabel(t *testing.T) {
	// The installer's license page as read on 2026-10-10: OCR put the
	// labels under the arrow buttons, the detector put the Agree box in
	// the gap between the two buttons.
	lines := []textshot.TextResult{
		{Text: "Disagree", Box: image.Rect(886, 713, 945, 732)},
		{Text: "Agree", Box: image.Rect(984, 713, 1026, 732)},
	}
	m := Match{Lines: lines, Elements: []textshot.Element{
		{Key: "button:Agree", Kind: textshot.KindButton, Text: "Agree", Box: image.Rect(943, 684, 978, 731)},
	}}
	box, ok := m.Find("button:Agree")
	require.True(t, ok)
	require.Equal(t, image.Rect(984, 685, 1026, 732), box, "the label reaching up over its arrow, not the misplaced box")
	require.Equal(t, image.Pt(1005, 708), image.Pt((box.Min.X+box.Max.X)/2, (box.Min.Y+box.Max.Y)/2))

	// A box that covers both buttons: its centre is the gap, the label wins.
	m.Elements[0].Box = image.Rect(887, 679, 1033, 733)
	box, _ = m.Find("button:Agree")
	require.Equal(t, image.Rect(984, 685, 1026, 732), box)

	// A box that contains its label keeps the detector's box (the whole
	// control, circle and label).
	m.Elements[0].Box = image.Rect(980, 679, 1033, 733)
	box, _ = m.Find("button:Agree")
	require.Equal(t, image.Rect(980, 679, 1033, 733), box)
}
