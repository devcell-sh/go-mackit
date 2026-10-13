package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-textshot"
	"github.com/stretchr/testify/require"
)

// fakeScreen serves a sequence of OCR results; the last one repeats. By
// default the next frame is served on every capture; with onInput set the
// frame only advances when the fake input acts (a UI that reacts to
// clicks). Each frame is a distinct image so the OCR fake can map it.
type fakeScreen struct {
	frames  [][]textshot.TextResult
	i       int
	onInput bool
	byImg   map[image.Image][]textshot.TextResult
	// styles overrides the Style of elements by text ("Install": disabled).
	styles map[string]textshot.Style
	// textCalls and screenCalls count model runs.
	textCalls, screenCalls int
}

func newFakeScreen(frames ...[]textshot.TextResult) *fakeScreen {
	return &fakeScreen{frames: frames, byImg: map[image.Image][]textshot.TextResult{}, styles: map[string]textshot.Style{}}
}

// Capture paints the frame index into a block of the image so that
// textshot.Diff sees a change whenever the frame advances (a different
// frame with identical lines still counts as a new capture).
func (f *fakeScreen) Capture(context.Context) (image.Image, error) {
	idx := min(f.i, len(f.frames)-1)
	frame := f.frames[idx]
	if !f.onInput {
		f.i++
	}
	img := image.NewRGBA(hd)
	c := color.RGBA{R: uint8(idx * 40), G: uint8(f.i * 40), B: 200, A: 255}
	draw.Draw(img, image.Rect(0, 0, 200, 200), &image.Uniform{c}, image.Point{}, draw.Src)
	f.byImg[img] = frame
	return img, nil
}

func (f *fakeScreen) advance() {
	if f.onInput {
		f.i++
	}
}

func (f *fakeScreen) DetectText(_ context.Context, img image.Image) ([]textshot.TextResult, error) {
	f.textCalls++
	return f.byImg[img], nil
}

// DetectScreen turns the frame's lines into text elements (buttons for
// Continue/Agree/Install), plus a detected interactable for the Utilities
// menu label so snapping is exercised, and a Disk Utility row icon.
func (f *fakeScreen) DetectScreen(_ context.Context, img image.Image, lines []textshot.TextResult) (*textshot.Screen, error) {
	f.screenCalls++
	scr := &textshot.Screen{Size: img.Bounds().Size(), Theme: textshot.ThemeLight}
	for _, l := range lines {
		kind := textshot.KindText
		switch l.Text {
		case "Continue", "Agree", "Install":
			kind = textshot.KindButton
		}
		scr.Elements = append(scr.Elements, textshot.Element{Key: string(kind) + ":" + l.Text, Kind: kind, Box: l.Box, Text: l.Text, Score: l.Score, Style: f.styles[l.Text]})
	}
	if len(lines) > 0 && lines[0].Text == utilitiesLines[0].Text {
		scr.Elements = append(scr.Elements,
			textshot.Element{Key: "icon@262,3", Kind: textshot.KindIcon, Box: image.Rect(262, 3, 311, 20), Score: 0.5},
			textshot.Element{Key: "icon:Disk Utility", Kind: textshot.KindIcon, Box: image.Rect(700, 500, 740, 540), Text: "Disk Utility", Score: 0.6},
		)
	}
	return scr, nil
}

type fakeInput struct {
	log    []string
	screen *fakeScreen
	// onInput runs after each input, to change styles like a real UI.
	onInput func()
}

func (f *fakeInput) record(s string) error {
	f.log = append(f.log, s)
	if f.screen != nil {
		f.screen.advance()
	}
	if f.onInput != nil {
		f.onInput()
	}
	return nil
}

func (f *fakeInput) SendKeys(_ context.Context, chord ...string) error {
	return f.record(fmt.Sprintf("keys%v", chord))
}

func (f *fakeInput) Click(_ context.Context, pt image.Point, display image.Rectangle) error {
	return f.record(fmt.Sprintf("click(%d,%d)/%dx%d", pt.X, pt.Y, display.Dx(), display.Dy()))
}

func (f *fakeInput) Type(_ context.Context, text string) error {
	return f.record("type:" + text)
}

func newRunner(screen *fakeScreen, in *fakeInput) *Runner {
	return &Runner{
		Capture: screen.Capture, OCR: screen, Input: in, Catalog: Catalina,
		Interval: time.Millisecond, SettleDelay: time.Millisecond, DefaultTimeout: 200 * time.Millisecond,
	}
}

