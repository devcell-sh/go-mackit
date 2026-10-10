// Package flow drives the macOS installer UI step by step: it reads the
// guest screen with OCR, identifies which installer screen is showing from
// a per-macOS-version catalog, locates UI elements by text or kind, and
// sends keyboard or pointer input through the VM backend.
//
// A Catalog is pure data, one per macOS version (Catalina, Sequoia). The
// Runner is backend-agnostic: it takes a screen capture function, an OCR
// engine and an Input implementation (QMP for QEMU).
//
// Elements are addressed by selector strings in the format of textshot
// element keys:
//
//	text:Reinstall macOS     a text line or labelled control with that text
//	button:Continue          a detected interactable labelled "Continue"
//	icon@700,310             an unlabelled interactable by its key
//	utilities_menu           a named Element of the current catalog screen
//
// Text matches are case- and punctuation-insensitive; an exact match is
// preferred over a substring match.
package flow

import (
	"image"
	"regexp"
	"strings"

	"github.com/devcell-sh/go-textshot"
)

// Element is a named piece of on-screen text a catalog screen exposes for
// clicking. Text is matched against every OCR line; when it is only part
// of a longer line (the menu bar is one line), its position is estimated
// from the character offsets and snapped to a detected interactable at
// that spot when there is one.
type Element struct {
	Name string
	Text string
	// Nth selects the occurrence of Text within the matched line when it
	// appears more than once (0 = first). "Utilities" appears twice in
	// "macOS Utilities File Edit Utilities Window".
	Nth int
}

// Screen describes one installer screen by the text it shows. All phrases
// in All must be present and, when Any is non-empty, at least one of Any.
type Screen struct {
	Name string
	All  []string
	Any  []string
	// Elements are named positions a step on this screen may click.
	Elements []Element
	// Verified records whether the phrases were taken from a real
	// screenshot of this macOS version (false = best guess, tune on sight).
	Verified bool
}

// Catalog is the screen set of one macOS version. Screens are tried in
// order and the first match wins, so put the most specific first.
type Catalog struct {
	Version string
	Screens []Screen
}

// Match is an identified screen together with what was read from it.
type Match struct {
	// Screen is nil when no catalog screen matched.
	Screen *Screen
	// Bounds is the captured display size; element boxes and ClickAt
	// coordinates are in this space.
	Bounds image.Rectangle
	// Lines are the OCR text lines the screen was identified from.
	Lines []textshot.TextResult
	// Elements are the detected UI elements (text, buttons, icons). Nil
	// when the step did not need them: element detection costs several
	// seconds per frame, so the runner only runs it on demand.
	Elements []textshot.Element
}

// Text returns the OCR lines joined by newlines.
func (m Match) Text() string {
	return strings.Join(textshot.Lines(m.Lines), "\n")
}

// Name is the matched screen's name, or "" when nothing matched.
func (m Match) Name() string {
	if m.Screen == nil {
		return ""
	}
	return m.Screen.Name
}

// Find resolves a selector (see the package doc) to an element box.
func (m Match) Find(selector string) (image.Rectangle, bool) {
	switch {
	case strings.HasPrefix(selector, "text:"):
		return m.findText(selector[len("text:"):], textshot.KindText, textshot.KindButton)
	case strings.HasPrefix(selector, "button:"):
		return m.findText(selector[len("button:"):], textshot.KindButton)
	case strings.HasPrefix(selector, "icon@"):
		for _, e := range m.Elements {
			if e.Key == selector {
				return e.Box, true
			}
		}
		return image.Rectangle{}, false
	}
	if m.Screen == nil {
		return image.Rectangle{}, false
	}
	for _, e := range m.Screen.Elements {
		if e.Name == selector {
			return m.locate(e)
		}
	}
	return image.Rectangle{}, false
}

// Center returns the click point of a selector's element.
func (m Match) Center(selector string) (image.Point, bool) {
	r, ok := m.Find(selector)
	if !ok {
		return image.Point{}, false
	}
	return image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2), true
}

