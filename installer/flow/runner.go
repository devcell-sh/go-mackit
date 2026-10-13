package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/devcell-sh/go-textshot"
)

// Input sends keyboard and pointer events to the guest. vm/qemu.QMPInput
// implements it over QMP.
type Input interface {
	// SendKeys presses the key codes together as one chord.
	SendKeys(ctx context.Context, chord ...string) error
	// Click moves the pointer to pt (pixels within display) and clicks.
	Click(ctx context.Context, pt image.Point, display image.Rectangle) error
	// Type sends text as key presses.
	Type(ctx context.Context, text string) error
}

// OCR reads a screen capture. DetectText is cheap (about a second on two
// emulated CPUs) and identifies screens; DetectScreen adds the UI element
// detector (several seconds) and per-element styles on top of lines that
// DetectText already read, so OCR never runs twice on one frame. The
// runner only calls it once the text says the wanted screen is up. See
// NewEngineOCR for the go-textshot implementation.
type OCR interface {
	DetectText(ctx context.Context, img image.Image) ([]textshot.TextResult, error)
	DetectScreen(ctx context.Context, img image.Image, lines []textshot.TextResult) (*textshot.Screen, error)
}

// EngineOCR adapts a textshot.Engine to OCR: DetectScreen runs only the
// interactable detector and composes it with the lines already read.
type EngineOCR struct {
	*textshot.Engine
}

// NewEngineOCR wraps a textshot engine for the Runner.
func NewEngineOCR(e *textshot.Engine) EngineOCR { return EngineOCR{Engine: e} }

func (e EngineOCR) DetectScreen(ctx context.Context, img image.Image, lines []textshot.TextResult) (*textshot.Screen, error) {
	inter, err := e.DetectInteractables(ctx, img)
	if err != nil {
		return nil, err
	}
	return textshot.ComposeScreen(img, inter, lines), nil
}

// Action is one input to perform on a matched screen. Set exactly one
// field; the constructors below build them.
type Action struct {
	// Keys are pressed one after another ("tab", "tab", "ret").
	Keys []string
	// Chord keys are pressed together ("meta_l", "spc").
	Chord []string
	// Click is a selector (see the package doc) of the element to click:
	// "text:Reinstall macOS", "button:Continue", or a catalog element name.
	Click string
	// ClickAt clicks at fixed display coordinates.
	ClickAt *image.Point
	// Type sends text.
	Type string
	// Sleep pauses.
	Sleep time.Duration
}

func Keys(keys ...string) Action   { return Action{Keys: keys} }
func Chord(keys ...string) Action  { return Action{Chord: keys} }
func Click(element string) Action  { return Action{Click: element} }
func ClickAt(x, y int) Action      { p := image.Pt(x, y); return Action{ClickAt: &p} }
func Type(text string) Action      { return Action{Type: text} }
func Sleep(d time.Duration) Action { return Action{Sleep: d} }

// Step waits for a screen and then performs actions on it.
//
//	{Screen: "recovery.utilities", Elements: []string{"text:Reinstall macOS"},
//	 Actions: []Action{Click("text:Reinstall macOS")}}
//
// reads: wait until the Utilities screen shows "Reinstall macOS", then
// click it.
type Step struct {
	Name string
	// Screen is the catalog screen name to wait for. Empty means any
	// screen: the step then waits only for its Elements.
	Screen string
	// Elements are selectors that must resolve before the step runs (a
	// menu item that only appears once a menu is open, a button that
	// appears once a page has loaded). Steps that click by selector
	// should list the selector here so the wait covers it.
	Elements []string
	// Text lists phrases that must be among the OCR lines before the step
	// runs ("finished erase" in a Terminal window). Matching is normalized
	// substring, like catalog phrases, and needs no element detector.
	Text []string
	// Timeout bounds the wait (default Runner.DefaultTimeout).
	Timeout time.Duration
	// Optional steps are skipped instead of failing when the screen does
	// not appear in time (the OpenCore picker is hidden by config, for
	// instance).
	Optional bool
	// Reactions run their actions whenever another screen is observed
	// while waiting (press Enter on the boot picker, dismiss a dialog)
	// and keep waiting.
	Reactions []Reaction
	// DoneOn lists screens that mean the step's purpose is already
	// achieved because the UI moved on by itself (a page that advances on
	// its own, a dialog that closed). Seeing one ends the step without
	// its actions.
	DoneOn []string
	// Display makes the step wait for the guest display to be this size
	// and stay so for DisplayHold (default 30 s) instead of reading the
	// screen. The installed system's window server switches QEMU's
	// display to its own mode, which is how the install flow knows the
	// installer's reboots are over; the hold skips the few seconds every
	// boot spends in the same mode before OpenCore sets its own.
	Display     image.Point
	DisplayHold time.Duration
	Actions     []Action
}

