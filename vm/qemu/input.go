package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"strings"
	"time"
)

// Screendump captures the guest display through QMP and decodes it. QEMU
// writes a PPM to a temp file (the socket and this process share a
// filesystem), which is removed after decoding.
func Screendump(sock string) (image.Image, error) {
	f, err := os.CreateTemp("", "mackit-screendump-*.ppm")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	if err := QMPScreendump(sock, path); err != nil {
		return nil, err
	}
	r, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return decodePPM(r)
}

// QMPSendKeys presses the given key codes together (a chord) and releases
// them after QEMU's default hold time.
func QMPSendKeys(sock string, qcodes ...string) error {
	if len(qcodes) == 0 {
		return nil
	}
	keys := make([]map[string]string, len(qcodes))
	for i, k := range qcodes {
		keys[i] = map[string]string{"type": "qcode", "data": k}
	}
	args, _ := json.Marshal(map[string]any{"keys": keys})
	_, err := qmpCommand(sock, fmt.Sprintf(`{"execute": "send-key", "arguments": %s}`, args))
	return err
}

// absMax is QEMU's INPUT_EVENT_ABS_MAX: absolute pointer coordinates are
// scaled to 0..32767 regardless of the display size.
const absMax = 32767

// Device ids of the USB input devices on the QEMU command line.
//
// Pointer events are deliberately not addressed by device: QEMU 11.1
// aborts ("Property 'qemu-fixed-text-console.device' not found") when
// input-send-event carries a device argument. Instead the tablet is made
// the active pointer with the monitor's mouse_set, because QEMU routes
// button events to the active mouse (the PS/2 mouse by default on q35)
// while absolute moves always go to the tablet.
const (
	USBKeyboardID = "kbd0"
	USBTabletID   = "tablet0"
)

// QMPActivateTablet makes the USB tablet the active pointer so that
// button events reach it. Idempotent; cheap enough to call before every
// click.
func QMPActivateTablet(sock string) error {
	out, err := QMPHumanMonitor(sock, "info mice")
	if err != nil {
		return err
	}
	idx, active, ok := tabletIndex(out)
	if !ok {
		return fmt.Errorf("no tablet in QEMU input devices: %q", strings.TrimSpace(out))
	}
	if active {
		return nil
	}
	_, err = QMPHumanMonitor(sock, "mouse_set "+idx)
	return err
}

// tabletIndex parses "info mice" output ("* Mouse #2: QEMU PS/2 Mouse",
// "  Mouse #4: QEMU HID Tablet (absolute)") and returns the tablet's index
// and whether it is already the active ("*") pointer.
func tabletIndex(infoMice string) (idx string, active, ok bool) {
	for _, line := range strings.Split(infoMice, "\n") {
		if !strings.Contains(line, "Tablet") {
			continue
		}
		i := strings.Index(line, "#")
		if i < 0 {
			continue
		}
		idx = strings.TrimSuffix(strings.Fields(line[i+1:])[0], ":")
		return idx, strings.HasPrefix(strings.TrimSpace(line), "*"), true
	}
	return "", false, false
}

// QMPHumanMonitor runs a legacy monitor (HMP) command over QMP and returns
// its text output.
func QMPHumanMonitor(sock, command string) (string, error) {
	args, _ := json.Marshal(map[string]any{"command-line": command})
	raw, err := qmpCommand(sock, fmt.Sprintf(`{"execute": "human-monitor-command", "arguments": %s}`, args))
	if err != nil {
		return "", err
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parsing HMP output: %w", err)
	}
	return out, nil
}

// QMPMouseMove moves the absolute pointer (usb-tablet) to pt within a
// display of the given bounds.
func QMPMouseMove(sock string, pt image.Point, display image.Rectangle) error {
	w, h := display.Dx(), display.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("invalid display bounds %v", display)
	}
	x := clampInt((pt.X-display.Min.X)*absMax/w, 0, absMax)
	y := clampInt((pt.Y-display.Min.Y)*absMax/h, 0, absMax)
	return qmpInputEvents(sock, []map[string]any{
		{"type": "abs", "data": map[string]any{"axis": "x", "value": x}},
		{"type": "abs", "data": map[string]any{"axis": "y", "value": y}},
	})
}

// QMPMouseClick presses and releases a pointer button ("left", "right",
// "middle") at the current pointer position. Press and release are
// separate commands on purpose: QEMU coalesces the pointer events of one
// input-send-event into a single HID report, so a press and release in
// the same batch reach the guest as just "button up".
func QMPMouseClick(sock, button string) error {
	if err := QMPMouseButton(sock, button, true); err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
	return QMPMouseButton(sock, button, false)
}

// QMPMouseButton presses (down=true) or releases a pointer button.
func QMPMouseButton(sock, button string, down bool) error {
	return qmpInputEvents(sock, []map[string]any{
		{"type": "btn", "data": map[string]any{"down": down, "button": button}},
	})
}

func qmpInputEvents(sock string, events []map[string]any) error {
	args, _ := json.Marshal(map[string]any{"events": events})
	_, err := qmpCommand(sock, fmt.Sprintf(`{"execute": "input-send-event", "arguments": %s}`, args))
	return err
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// QMPInput drives the guest keyboard and pointer over QMP. It satisfies
// the installer flow's Input interface.
type QMPInput struct {
	Sock string
	// KeyDelay is the pause between successive key presses when typing
	// (default 60ms). QEMU holds each key for 100ms on its own.
	KeyDelay time.Duration
}

func (q QMPInput) delay() time.Duration {
	if q.KeyDelay > 0 {
		return q.KeyDelay
	}
	return 60 * time.Millisecond
}

// SendKeys presses one chord.
func (q QMPInput) SendKeys(ctx context.Context, chord ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return QMPSendKeys(q.Sock, chord...)
}

// Click moves the pointer to pt (display pixel coordinates) and left-clicks.
func (q QMPInput) Click(ctx context.Context, pt image.Point, display image.Rectangle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := QMPActivateTablet(q.Sock); err != nil {
		return err
	}
	if err := QMPMouseMove(q.Sock, pt, display); err != nil {
		return err
	}
	if err := sleepCtx(ctx, q.delay()); err != nil {
		return err
	}
	return QMPMouseClick(q.Sock, "left")
}

// Type sends text as individual key presses.
func (q QMPInput) Type(ctx context.Context, text string) error {
	chords, err := KeysForText(text)
	if err != nil {
		return err
	}
	for i, chord := range chords {
		if err := q.SendKeys(ctx, chord...); err != nil {
			return fmt.Errorf("typing %q at %d: %w", strings.TrimSpace(text), i, err)
		}
		if err := sleepCtx(ctx, q.delay()+100*time.Millisecond); err != nil {
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