func TestWaitForAcrossFrames(t *testing.T) {
	screen := newFakeScreen(nil, loadingLines, loadingLines, utilitiesLines)
	r := newRunner(screen, &fakeInput{})
	m, err := r.WaitFor(context.Background(), "recovery.utilities", nil, time.Second)
	require.NoError(t, err)
	require.Equal(t, "recovery.utilities", m.Name())
	require.Equal(t, 4, screen.i, "four captures until the utilities frame")
}

func TestWaitForTimeoutReportsLastSeen(t *testing.T) {
	r := newRunner(newFakeScreen(loadingLines), &fakeInput{})
	_, err := r.WaitFor(context.Background(), "recovery.utilities", nil, 20*time.Millisecond)
	require.ErrorIs(t, err, ErrTimeout)
	require.ErrorContains(t, err, `last seen "recovery.loading"`)

	_, err = r.WaitFor(context.Background(), "not.a.screen", nil, time.Second)
	require.ErrorContains(t, err, "not in the catalina catalog")
}

func TestWaitForRequiresElements(t *testing.T) {
	withMenu := append(append([]textshot.TextResult{}, utilitiesLines...),
		textshot.TextResult{Box: image.Rect(270, 60, 340, 80), Text: "Terminal"})
	screen := newFakeScreen(utilitiesLines, utilitiesLines, withMenu)
	r := newRunner(screen, &fakeInput{})
	m, err := r.WaitFor(context.Background(), "recovery.utilities", []string{"terminal_item"}, time.Second)
	require.NoError(t, err)
	require.Equal(t, 3, screen.i)
	_, found := m.Find("terminal_item")
	require.True(t, found)
	require.NotNil(t, m.Elements, "waiting for selectors runs the element detector")
}

func TestRunExecutesActionsAndSkipsOptional(t *testing.T) {
	withMenu := append(append([]textshot.TextResult{}, utilitiesLines...),
		textshot.TextResult{Box: image.Rect(270, 60, 340, 80), Text: "Terminal"})
	terminal := []textshot.TextResult{{Box: image.Rect(0, 0, 420, 22), Text: "Terminal Shell Edit View Window Help"}}
	// The UI only changes in response to input: utilities -> menu open ->
	// terminal. The optional picker step must time out without consuming
	// frames.
	screen := newFakeScreen(utilitiesLines, withMenu, terminal)
	screen.onInput = true
	in := &fakeInput{screen: screen}
	r := newRunner(screen, in)

	steps := append([]Step{
		{Name: "picker", Screen: "opencore.picker", Timeout: 10 * time.Millisecond, Optional: true, Actions: []Action{Keys("ret")}},
	}, OpenTerminal...)
	steps[len(steps)-1].Timeout = time.Second
	steps = append(steps, Step{Screen: "recovery.terminal", Actions: []Action{
		Type("ls\n"), Keys("tab", "ret"), Chord("ctrl", "c"), ClickAt(5, 6), Sleep(time.Millisecond),
	}})
	require.NoError(t, r.Run(context.Background(), steps))

	require.Len(t, in.log, 7)
	require.Equal(t, "click(286,11)/1920x1080", in.log[0], "utilities menu snapped to the detected menu label")
	require.Equal(t, "click(305,70)/1920x1080", in.log[1])
	require.Equal(t, []string{"type:ls\n", "keys[tab]", "keys[ret]", "keys[ctrl c]", "click(5,6)/1920x1080"}, in.log[2:])
}

func TestRunFailsOnMissingElementAndRequiredScreen(t *testing.T) {
	in := &fakeInput{}
	r := newRunner(newFakeScreen(utilitiesLines), in)
	err := r.Run(context.Background(), []Step{{Screen: "recovery.utilities", Actions: []Action{Click("terminal_item")}}})
	require.ErrorContains(t, err, `element "terminal_item" not found`)
	require.Empty(t, in.log)

	err = r.Run(context.Background(), []Step{{Name: "boot", Screen: "recovery.terminal", Timeout: 10 * time.Millisecond}})
	require.ErrorIs(t, err, ErrTimeout)
	require.ErrorContains(t, err, "boot:")
}

func TestRunHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newRunner(newFakeScreen(loadingLines), &fakeInput{})
	_, err := r.WaitFor(ctx, "recovery.utilities", nil, time.Second)
	require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout))
}