// errMovedOn is returned by waitFor when a DoneOn screen is observed.
var errMovedOn = errors.New("the screen moved on")

// Reaction pairs a screen with the actions to perform when it shows up
// while a step is waiting for a different screen.
type Reaction struct {
	Screen  string
	Actions []Action
}

// FrameTimeLayout is the ISO 8601 basic UTC layout used in saved frame
// names (20261010T055339Z): sortable, colon-free, same convention as the
// test result directories.
const FrameTimeLayout = "20060102T150405Z"

// ErrTimeout is returned (wrapped) when a screen does not appear in time.
var ErrTimeout = errors.New("screen did not appear")

// Runner observes the screen and executes steps.
type Runner struct {
	// Capture returns the current guest display.
	Capture func(ctx context.Context) (image.Image, error)
	OCR     OCR
	Input   Input
	Catalog *Catalog
	Logger  *slog.Logger
	// Interval between screen reads while waiting (default 3s).
	Interval time.Duration
	// DefaultTimeout for steps without one (default 5m).
	DefaultTimeout time.Duration
	// SettleDelay is the pause after each action so the UI can react
	// before the next read (default 500ms). With SettleTimeout set it is
	// the capture interval of the settle wait instead.
	SettleDelay time.Duration
	// SettleTimeout, when set, replaces the fixed pause after an action
	// with a wait for the screen to stop changing: frames are captured
	// every SettleDelay until two in a row differ by less than
	// ChangeThreshold, or SettleTimeout elapses. Page transitions and
	// animations take as long as they take instead of a guessed delay.
	SettleTimeout time.Duration
	// ChangeThreshold is the fraction of changed pixels (textshot.Diff)
	// below which two frames count as the same screen (default 0.002).
	// A frame that has not changed since the last read reuses that
	// reading instead of running the models again.
	ChangeThreshold float64
	// EnterOnDefault presses Enter instead of clicking when the click
	// target is a highlighted (default) button, which is what the
	// installer's blue Continue and Agree buttons are. Enter does not
	// depend on pointer coordinates.
	EnterOnDefault bool
	// ShotDir, when set, receives a PNG of every frame the runner reads,
	// named <ISO 8601 basic UTC time>-<screen>-<sequence>.png, for
	// example 20261010T055339Z-recovery.utilities-0002.png (screen is the
	// identified catalog name or "unknown"). The basic profile has no
	// colons, which Finder would display as slashes. Meant for debug runs.
	ShotDir string
	// OCRDir, when set, receives what OCR read from every frame as JSON,
	// named like the frames (<time>-<screen>-<sequence>.json) so a frame
	// and its reading pair up. See Reading.
	OCRDir string

	last    string
	shotSeq int
	// prev is the last frame read with the models, for Diff reuse.
	prev *reading
}

// reading is one frame's capture and what the models made of it.
type reading struct {
	img      image.Image
	lines    []textshot.TextResult
	screen   *textshot.Screen // nil when the detector did not run
	elements bool
}

