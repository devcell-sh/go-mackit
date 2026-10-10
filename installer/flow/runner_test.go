package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
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
}

func newFakeScreen(frames ...[]textshot.TextResult) *fakeScreen {
	return &fakeScreen{frames: frames, byImg: map[image.Image][]textshot.TextResult{}}
}

func (f *fakeScreen) Capture(context.Context) (image.Image, error) {
	frame := f.frames[min(f.i, len(f.frames)-1)]
	if !f.onInput {
		f.i++
	}
	img := image.NewRGBA(hd)
	f.byImg[img] = frame
	return img, nil
}

func (f *fakeScreen) advance() {
	if f.onInput {
		f.i++
	}
}

func (f *fakeScreen) DetectText(_ context.Context, img image.Image) ([]textshot.TextResult, error) {
	return f.byImg[img], nil
}

// DetectElements turns the frame's lines into text elements, plus a
// detected interactable for the Utilities menu label so snapping is
// exercised.
func (f *fakeScreen) DetectElements(_ context.Context, img image.Image) ([]textshot.Element, error) {
	var out []textshot.Element
	for _, l := range f.byImg[img] {
		kind := textshot.KindText
		if l.Text == "Continue" {
			kind = textshot.KindButton
		}
		out = append(out, textshot.Element{Key: string(kind) + ":" + l.Text, Kind: kind, Box: l.Box, Text: l.Text, Score: l.Score})
	}
	if len(f.byImg[img]) > 0 && f.byImg[img][0].Text == utilitiesLines[0].Text {
		out = append(out, textshot.Element{Key: "icon@262,3", Kind: textshot.KindIcon, Box: image.Rect(262, 3, 311, 20), Score: 0.5})
	}
	return out, nil
}

type fakeInput struct {
	log    []string
	screen *fakeScreen
}

func (f *fakeInput) record(s string) error {
	f.log = append(f.log, s)
	if f.screen != nil {
		f.screen.advance()
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