func TestRunReactsToPickerWhileWaiting(t *testing.T) {
	picker := []textshot.TextResult{{Box: image.Rect(10, 10, 300, 30), Text: "macOS Base System"}}
	// Picker shows until Enter is pressed, then loading, then utilities.
	screen := newFakeScreen(picker, loadingLines, utilitiesLines)
	screen.onInput = true
	in := &fakeInput{screen: screen}
	r := newRunner(screen, in)

	// Advance loading -> utilities without input after a few reads.
	reads := 0
	capture := screen.Capture
	r.Capture = func(ctx context.Context) (image.Image, error) {
		reads++
		if reads == 4 {
			screen.i++
		}
		return capture(ctx)
	}
	err := r.Run(context.Background(), []Step{{
		Screen:    "recovery.utilities",
		Timeout:   time.Second,
		Reactions: []Reaction{{Screen: "opencore.picker", Actions: []Action{Keys("ret")}}},
	}})
	require.NoError(t, err)
	require.Equal(t, []string{"keys[ret]"}, in.log, "Enter pressed exactly once, on the picker")
}

func TestRunClicksBySelector(t *testing.T) {
	installer := []textshot.TextResult{
		{Box: image.Rect(700, 200, 1200, 240), Text: "Install macOS Catalina"},
		{Box: image.Rect(1100, 600, 1180, 624), Text: "Continue"},
	}
	screen := newFakeScreen(utilitiesLines, installer)
	screen.onInput = true
	in := &fakeInput{screen: screen}
	r := newRunner(screen, in)
	steps := []Step{
		{Name: "reinstall", Screen: "recovery.utilities", Elements: []string{"text:Reinstall macOS"}, Actions: []Action{Click("text:Reinstall macOS")}},
		{Name: "continue", Elements: []string{"button:Continue"}, Actions: []Action{Click("button:Continue")}},
	}
	require.NoError(t, r.Run(context.Background(), steps))
	require.Equal(t, []string{"click(804,355)/1920x1080", "click(1140,612)/1920x1080"}, in.log)
}

func TestObserveSavesFramesInShotDir(t *testing.T) {
	screen := newFakeScreen(nil, utilitiesLines)
	r := newRunner(screen, &fakeInput{})
	r.ShotDir = filepath.Join(t.TempDir(), "shots")
	for range 2 {
		_, _, err := r.Observe(context.Background(), false)
		require.NoError(t, err)
	}
	files, err := filepath.Glob(filepath.Join(r.ShotDir, "*.png"))
	require.NoError(t, err)
	require.Len(t, files, 2)
	names := filepath.Base(files[0]) + " " + filepath.Base(files[1])
	require.Regexp(t, `\d{8}T\d{6}Z-unknown-0001\.png`, names)
	require.Regexp(t, `\d{8}T\d{6}Z-recovery\.utilities-0002\.png`, names)
}

func TestObserveSavesReadingsInOCRDir(t *testing.T) {
	screen := newFakeScreen(nil, utilitiesLines)
	r := newRunner(screen, &fakeInput{})
	r.ShotDir = filepath.Join(t.TempDir(), "shots")
	r.OCRDir = filepath.Join(t.TempDir(), "ocr")
	for range 2 {
		_, _, err := r.Observe(context.Background(), false)
		require.NoError(t, err)
	}
	files, err := filepath.Glob(filepath.Join(r.OCRDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 2)
	names := filepath.Base(files[0]) + " " + filepath.Base(files[1])
	require.Regexp(t, `\d{8}T\d{6}Z-unknown-0001\.json`, names)
	require.Regexp(t, `\d{8}T\d{6}Z-recovery\.utilities-0002\.json`, names)

	var found *Reading
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		var rd Reading
		require.NoError(t, json.Unmarshal(data, &rd))
		if rd.Screen == "recovery.utilities" {
			found = &rd
			// The frame PNG shares the base name with the reading.
			require.Equal(t, strings.TrimSuffix(filepath.Base(f), ".json")+".png", filepath.Base(rd.Frame))
			require.FileExists(t, rd.Frame)
		}
	}
	require.NotNil(t, found, "utilities reading saved")
	require.False(t, found.Elements)
	require.Len(t, found.Lines, len(utilitiesLines))
	require.Equal(t, utilitiesLines[0].Text, found.Lines[0].Text)
	require.Equal(t, boxOf(utilitiesLines[0].Box), found.Lines[0].Box)
}

func TestObserveReusesReadingForUnchangedFrame(t *testing.T) {
	screen := newFakeScreen(utilitiesLines)
	screen.onInput = true // the frame never advances, so every capture looks the same
	r := newRunner(screen, &fakeInput{})
	for range 3 {
		m, ok, err := r.Observe(context.Background(), false)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "recovery.utilities", m.Name())
	}
	require.Equal(t, 1, screen.textCalls, "OCR runs once for three identical frames")
	require.Equal(t, 0, screen.screenCalls)

	// Elements asked for on the same frame: the detector runs once, then
	// the reading with elements is reused.
	for range 2 {
		m, _, err := r.Observe(context.Background(), true)
		require.NoError(t, err)
		require.NotNil(t, m.Elements)
	}
	require.Equal(t, 1, screen.textCalls)
	require.Equal(t, 1, screen.screenCalls)
}