// need is what a wait requires of a frame: whether elements must be
// detected, and, to skip the detector while the wanted screen is not up,
// the screen name and selectors the step waits for.
type need struct {
	elements  bool
	screen    string
	selectors []string
}

// Reading is what the runner saved for one frame when OCRDir is set.
type Reading struct {
	// Time the frame was captured, RFC 3339 UTC.
	Time string `json:"time"`
	// Screen is the identified catalog screen or "unknown".
	Screen string `json:"screen"`
	// Size of the frame in pixels.
	Size ReadSize `json:"size"`
	// Frame is the PNG path when ShotDir is also set.
	Frame string `json:"frame,omitempty"`
	// Elements is true when the UI element detector ran on this frame.
	Elements bool `json:"elements_detected"`
	// Theme and Backdrop are the screen's brightness and dominant colour
	// (set when the detector ran).
	Theme    textshot.Theme `json:"theme,omitempty"`
	Backdrop string         `json:"backdrop,omitempty"`
	Lines    []ReadLine     `json:"lines"`
	Found    []ReadElement  `json:"found,omitempty"`
}

// ReadSize is a frame size in pixels.
type ReadSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// ReadLine is one OCR text line.
type ReadLine struct {
	Text  string        `json:"text"`
	Box   [4]int        `json:"box"`
	Score float32       `json:"score"`
	Value *float64      `json:"value,omitempty"`
	UOM   textshot.Unit `json:"uom,omitempty"`
}

// ReadElement is one detected UI element.
type ReadElement struct {
	ID    int            `json:"id"`
	Key   string         `json:"key"`
	Kind  string         `json:"kind"`
	Text  string         `json:"text,omitempty"`
	Box   [4]int         `json:"box"`
	Score float32        `json:"score"`
	Style textshot.Style `json:"style"`
	Value *float64       `json:"value,omitempty"`
	UOM   textshot.Unit  `json:"uom,omitempty"`
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Logger
}

func (r *Runner) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return 3 * time.Second
}

func (r *Runner) settle() time.Duration {
	if r.SettleDelay > 0 {
		return r.SettleDelay
	}
	return 500 * time.Millisecond
}

// Observe reads the screen once and identifies it. ok is false when no
// catalog screen matches. Screen changes are logged at Info, raw OCR lines
// at Debug. With elements set, the UI element detector also runs on the
// frame and Match.Elements is populated. A frame that has not changed
// since the previous read (textshot.Diff below ChangeThreshold) reuses
// that reading instead of running the models again.
func (r *Runner) Observe(ctx context.Context, elements bool) (Match, bool, error) {
	return r.observe(ctx, need{elements: elements})
}

func (r *Runner) changeThreshold() float64 {
	if r.ChangeThreshold > 0 {
		return r.ChangeThreshold
	}
	return DefaultChangeThreshold
}

// DefaultChangeThreshold is the fraction of changed pixels below which a
// capture counts as the same frame as the last one read with the models.
// It sits between the noise of a static screen (a blinking cursor or a
// spinner moves under 0.02% of a 1920x1080 frame) and the smallest change
// worth reading: one line of Terminal output is about 0.15%.
const DefaultChangeThreshold = 0.0005