// findText returns the box of the element whose text best matches want:
// an exact normalized match beats a substring match, and earlier kinds in
// the list beat later ones. Falls back to the OCR lines when no elements
// were detected.
func (m Match) findText(want string, kinds ...textshot.Kind) (image.Rectangle, bool) {
	want = Normalize(want)
	if want == "" {
		return image.Rectangle{}, false
	}
	bestRank := -1
	var best image.Rectangle
	consider := func(text string, box image.Rectangle, kindRank int) {
		norm := Normalize(text)
		var rank int
		switch {
		case norm == want:
			rank = 2
		case strings.Contains(norm, want):
			rank = 1
		default:
			return
		}
		rank = rank*10 + (10 - kindRank)
		if rank > bestRank {
			bestRank, best = rank, box
		}
	}
	if m.Elements != nil {
		for _, e := range m.Elements {
			for k, kind := range kinds {
				if e.Kind == kind {
					consider(e.Text, e.Box, k)
				}
			}
		}
	} else {
		for _, l := range m.Lines {
			consider(l.Text, l.Box, 0)
		}
	}
	return best, bestRank >= 0
}

// locate resolves a catalog Element: the first OCR line containing its
// text, narrowed to the text's character span and snapped to a detected
// interactable at that spot.
func (m Match) locate(e Element) (image.Rectangle, bool) {
	want := Normalize(e.Text)
	if want == "" {
		return image.Rectangle{}, false
	}
	for _, l := range m.Lines {
		norm := Normalize(l.Text)
		start := nthIndex(norm, want, e.Nth)
		if start < 0 {
			continue
		}
		box := subBox(l.Box, norm, start, start+len(want))
		if box == l.Box {
			return box, true
		}
		return m.snap(box, l.Box), true
	}
	return image.Rectangle{}, false
}

// snap replaces an estimated sub-line box with the detected interactable
// (button or icon) that overlaps it most, if that element sits within
// the line (not much taller than it).
func (m Match) snap(est, line image.Rectangle) image.Rectangle {
	best, bestArea := est, 0
	for _, e := range m.Elements {
		if e.Kind == textshot.KindText || e.Box.Dy() > 2*line.Dy() {
			continue
		}
		if a := area(e.Box.Intersect(est)); a > bestArea {
			best, bestArea = e.Box, a
		}
	}
	return best
}

func area(r image.Rectangle) int { return r.Dx() * r.Dy() }

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Normalize lowercases text and collapses every run of non-alphanumeric
// characters into one space, so OCR noise in punctuation and case does not
// break matching ("macOs Utilities" and "macOS Utilities" both become
// "macos utilities").
func Normalize(s string) string {
	return strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(s), " "))
}

// Lookup finds a screen by name.
func (c *Catalog) Lookup(name string) *Screen {
	for i := range c.Screens {
		if c.Screens[i].Name == name {
			return &c.Screens[i]
		}
	}
	return nil
}

// Identify matches OCR output against the catalog. ok is false when no
// screen matches; the returned Match still carries the lines and bounds.
func (c *Catalog) Identify(lines []textshot.TextResult, bounds image.Rectangle) (m Match, ok bool) {
	m = Match{Bounds: bounds, Lines: lines}
	text := " " + Normalize(strings.Join(textshot.Lines(lines), " \n ")) + " "
	for i := range c.Screens {
		s := &c.Screens[i]
		if s.matches(text) {
			m.Screen = s
			return m, true
		}
	}
	return m, false
}

func (s *Screen) matches(text string) bool {
	for _, p := range s.All {
		if !strings.Contains(text, Normalize(p)) {
			return false
		}
	}
	if len(s.Any) == 0 {
		return len(s.All) > 0
	}
	for _, p := range s.Any {
		if strings.Contains(text, Normalize(p)) {
			return true
		}
	}
	return false
}

// nthIndex returns the byte offset of the nth occurrence of sub in s.
func nthIndex(s, sub string, nth int) int {
	off := 0
	for {
		i := strings.Index(s[off:], sub)
		if i < 0 {
			return -1
		}
		if nth == 0 {
			return off + i
		}
		nth--
		off += i + len(sub)
	}
}

// subBox narrows box to the characters [start,end) of a line of len(text)
// characters, assuming uniform character width.
func subBox(box image.Rectangle, text string, start, end int) image.Rectangle {
	n := len(text)
	if n == 0 || (start == 0 && end >= n) {
		return box
	}
	w := float64(box.Dx())
	x0 := box.Min.X + int(w*float64(start)/float64(n)+0.5)
	x1 := box.Min.X + int(w*float64(end)/float64(n)+0.5)
	return image.Rect(x0, box.Min.Y, max(x1, x0+1), box.Max.Y)
}