func TestWaitForSkipsDetectorUntilScreenMatches(t *testing.T) {
	screen := newFakeScreen(loadingLines, loadingLines, introLines, introLines, utilitiesLines)
	r := newRunner(screen, &fakeInput{})
	_, err := r.WaitFor(context.Background(), "recovery.utilities", []string{"text:Reinstall macOS"}, time.Second)
	require.NoError(t, err)
	require.Equal(t, 5, screen.textCalls, "text read on every changed frame")
	require.Equal(t, 1, screen.screenCalls, "detector only on the utilities frame")

	// Without a screen name, the selector's text gates the detector.
	screen = newFakeScreen(loadingLines, utilitiesLines, introLines)
	r = newRunner(screen, &fakeInput{})
	_, err = r.WaitFor(context.Background(), "", []string{"button:Continue"}, time.Second)
	require.NoError(t, err)
	require.Equal(t, 1, screen.screenCalls, "detector only once Continue is among the lines")
}

func TestFindSkipsDisabledButtonAndExplains(t *testing.T) {
	screen := newFakeScreen(targetLines)
	screen.styles["Install"] = textshot.Style{Disabled: true, Contrast: 1.4}
	r := newRunner(screen, &fakeInput{})
	m, _, err := r.Observe(context.Background(), true)
	require.NoError(t, err)
	_, ok := m.Find("button:Install")
	require.False(t, ok, "a disabled button does not resolve")
	require.Contains(t, m.Explain("button:Install"), "disabled")
	require.Empty(t, m.Explain("button:Back"), "absent elements have no explanation")

	_, err = r.WaitFor(context.Background(), "installer.target", []string{"button:Install"}, 20*time.Millisecond)
	require.ErrorIs(t, err, ErrTimeout)
	require.Contains(t, err.Error(), `"button:Install" is on screen but disabled`)

	screen.styles["Install"] = textshot.Style{Contrast: 9}
	screen.i++ // a new frame, so the reading is refreshed
	m, _, err = r.Observe(context.Background(), true)
	require.NoError(t, err)
	_, ok = m.Find("button:Install")
	require.True(t, ok)
}

func TestClickDefaultButtonPressesEnter(t *testing.T) {
	screen := newFakeScreen(introLines)
	screen.styles["Continue"] = textshot.Style{Highlighted: true, Contrast: 8}
	in := &fakeInput{}
	r := newRunner(screen, in)
	r.EnterOnDefault = true
	require.NoError(t, r.Run(context.Background(), []Step{{Elements: []string{"button:Continue"}, Actions: []Action{Click("button:Continue")}}}))
	require.Equal(t, []string{"keys[ret]"}, in.log)

	r.EnterOnDefault = false
	in.log = nil
	screen.i = 0
	require.NoError(t, r.Run(context.Background(), []Step{{Elements: []string{"button:Continue"}, Actions: []Action{Click("button:Continue")}}}))
	require.Len(t, in.log, 1)
	require.Contains(t, in.log[0], "click(")
}

func TestFindByTextshotKey(t *testing.T) {
	screen := newFakeScreen(utilitiesLines)
	r := newRunner(screen, &fakeInput{})
	m, _, err := r.Observe(context.Background(), true)
	require.NoError(t, err)
	box, ok := m.Find("icon:Disk Utility")
	require.True(t, ok)
	require.Equal(t, image.Rect(700, 500, 740, 540), box)
	_, ok = m.Find("icon:Nothing")
	require.False(t, ok)
	_, ok = m.Find("icon@262,3")
	require.True(t, ok)
}

func TestSettleWaitsUntilFramesStopChanging(t *testing.T) {
	// Each capture paints a new frame index while the fake advances, so
	// the settle loop sees change until the last frame repeats.
	screen := newFakeScreen(utilitiesLines, utilitiesLines, utilitiesLines, utilitiesLines)
	in := &fakeInput{}
	r := newRunner(screen, in)
	r.SettleTimeout = time.Second
	m, _, err := r.Observe(context.Background(), false)
	require.NoError(t, err)
	before := screen.i
	require.NoError(t, r.doAll(context.Background(), m, []Action{Keys("ret")}))
	require.GreaterOrEqual(t, screen.i-before, 2, "at least two captures to see the screen settle")
}