func (r *Runner) observe(ctx context.Context, n need) (Match, bool, error) {
	img, err := r.Capture(ctx)
	if err != nil {
		return Match{}, false, fmt.Errorf("screen capture: %w", err)
	}
	log := r.logger()

	// Same frame as the last one read with the models: reuse the reading
	// and run the detector only if it is now needed and was not run
	// before. The comparison is always against that last-read frame, not
	// the previous capture, so a screen that changes a little at a time
	// (Terminal printing lines, a progress bar) is read again once the
	// changes add up instead of never.
	var rd *reading
	reused := false
	if r.prev != nil {
		if _, frac := textshot.Diff(r.prev.img, img); frac < r.changeThreshold() {
			rd = &reading{img: r.prev.img, lines: r.prev.lines, screen: r.prev.screen, elements: r.prev.elements}
			reused = true
			log.Debug("screen unchanged, reusing reading", "changed", frac)
		}
	}
	if rd == nil {
		lines, err := r.OCR.DetectText(ctx, img)
		if err != nil {
			return Match{}, false, fmt.Errorf("ocr: %w", err)
		}
		rd = &reading{img: img, lines: lines}
	}
	m, ok := r.Catalog.Identify(rd.lines, img.Bounds())
	name := m.Name()
	if name == "" {
		name = "unknown"
	}

	// The detector is the expensive part: run it only when the step can
	// use the result, that is once the text says the wanted screen is up
	// and every text selector it waits for is on screen.
	detected := false
	if n.elements && rd.screen == nil {
		if gate, why := detectorWanted(n, name, rd.lines); gate {
			scr, err := r.OCR.DetectScreen(ctx, img, rd.lines)
			if err != nil {
				return Match{}, false, fmt.Errorf("elements: %w", err)
			}
			rd.screen = scr
			rd.elements = true
			detected = true
		} else {
			log.Debug("element detector skipped", "screen", name, "reason", why)
		}
	}
	if rd.screen != nil {
		m.Elements = rd.screen.Elements
		m.Theme = rd.screen.Theme
	}
	// A frame is worth saving and logging when a model ran on it: fresh
	// OCR, or the detector on a frame read before.
	changed := !reused || detected
	r.prev = rd

	if changed && (r.ShotDir != "" || r.OCRDir != "") {
		r.shotSeq++
		base := fmt.Sprintf("%s-%s-%04d", time.Now().UTC().Format(FrameTimeLayout), name, r.shotSeq)
		framePath := ""
		if r.ShotDir != "" {
			if path, err := r.saveFrame(img, base); err != nil {
				log.Debug("saving screen frame", "error", err)
			} else {
				framePath = path
				log.Debug("screen frame saved", "path", path)
			}
		}
		if r.OCRDir != "" {
			if path, err := r.saveReading(base, name, img.Bounds().Size(), framePath, rd); err != nil {
				log.Debug("saving OCR reading", "error", err)
			} else {
				log.Debug("OCR reading saved", "path", path)
			}
		}
	}
	if name != r.last {
		log.Info("screen changed", "screen", name, "size", img.Bounds().Size(), "lines", len(rd.lines))
		r.last = name
	}
	if changed {
		log.Debug("screen read", "screen", name, "text", m.Text())
		if rd.screen != nil {
			keys := make([]string, len(m.Elements))
			for i, e := range m.Elements {
				keys[i] = e.Key
			}
			log.Debug("elements read", "screen", name, "theme", m.Theme, "elements", keys)
		}
		for _, l := range rd.lines {
			if l.UOM != "" {
				log.Info("progress", "screen", name, "value", l.Value, "uom", string(l.UOM), "text", l.Text)
			}
		}
	}
	return m, ok, nil
}

// detectorWanted decides whether running the element detector on this
// frame can satisfy the wait: the wanted screen (if any) is identified and
// every selector that names text has that text among the OCR lines.
func detectorWanted(n need, screen string, lines []textshot.TextResult) (bool, string) {
	if n.screen != "" && screen != n.screen {
		return false, fmt.Sprintf("waiting for %q", n.screen)
	}
	var text string
	for _, l := range lines {
		text += " " + Normalize(l.Text)
	}
	for _, sel := range n.selectors {
		want := selectorText(sel)
		if want != "" && !strings.Contains(text, Normalize(want)) {
			return false, fmt.Sprintf("%q not on screen", sel)
		}
	}
	return true, ""
}

