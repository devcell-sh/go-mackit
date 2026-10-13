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
	"fmt"
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
	// Elements are the detected UI elements (text, buttons, icons) with
	// their measured Style. Nil when the step did not need them: element
	// detection costs several seconds per frame, so the runner only runs
	// it on demand.
	Elements []textshot.Element
	// Theme is the screen's brightness when elements were detected.
	Theme textshot.Theme
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
	el, ok := m.findElement(selector)
	return el.Box, ok
}

// findElement resolves a selector to an element. A textshot Key
// ("button:Continue", "icon:Disk Utility", "icon@700,310", "text:Agree#2")
// matches exactly first; then "text:" and "button:" selectors match by
// text (exact beats substring); then catalog element names. Elements that
// read as disabled (greyed out) never resolve: clicking them does nothing,
// and a step waiting for "text:Install" should wait until it is enabled.
func (m Match) findElement(selector string) (textshot.Element, bool) {
	for _, e := range m.Elements {
		if e.Key == selector && !disabled(e) {
			return m.anchorToLabel(e), true
		}
	}
	switch {
	case strings.HasPrefix(selector, "text:"):
		return m.findText(selector[len("text:"):], textshot.KindText, textshot.KindButton)
	case strings.HasPrefix(selector, "button:"):
		return m.findText(selector[len("button:"):], textshot.KindButton)
	case strings.HasPrefix(selector, "icon:"), strings.HasPrefix(selector, "icon@"):
		return textshot.Element{}, false
	}
	if m.Screen == nil {
		return textshot.Element{}, false
	}
	for _, e := range m.Screen.Elements {
		if e.Name == selector {
			box, ok := m.locate(e)
			return textshot.Element{Box: box, Kind: textshot.KindButton, Key: selector, Text: e.Text}, ok
		}
	}
	return textshot.Element{}, false
}

// Explain says why a selector does not resolve when the reason is visible
// on screen: the element is there but disabled. Empty when the selector
// resolves or the element is simply absent.
func (m Match) Explain(selector string) string {
	if _, ok := m.findElement(selector); ok {
		return ""
	}
	want := Normalize(selectorText(selector))
	if want == "" {
		return ""
	}
	for _, e := range m.Elements {
		if e.Key == selector || strings.Contains(Normalize(e.Text), want) {
			if disabled(e) {
				return fmt.Sprintf("%q is on screen but disabled (contrast %.1f)", selector, e.Style.Contrast)
			}
		}
	}
	return ""
}

func disabled(e textshot.Element) bool {
	return e.Style.Disabled
}

func center(r image.Rectangle) image.Point {
	return image.Pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

// Center returns the click point of a selector's element.
func (m Match) Center(selector string) (image.Point, bool) {
	r, ok := m.Find(selector)
	if !ok {
		return image.Point{}, false
	}
	return center(r), true
}

// findText returns the box of the element whose text best matches want:
// an exact normalized match beats a substring match, and earlier kinds in
// the list beat later ones. Falls back to the OCR lines when no elements
// were detected.
func (m Match) findText(want string, kinds ...textshot.Kind) (textshot.Element, bool) {
	want = Normalize(want)
	if want == "" {
		return textshot.Element{}, false
	}
	bestRank := -1
	var best textshot.Element
	consider := func(text string, el textshot.Element, kindRank int) {
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
			bestRank, best = rank, el
		}
	}
	if m.Elements != nil {
		for _, e := range m.Elements {
			if disabled(e) {
				continue
			}
			for k, kind := range kinds {
				if e.Kind == kind {
					consider(e.Text, e, k)
				}
			}
		}
		if bestRank >= 0 {
			best = m.anchorToLabel(best)
		}
	} else {
		for _, l := range m.Lines {
			consider(l.Text, textshot.Element{Kind: textshot.KindText, Box: l.Box, Text: l.Text, Score: l.Score}, 0)
		}
	}
	return best, bestRank >= 0
}

// anchorToLabel makes sure a click on a text-bearing element lands on its
// label. The detector's box is occasionally off: on the installer's
// license page it once put "Agree" in the gap between the Disagree and
// Agree arrow buttons, and a wide box spanning two buttons has its centre
// between them. When the OCR line carrying the element's text is not
// centred inside the box, or the box is more than twice as wide as the
// line, the line's box is used instead: the label is part of the control.
func (m Match) anchorToLabel(e textshot.Element) textshot.Element {
	want := Normalize(e.Text)
	if want == "" || e.Kind == textshot.KindIcon {
		// An icon's label sits beside or under it; the icon is the target.
		return e
	}
	for _, l := range m.Lines {
		if Normalize(l.Text) != want {
			continue
		}
		c := image.Pt((l.Box.Min.X+l.Box.Max.X)/2, (l.Box.Min.Y+l.Box.Max.Y)/2)
		if !c.In(e.Box) || e.Box.Dx() > 2*l.Box.Dx() {
			// The installer's buttons are an arrow circle above a label,
			// and a click on the label alone did not always register
			// (Install, 2026-10-10), so the box reaches up over the
			// circle: its centre lands where the detector's box centre
			// does when that box is right.
			e.Box = image.Rect(l.Box.Min.X, l.Box.Min.Y-3*l.Box.Dy()/2, l.Box.Max.X, l.Box.Max.Y)
		}
		return e
	}
	return e
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