// TestClickRereadsScreenWhenTargetIsStale is the Utilities page: Continue
// is disabled until a row is selected, so the second click of the step
// must act on the screen after the first click, not on the frame the
// step was matched against.
func TestClickRereadsScreenWhenTargetIsStale(t *testing.T) {
	screen := newFakeScreen(utilitiesLines, utilitiesLines)
	screen.onInput = true
	screen.styles["Continue"] = textshot.Style{Disabled: true, Contrast: 1.8}
	in := &fakeInput{screen: screen}
	in.onInput = func() { screen.styles["Continue"] = textshot.Style{Contrast: 9} }
	r := newRunner(screen, in)
	err := r.Run(context.Background(), []Step{{
		Screen:   "recovery.utilities",
		Elements: []string{"text:Reinstall macOS"},
		Actions:  []Action{Click("text:Reinstall macOS"), Click("button:Continue")},
	}})
	require.NoError(t, err)
	require.Len(t, in.log, 2)
	require.Contains(t, in.log[1], "click(")
	require.Equal(t, 2, screen.screenCalls, "detector re-ran once for the stale target")
}

// driftScreen paints a little more of the frame on every capture, each
// increment under the change threshold on its own: Terminal printing
// lines one at a time, a progress bar creeping. The reading must be
// refreshed once the changes add up against the last frame actually read.
type driftScreen struct {
	fakeScreen
	n int
}

func (d *driftScreen) Capture(context.Context) (image.Image, error) {
	d.n++
	img := image.NewRGBA(hd)
	// 30x30 = 900 px per step, below DefaultChangeThreshold (1036 px of 1920x1080).
	draw.Draw(img, image.Rect(0, 0, 30*d.n, 30), image.White, image.Point{}, draw.Src)
	frame := d.frames[min(d.n-1, len(d.frames)-1)]
	d.byImg[img] = frame
	return img, nil
}

func TestObserveRereadsWhenSmallChangesAddUp(t *testing.T) {
	screen := &driftScreen{fakeScreen: *newFakeScreen(terminalLines, terminalLines, terminalEraseLines)}
	shots := t.TempDir()
	r := newRunner(&screen.fakeScreen, &fakeInput{})
	r.Capture = screen.Capture
	r.OCR = screen
	r.ShotDir = shots

	m, _, err := r.Observe(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, screen.textCalls)
	require.NotContains(t, strings.ToLower(m.Text()), "finished erase")

	// Second capture differs from the first by 900 px: reused, and no
	// frame saved for it.
	m, _, err = r.Observe(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, screen.textCalls, "a change under the threshold reuses the reading")
	require.NotContains(t, strings.ToLower(m.Text()), "finished erase")

	// Third capture differs from the first (the last read frame) by 1800
	// px even though it differs from the second by only 900: read again.
	m, _, err = r.Observe(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 2, screen.textCalls, "drift against the last read frame triggers a new read")
	require.Contains(t, strings.ToLower(m.Text()), "finished erase")

	entries, err := os.ReadDir(shots)
	require.NoError(t, err)
	require.Len(t, entries, 2, "one frame per model run, none for the reused reading")
}

// progressLines is the installer's progress page right after Install.
var progressLines = []textshot.TextResult{
	{Text: "Install macOS Catalina Edit Window", Box: image.Rect(0, 0, 400, 20)},
	{Text: "macOS Catalina will be installed on the disk \"Macintosh HD\".", Box: image.Rect(760, 440, 1160, 458)},
	{Text: "About 19 minutes remaining", Box: image.Rect(880, 630, 1040, 650)},
}

func TestStepDoneOnWhenUIMovesOn(t *testing.T) {
	// Waiting for the target page's Install button, but the installer has
	// already gone to its progress page: the step ends without clicking.
	screen := newFakeScreen(progressLines)
	in := &fakeInput{}
	r := newRunner(screen, in)
	steps := []Step{{
		Name: "install", Screen: "installer.target", Elements: []string{"button:Install"},
		Timeout: time.Second, DoneOn: []string{"installer.progress"},
		Actions: []Action{Click("button:Install")},
	}}
	require.NoError(t, r.Run(context.Background(), steps))
	require.Empty(t, in.log, "no click when the UI moved on")

	// An unknown done-on screen is a flow bug.
	steps[0].DoneOn = []string{"installer.done"}
	require.ErrorContains(t, r.Run(context.Background(), steps), `done-on screen "installer.done"`)
}