// selectorText returns the text a selector looks for, or "" when it is
// positional (icon@x,y) or a catalog element name.
func selectorText(sel string) string {
	for _, prefix := range []string{"text:", "button:", "icon:"} {
		if strings.HasPrefix(sel, prefix) {
			want := sel[len(prefix):]
			if i := strings.LastIndex(want, "#"); i > 0 {
				want = want[:i]
			}
			return want
		}
	}
	return ""
}

// WaitFor polls until the named screen (any screen when empty) is showing
// with all listed element selectors resolved, or timeout elapses.
func (r *Runner) WaitFor(ctx context.Context, screen string, elements []string, timeout time.Duration) (Match, error) {
	return r.waitFor(ctx, screen, elements, nil, timeout, nil, nil, len(elements) > 0)
}

func (r *Runner) waitFor(ctx context.Context, screen string, elements, texts []string, timeout time.Duration, reactions []Reaction, doneOn []string, needElements bool) (Match, error) {
	if screen != "" && r.Catalog.Lookup(screen) == nil {
		return Match{}, fmt.Errorf("screen %q is not in the %s catalog", screen, r.Catalog.Version)
	}
	for _, re := range reactions {
		if r.Catalog.Lookup(re.Screen) == nil {
			return Match{}, fmt.Errorf("reaction screen %q is not in the %s catalog", re.Screen, r.Catalog.Version)
		}
	}
	for _, d := range doneOn {
		if r.Catalog.Lookup(d) == nil {
			return Match{}, fmt.Errorf("done-on screen %q is not in the %s catalog", d, r.Catalog.Version)
		}
	}
	if timeout <= 0 {
		timeout = r.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	lastSeen := ""
	n := need{elements: needElements, screen: screen, selectors: elements}
	for _, t := range texts {
		n.selectors = append(n.selectors, "text:"+t)
	}
	for {
		m, ok, err := r.observe(ctx, n)
		if err != nil {
			r.logger().Debug("screen read failed", "error", err)
		} else {
			if ok {
				lastSeen = m.Name()
			}
			if (screen == "" || m.Name() == screen) && hasText(m, texts) && hasElements(m, elements) {
				return m, nil
			}
			if slices.Contains(doneOn, m.Name()) {
				return m, errMovedOn
			}
			for _, re := range reactions {
				if re.Screen != m.Name() {
					continue
				}
				r.logger().Info("reacting to screen", "screen", m.Name(), "actions", len(re.Actions))
				if err := r.doAll(ctx, m, re.Actions); err != nil {
					return m, fmt.Errorf("reaction to %q: %w", m.Name(), err)
				}
			}
		}
		if time.Now().After(deadline) {
			if lastSeen == "" {
				lastSeen = "unknown"
			}
			detail := ""
			for _, sel := range elements {
				if why := m.Explain(sel); why != "" {
					detail += "; " + why
				}
			}
			return m, fmt.Errorf("%w: wanted %q within %v, last seen %q%s", ErrTimeout, screen, timeout, lastSeen, detail)
		}
		if err := sleepCtx(ctx, r.interval()); err != nil {
			return Match{}, err
		}
	}
}

// waitForDisplay captures the screen every interval until it has been
// size for hold (default 30 s), logging each mode change. No OCR runs.
func (r *Runner) waitForDisplay(ctx context.Context, size image.Point, hold, timeout time.Duration) error {
	if hold <= 0 {
		hold = 30 * time.Second
	}
	if timeout <= 0 {
		timeout = r.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	var last image.Point
	var since time.Time
	for {
		img, err := r.Capture(ctx)
		if err != nil {
			r.logger().Debug("screen capture failed", "error", err)
		} else {
			cur := img.Bounds().Size()
			if cur != last {
				r.logger().Info("display mode", "size", cur.String())
				last = cur
				since = time.Now()
			}
			if cur == size && time.Since(since) >= hold {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: wanted display %s within %v, last %s", ErrTimeout, size.String(), timeout, last.String())
		}
		if err := sleepCtx(ctx, r.interval()); err != nil {
			return err
		}
	}
}

// hasText reports whether every phrase is among the OCR lines.
func hasText(m Match, phrases []string) bool {
	if len(phrases) == 0 {
		return true
	}
	var text string
	for _, l := range m.Lines {
		text += " " + Normalize(l.Text)
	}
	for _, p := range phrases {
		if !strings.Contains(text, Normalize(p)) {
			return false
		}
	}
	return true
}

func hasElements(m Match, selectors []string) bool {
	for _, s := range selectors {
		if _, ok := m.Find(s); !ok {
			return false
		}
	}
	return true
}

// needsElements reports whether a step clicks by selector or waits for
// selectors, in which case frames are read with the element detector.
func (s Step) needsElements() bool {
	if len(s.Elements) > 0 {
		return true
	}
	for _, a := range s.Actions {
		if a.Click != "" {
			return true
		}
	}
	return false
}

// Do performs one action against a matched screen. A click whose target
// does not resolve in m (the screen moved on since m was read: a row was
// selected and its Continue button lit up) re-reads the screen once and
// retries, so actions later in a step act on what is on screen now.
func (r *Runner) Do(ctx context.Context, m Match, a Action) error {
	_, err := r.do(ctx, m, a)
	return err
}

func (r *Runner) do(ctx context.Context, m Match, a Action) (Match, error) {
	switch {
	case len(a.Keys) > 0:
		for _, k := range a.Keys {
			if err := r.Input.SendKeys(ctx, k); err != nil {
				return m, fmt.Errorf("key %s: %w", k, err)
			}
			if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
				return m, err
			}
		}
	case len(a.Chord) > 0:
		return m, r.Input.SendKeys(ctx, a.Chord...)
	case a.Click != "":
		el, ok := m.findElement(a.Click)
		if !ok {
			r.logger().Debug("click target not resolved, re-reading the screen", "element", a.Click, "reason", m.Explain(a.Click))
			fresh, _, err := r.observe(ctx, need{elements: true, selectors: []string{a.Click}})
			if err != nil {
				return m, err
			}
			m = fresh
			el, ok = m.findElement(a.Click)
		}
		if !ok {
			why := m.Explain(a.Click)
			if why == "" {
				why = fmt.Sprintf("element %q not found on screen %q", a.Click, m.Name())
			}
			return m, errors.New(why)
		}
		if r.EnterOnDefault && el.Kind == textshot.KindButton && el.Style.Highlighted {
			r.logger().Debug("default button, pressing Enter", "element", a.Click, "key", el.Key)
			return m, r.Input.SendKeys(ctx, "ret")
		}
		pt := center(el.Box)
		r.logger().Debug("click element", "element", a.Click, "key", el.Key, "at", pt)
		return m, r.Input.Click(ctx, pt, m.Bounds)
	case a.ClickAt != nil:
		return m, r.Input.Click(ctx, *a.ClickAt, m.Bounds)
	case a.Type != "":
		return m, r.Input.Type(ctx, a.Type)
	case a.Sleep > 0:
		return m, sleepCtx(ctx, a.Sleep)
	}
	return m, nil
}

// Run executes steps in order: wait for each step's screen, then perform
// its actions. Optional steps whose screen never appears are skipped.
func (r *Runner) Run(ctx context.Context, steps []Step) error {
	log := r.logger()
	for i, s := range steps {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("step %d", i+1)
		}
		var m Match
		var err error
		if s.Display != (image.Point{}) {
			log.Info("waiting for display mode", "step", name, "size", s.Display.String())
			err = r.waitForDisplay(ctx, s.Display, s.DisplayHold, s.Timeout)
		} else {
			log.Info("waiting for screen", "step", name, "screen", s.Screen)
			m, err = r.waitFor(ctx, s.Screen, s.Elements, s.Text, s.Timeout, s.Reactions, s.DoneOn, s.needsElements())
		}
		if err != nil {
			if errors.Is(err, errMovedOn) {
				log.Info("step done, the UI moved on by itself", "step", name, "screen", m.Name())
				continue
			}
			if s.Optional && errors.Is(err, ErrTimeout) {
				log.Info("optional step skipped", "step", name, "reason", err)
				continue
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := r.doAll(ctx, m, s.Actions); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		log.Info("step done", "step", name, "actions", len(s.Actions))
	}
	return nil
}

// saveFrame writes img to ShotDir as <ISO 8601 basic UTC>-<screen>-<seq>.png.
func (r *Runner) saveFrame(img image.Image, base string) (string, error) {
	if err := os.MkdirAll(r.ShotDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(r.ShotDir, base+".png")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	return path, f.Close()
}

// saveReading writes the OCR result for one frame as JSON into OCRDir.
func (r *Runner) saveReading(base, screen string, size image.Point, framePath string, in *reading) (string, error) {
	if err := os.MkdirAll(r.OCRDir, 0o755); err != nil {
		return "", err
	}
	rd := Reading{
		Time:     time.Now().UTC().Format(time.RFC3339Nano),
		Screen:   screen,
		Size:     ReadSize{W: size.X, H: size.Y},
		Frame:    framePath,
		Elements: in.screen != nil,
		Lines:    make([]ReadLine, 0, len(in.lines)),
	}
	for _, l := range in.lines {
		rl := ReadLine{Text: l.Text, Box: boxOf(l.Box), Score: l.Score}
		if l.UOM != "" {
			rl.Value = &l.Value
			rl.UOM = l.UOM
		}
		rd.Lines = append(rd.Lines, rl)
	}
	if in.screen != nil {
		rd.Theme = in.screen.Theme
		b := in.screen.Backdrop
		rd.Backdrop = fmt.Sprintf("#%02x%02x%02x", b.R, b.G, b.B)
		for _, e := range in.screen.Elements {
			re := ReadElement{ID: e.ID, Key: e.Key, Kind: string(e.Kind), Text: e.Text, Box: boxOf(e.Box), Score: e.Score, Style: e.Style}
			if e.UOM != "" {
				re.Value = &e.Value
				re.UOM = e.UOM
			}
			rd.Found = append(rd.Found, re)
		}
	}
	data, err := json.MarshalIndent(rd, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(r.OCRDir, base+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func boxOf(b image.Rectangle) [4]int {
	return [4]int{b.Min.X, b.Min.Y, b.Max.X, b.Max.Y}
}

// doAll performs actions in order, letting the screen settle after each.
func (r *Runner) doAll(ctx context.Context, m Match, actions []Action) error {
	for j, a := range actions {
		var err error
		if m, err = r.do(ctx, m, a); err != nil {
			return fmt.Errorf("action %d: %w", j+1, err)
		}
		if err := r.waitSettled(ctx); err != nil {
			return err
		}
	}
	return nil
}

// waitSettled pauses after an action. With SettleTimeout set it captures
// frames every SettleDelay until two consecutive ones differ by less than
// ChangeThreshold (the UI stopped moving) or SettleTimeout elapses;
// otherwise it sleeps SettleDelay.
func (r *Runner) waitSettled(ctx context.Context) error {
	if r.SettleTimeout <= 0 {
		return sleepCtx(ctx, r.settle())
	}
	deadline := time.Now().Add(r.SettleTimeout)
	var prev image.Image
	for {
		if err := sleepCtx(ctx, r.settle()); err != nil {
			return err
		}
		img, err := r.Capture(ctx)
		if err != nil {
			return nil // the next observe reports capture errors
		}
		if prev != nil {
			if _, frac := textshot.Diff(prev, img); frac < r.changeThreshold() {
				return nil
			}
		}
		if time.Now().After(deadline) {
			r.logger().Debug("screen still changing after settle timeout", "timeout", r.SettleTimeout)
			return nil
		}
		prev = img
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
