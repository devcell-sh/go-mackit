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

// OCR reads a screen capture. *textshot.Engine implements it. DetectText
// is cheap and used to identify screens; DetectElements also runs the UI
// element detector and is only called when a step needs element boxes.
type OCR interface {
	DetectText(ctx context.Context, img image.Image) ([]textshot.TextResult, error)
	DetectElements(ctx context.Context, img image.Image) ([]textshot.Element, error)
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
	Actions   []Action
}

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
	// before the next read (default 500ms).
	SettleDelay time.Duration
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
	Elements bool          `json:"elements_detected"`
	Lines    []ReadLine    `json:"lines"`
	Found    []ReadElement `json:"found,omitempty"`
}

// ReadSize is a frame size in pixels.
type ReadSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// ReadLine is one OCR text line.
type ReadLine struct {
	Text  string  `json:"text"`
	Box   [4]int  `json:"box"`
	Score float32 `json:"score"`
}

// ReadElement is one detected UI element.
type ReadElement struct {
	ID    int     `json:"id"`
	Key   string  `json:"key"`
	Kind  string  `json:"kind"`
	Text  string  `json:"text,omitempty"`
	Box   [4]int  `json:"box"`
	Score float32 `json:"score"`
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
// frame and Match.Elements is populated.
func (r *Runner) Observe(ctx context.Context, elements bool) (Match, bool, error) {
	img, err := r.Capture(ctx)
	if err != nil {
		return Match{}, false, fmt.Errorf("screen capture: %w", err)
	}
	var lines []textshot.TextResult
	var els []textshot.Element
	if elements {
		els, err = r.OCR.DetectElements(ctx, img)
		if err != nil {
			return Match{}, false, fmt.Errorf("elements: %w", err)
		}
		for _, e := range els {
			if e.Kind != textshot.KindIcon {
				lines = append(lines, textshot.TextResult{Box: e.Box, Text: e.Text, Score: e.Score})
			}
		}
	} else {
		lines, err = r.OCR.DetectText(ctx, img)
		if err != nil {
			return Match{}, false, fmt.Errorf("ocr: %w", err)
		}
	}
	m, ok := r.Catalog.Identify(lines, img.Bounds())
	m.Elements = els
	name := m.Name()
	if name == "" {
		name = "unknown"
	}
	log := r.logger()
	if r.ShotDir != "" || r.OCRDir != "" {
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
			if path, err := r.saveReading(base, name, img.Bounds().Size(), framePath, elements, lines, els); err != nil {
				log.Debug("saving OCR reading", "error", err)
			} else {
				log.Debug("OCR reading saved", "path", path)
			}
		}
	}
	if name != r.last {
		log.Info("screen changed", "screen", name, "size", img.Bounds().Size(), "lines", len(lines))
		r.last = name
	}
	log.Debug("screen read", "screen", name, "text", m.Text())
	if elements {
		keys := make([]string, len(els))
		for i, e := range els {
			keys[i] = e.Key
		}
		log.Debug("elements read", "screen", name, "elements", keys)
	}
	return m, ok, nil
}

// WaitFor polls until the named screen (any screen when empty) is showing
// with all listed element selectors resolved, or timeout elapses.
func (r *Runner) WaitFor(ctx context.Context, screen string, elements []string, timeout time.Duration) (Match, error) {
	return r.waitFor(ctx, screen, elements, timeout, nil, len(elements) > 0)
}

func (r *Runner) waitFor(ctx context.Context, screen string, elements []string, timeout time.Duration, reactions []Reaction, needElements bool) (Match, error) {
	if screen != "" && r.Catalog.Lookup(screen) == nil {
		return Match{}, fmt.Errorf("screen %q is not in the %s catalog", screen, r.Catalog.Version)
	}
	for _, re := range reactions {
		if r.Catalog.Lookup(re.Screen) == nil {
			return Match{}, fmt.Errorf("reaction screen %q is not in the %s catalog", re.Screen, r.Catalog.Version)
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
	for {
		m, ok, err := r.Observe(ctx, needElements)
		if err != nil {
			r.logger().Debug("screen read failed", "error", err)
		} else {
			if ok {
				lastSeen = m.Name()
			}
			if (screen == "" || m.Name() == screen) && hasElements(m, elements) {
				return m, nil
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
			return m, fmt.Errorf("%w: wanted %q within %v, last seen %q", ErrTimeout, screen, timeout, lastSeen)
		}
		if err := sleepCtx(ctx, r.interval()); err != nil {
			return Match{}, err
		}
	}
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

// Do performs one action against a matched screen.
func (r *Runner) Do(ctx context.Context, m Match, a Action) error {
	switch {
	case len(a.Keys) > 0:
		for _, k := range a.Keys {
			if err := r.Input.SendKeys(ctx, k); err != nil {
				return fmt.Errorf("key %s: %w", k, err)
			}
			if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
				return err
			}
		}
	case len(a.Chord) > 0:
		return r.Input.SendKeys(ctx, a.Chord...)
	case a.Click != "":
		pt, ok := m.Center(a.Click)
		if !ok {
			return fmt.Errorf("element %q not found on screen %q", a.Click, m.Name())
		}
		r.logger().Debug("click element", "element", a.Click, "at", pt)
		return r.Input.Click(ctx, pt, m.Bounds)
	case a.ClickAt != nil:
		return r.Input.Click(ctx, *a.ClickAt, m.Bounds)
	case a.Type != "":
		return r.Input.Type(ctx, a.Type)
	case a.Sleep > 0:
		return sleepCtx(ctx, a.Sleep)
	}
	return nil
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
		log.Info("waiting for screen", "step", name, "screen", s.Screen)
		m, err := r.waitFor(ctx, s.Screen, s.Elements, s.Timeout, s.Reactions, s.needsElements())
		if err != nil {
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
func (r *Runner) saveReading(base, screen string, size image.Point, framePath string, elements bool, lines []textshot.TextResult, els []textshot.Element) (string, error) {
	if err := os.MkdirAll(r.OCRDir, 0o755); err != nil {
		return "", err
	}
	rd := Reading{
		Time:     time.Now().UTC().Format(time.RFC3339Nano),
		Screen:   screen,
		Size:     ReadSize{W: size.X, H: size.Y},
		Frame:    framePath,
		Elements: elements,
		Lines:    make([]ReadLine, 0, len(lines)),
	}
	for _, l := range lines {
		rd.Lines = append(rd.Lines, ReadLine{Text: l.Text, Box: boxOf(l.Box), Score: l.Score})
	}
	for _, e := range els {
		rd.Found = append(rd.Found, ReadElement{ID: e.ID, Key: e.Key, Kind: string(e.Kind), Text: e.Text, Box: boxOf(e.Box), Score: e.Score})
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

// doAll performs actions in order with a settle pause after each.
func (r *Runner) doAll(ctx context.Context, m Match, actions []Action) error {
	for j, a := range actions {
		if err := r.Do(ctx, m, a); err != nil {
			return fmt.Errorf("action %d: %w", j+1, err)
		}
		if err := sleepCtx(ctx, r.settle()); err != nil {
			return err
		}
	}
	return nil
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
