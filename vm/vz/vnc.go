//go:build darwin && arm64

package vz

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Virtualization -framework Foundation
#include <stdio.h>
#include <Virtualization/Virtualization.h>
#include <objc/runtime.h>
#include <objc/message.h>

// _VZVNCServer is a private Virtualization.framework class that exposes the
// VM's framebuffer over VNC. Tart (cirruslabs/tart) uses it in production.
// We access it via the ObjC runtime to avoid importing private headers.

static int startVNCServer(void *vmPtr) {
    if (!vmPtr) return -4;
    fprintf(stderr, "VNC: vmPtr=%p\n", vmPtr);
    id vm = (__bridge id)vmPtr;
    fprintf(stderr, "VNC: vm=%p class=%s\n", vm, class_getName(object_getClass(vm)));

    Class cls = objc_getClass("_VZVNCServer");
    if (!cls) return -1;
    fprintf(stderr, "VNC: _VZVNCServer class found\n");

    id server = ((id (*)(Class, SEL))objc_msgSend)(cls, sel_registerName("alloc"));
    if (!server) return -6;
    fprintf(stderr, "VNC: alloc ok=%p\n", server);

    dispatch_queue_t queue = dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT, 0);
    fprintf(stderr, "VNC: calling initWithPort:queue:securityConfiguration:\n");
    server = ((id (*)(id, SEL, NSInteger, dispatch_queue_t, id))objc_msgSend)(
        server, sel_registerName("initWithPort:queue:securityConfiguration:"),
        0, queue, nil
    );
    if (!server) return -2;
    fprintf(stderr, "VNC: init ok=%p\n", server);

    fprintf(stderr, "VNC: calling setVirtualMachine:\n");
    ((void (*)(id, SEL, id))objc_msgSend)(server, sel_registerName("setVirtualMachine:"), vm);
    fprintf(stderr, "VNC: setVM ok\n");

    fprintf(stderr, "VNC: calling start\n");
    ((void (*)(id, SEL))objc_msgSend)(server, sel_registerName("start"));
    fprintf(stderr, "VNC: start ok, polling port\n");

    for (int i = 0; i < 100; i++) {
        NSInteger port = ((NSInteger (*)(id, SEL))objc_msgSend)(server, sel_registerName("port"));
        if (port > 0) return (int)port;
        usleep(50000);
    }
    return -3;
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/Code-Hex/vz/v3"

	"github.com/devcell-sh/go-mackit/vm"
)

// StartVNCServer starts a VNC server on a running VZ VM and returns the port.
// The vm.VM must have been created by this package's Backend.
func StartVNCServer(v vm.VM) (int, error) {
	h, ok := v.(*vmHandle)
	if !ok {
		return 0, fmt.Errorf("StartVNCServer requires a VZ vm handle, got %T", v)
	}
	return VNCPort(h.machine)
}

// vmObjcPtr extracts the underlying ObjC id from a *vz.VirtualMachine.
// Code-Hex/vz stores it as an embedded *pointer (= *objc.Pointer) whose
// sole field _ptr is the ObjC id. The struct layout (v3.8.0):
//
//	type VirtualMachine struct {
//	    id        string       // offset 0  (16 bytes)
//	    supported bool         // offset 16 (padded to 24)
//	    *pointer               // offset 24 (8 bytes, points to objc.Pointer{_ptr})
//	    ...
//	}
func vmObjcPtr(machine *vz.VirtualMachine) unsafe.Pointer {
	type objcPointer struct{ ptr unsafe.Pointer }
	type vmHeader struct {
		_   string
		_   bool
		ptr *objcPointer
	}
	h := (*vmHeader)(unsafe.Pointer(machine))
	if h.ptr == nil {
		return nil
	}
	return h.ptr.ptr
}

// VNCPort starts a VNC server on the VM and returns the port it's listening on.
func VNCPort(machine *vz.VirtualMachine) (int, error) {
	ptr := vmObjcPtr(machine)
	if ptr == nil {
		return 0, fmt.Errorf("VirtualMachine has nil ObjC pointer")
	}
	port := C.startVNCServer(ptr)
	runtime.KeepAlive(machine)
	if port < 0 {
		return 0, fmt.Errorf("failed to start VNC server (code %d)", port)
	}
	return int(port), nil
}

// VNCClient connects to a VNC server and provides screen capture and input.
type VNCClient struct {
	conn    net.Conn
	width   uint16
	height  uint16
	fb      *image.RGBA
	mu      sync.Mutex
	closed  bool
	logger  *slog.Logger
}

// DialVNC connects to a VNC server (RFB protocol) and completes the handshake.
// An optional logger receives connection and capture events.
func DialVNC(addr string, logger ...*slog.Logger) (*VNCClient, error) {
	var log *slog.Logger
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0].With("component", "vnc")
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}

	// RFB version handshake
	var version [12]byte
	if _, err := io.ReadFull(conn, version[:]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading server version: %w", err)
	}
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		conn.Close()
		return nil, err
	}

	// Security handshake
	var numSec [1]byte
	if _, err := io.ReadFull(conn, numSec[:]); err != nil {
		conn.Close()
		return nil, err
	}
	if numSec[0] == 0 {
		conn.Close()
		return nil, fmt.Errorf("server rejected connection")
	}
	secTypes := make([]byte, numSec[0])
	if _, err := io.ReadFull(conn, secTypes); err != nil {
		conn.Close()
		return nil, err
	}
	// Choose None (1) authentication
	hasNone := false
	for _, t := range secTypes {
		if t == 1 {
			hasNone = true
			break
		}
	}
	if !hasNone {
		conn.Close()
		return nil, fmt.Errorf("server does not support no-auth (types: %v)", secTypes)
	}
	if _, err := conn.Write([]byte{1}); err != nil {
		conn.Close()
		return nil, err
	}

	// SecurityResult
	var result [4]byte
	if _, err := io.ReadFull(conn, result[:]); err != nil {
		conn.Close()
		return nil, err
	}
	if binary.BigEndian.Uint32(result[:]) != 0 {
		conn.Close()
		return nil, fmt.Errorf("security handshake failed")
	}

	// ClientInit: shared=1
	if _, err := conn.Write([]byte{1}); err != nil {
		conn.Close()
		return nil, err
	}

	// ServerInit
	var header [24]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		conn.Close()
		return nil, err
	}
	width := binary.BigEndian.Uint16(header[0:2])
	height := binary.BigEndian.Uint16(header[2:4])
	nameLen := binary.BigEndian.Uint32(header[20:24])
	name := make([]byte, nameLen)
	if _, err := io.ReadFull(conn, name); err != nil {
		conn.Close()
		return nil, err
	}

	// Set pixel format to 32-bit RGBA
	setPixelFormat := make([]byte, 20)
	setPixelFormat[0] = 0 // SetPixelFormat
	setPixelFormat[4] = 32 // bits per pixel
	setPixelFormat[5] = 24 // depth
	setPixelFormat[6] = 0  // big-endian: no
	setPixelFormat[7] = 1  // true-color: yes
	binary.BigEndian.PutUint16(setPixelFormat[8:10], 255)  // red-max
	binary.BigEndian.PutUint16(setPixelFormat[10:12], 255) // green-max
	binary.BigEndian.PutUint16(setPixelFormat[12:14], 255) // blue-max
	setPixelFormat[14] = 0  // red-shift
	setPixelFormat[15] = 8  // green-shift
	setPixelFormat[16] = 16 // blue-shift
	if _, err := conn.Write(setPixelFormat); err != nil {
		conn.Close()
		return nil, err
	}

	// Set encodings: Raw (0)
	setEncodings := make([]byte, 8)
	setEncodings[0] = 2 // SetEncodings
	binary.BigEndian.PutUint16(setEncodings[2:4], 1) // 1 encoding
	binary.BigEndian.PutUint32(setEncodings[4:8], 0) // Raw
	if _, err := conn.Write(setEncodings); err != nil {
		conn.Close()
		return nil, err
	}

	c := &VNCClient{
		conn:   conn,
		width:  width,
		height: height,
		fb:     image.NewRGBA(image.Rect(0, 0, int(width), int(height))),
		logger: log,
	}
	if log != nil {
		log.Info("VNC connected", "addr", addr, "width", width, "height", height, "name", string(name))
	}
	return c, nil
}

// Capture requests a full framebuffer update and returns the current screen.
func (c *VNCClient) Capture(ctx context.Context) (image.Image, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("VNC connection closed")
	}

	// FramebufferUpdateRequest: incremental=0 (full), x=0, y=0, w, h
	req := make([]byte, 10)
	req[0] = 3 // FramebufferUpdateRequest
	req[1] = 0 // non-incremental
	binary.BigEndian.PutUint16(req[6:8], c.width)
	binary.BigEndian.PutUint16(req[8:10], c.height)
	if _, err := c.conn.Write(req); err != nil {
		return nil, err
	}

	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.conn.SetReadDeadline(time.Time{})

	// Read FramebufferUpdate response
	for {
		var msgType [1]byte
		if _, err := io.ReadFull(c.conn, msgType[:]); err != nil {
			return nil, err
		}
		switch msgType[0] {
		case 0: // FramebufferUpdate
			if err := c.readFramebufferUpdate(); err != nil {
				return nil, err
			}
			img := image.NewRGBA(c.fb.Rect)
			copy(img.Pix, c.fb.Pix)
			return img, nil
		case 1: // SetColourMapEntries - skip
			c.skipColorMap()
		case 2: // Bell - ignore
		case 3: // ServerCutText - skip
			c.skipCutText()
		default:
			return nil, fmt.Errorf("unexpected VNC message type %d", msgType[0])
		}
	}
}

func (c *VNCClient) readFramebufferUpdate() error {
	var header [3]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return err
	}
	numRects := binary.BigEndian.Uint16(header[1:3])

	for i := uint16(0); i < numRects; i++ {
		var rectHeader [12]byte
		if _, err := io.ReadFull(c.conn, rectHeader[:]); err != nil {
			return err
		}
		x := int(binary.BigEndian.Uint16(rectHeader[0:2]))
		y := int(binary.BigEndian.Uint16(rectHeader[2:4]))
		w := int(binary.BigEndian.Uint16(rectHeader[4:6]))
		h := int(binary.BigEndian.Uint16(rectHeader[6:8]))
		encoding := binary.BigEndian.Uint32(rectHeader[8:12])

		if encoding != 0 {
			return fmt.Errorf("unsupported encoding %d", encoding)
		}

		pixels := make([]byte, w*h*4)
		if _, err := io.ReadFull(c.conn, pixels); err != nil {
			return err
		}

		for py := 0; py < h; py++ {
			for px := 0; px < w; px++ {
				off := (py*w + px) * 4
				c.fb.SetRGBA(x+px, y+py, color.RGBA{
					R: pixels[off],
					G: pixels[off+1],
					B: pixels[off+2],
					A: 255,
				})
			}
		}
	}
	return nil
}

func (c *VNCClient) skipColorMap() {
	var header [5]byte
	io.ReadFull(c.conn, header[:])
	numColors := binary.BigEndian.Uint16(header[3:5])
	skip := make([]byte, numColors*6)
	io.ReadFull(c.conn, skip)
}

func (c *VNCClient) skipCutText() {
	var header [7]byte
	io.ReadFull(c.conn, header[:])
	textLen := binary.BigEndian.Uint32(header[3:7])
	skip := make([]byte, textLen)
	io.ReadFull(c.conn, skip)
}

func (c *VNCClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.conn.Close()
}

// VNCInput implements flow.Input over a VNC connection.
type VNCInput struct {
	conn     net.Conn
	width    uint16
	height   uint16
	mu       *sync.Mutex
	logger   *slog.Logger
	KeyDelay time.Duration
}

// NewVNCInput creates a flow.Input from a VNC client.
func NewVNCInput(client *VNCClient) *VNCInput {
	return &VNCInput{
		conn:   client.conn,
		width:  client.width,
		height: client.height,
		mu:     &client.mu,
		logger: client.logger,
	}
}

func (v *VNCInput) delay() time.Duration {
	if v.KeyDelay > 0 {
		return v.KeyDelay
	}
	return 60 * time.Millisecond
}

// SendKeys presses a chord: all keys down, pause, all keys up.
func (v *VNCInput) SendKeys(ctx context.Context, chord ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	for _, key := range chord {
		keysym, ok := qcodeToKeysym[key]
		if !ok {
			return fmt.Errorf("unknown key code %q", key)
		}
		if err := v.sendKeyEvent(keysym, true); err != nil {
			return err
		}
	}
	time.Sleep(50 * time.Millisecond)
	for i := len(chord) - 1; i >= 0; i-- {
		keysym, _ := qcodeToKeysym[chord[i]]
		if err := v.sendKeyEvent(keysym, false); err != nil {
			return err
		}
	}
	return nil
}

// Click moves the pointer and left-clicks at pt within display bounds.
func (v *VNCInput) Click(ctx context.Context, pt image.Point, display image.Rectangle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	x := uint16(pt.X)
	y := uint16(pt.Y)
	if err := v.sendPointerEvent(0, x, y); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	if err := v.sendPointerEvent(1, x, y); err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
	return v.sendPointerEvent(0, x, y)
}

// RightClick sends a right-click at pt within display bounds.
func (v *VNCInput) RightClick(ctx context.Context, pt image.Point, display image.Rectangle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	x := uint16(pt.X)
	y := uint16(pt.Y)
	if err := v.sendPointerEvent(0, x, y); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	if err := v.sendPointerEvent(4, x, y); err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
	return v.sendPointerEvent(0, x, y)
}

// Type sends text as individual key presses.
func (v *VNCInput) Type(ctx context.Context, text string) error {
	for _, r := range text {
		if err := ctx.Err(); err != nil {
			return err
		}

		var keysym uint32
		var shifted bool
		switch {
		case r >= 'a' && r <= 'z':
			keysym = uint32(r)
		case r >= '0' && r <= '9':
			keysym = uint32(r)
		case r >= 'A' && r <= 'Z':
			keysym = uint32(r - 'A' + 'a')
			shifted = true
		case r == '\n':
			keysym = 0xff0d // Return
		case r == '\t':
			keysym = 0xff09 // Tab
		case r == ' ':
			keysym = 0x0020
		default:
			ks, ok := runeToKeysym[r]
			if !ok {
				return fmt.Errorf("no keysym for %q", r)
			}
			keysym = ks.keysym
			shifted = ks.shifted
		}

		v.mu.Lock()
		if shifted {
			v.sendKeyEvent(0xffe1, true) // Shift_L
		}
		v.sendKeyEvent(keysym, true)
		time.Sleep(50 * time.Millisecond)
		v.sendKeyEvent(keysym, false)
		if shifted {
			v.sendKeyEvent(0xffe1, false)
		}
		v.mu.Unlock()
		time.Sleep(v.delay())
	}
	return nil
}

func (v *VNCInput) sendKeyEvent(keysym uint32, down bool) error {
	msg := make([]byte, 8)
	msg[0] = 4 // KeyEvent
	if down {
		msg[1] = 1
	}
	binary.BigEndian.PutUint32(msg[4:8], keysym)
	_, err := v.conn.Write(msg)
	return err
}

func (v *VNCInput) sendPointerEvent(buttons byte, x, y uint16) error {
	msg := make([]byte, 6)
	msg[0] = 5 // PointerEvent
	msg[1] = buttons
	binary.BigEndian.PutUint16(msg[2:4], x)
	binary.BigEndian.PutUint16(msg[4:6], y)
	_, err := v.conn.Write(msg)
	return err
}

// qcodeToKeysym maps QEMU key codes (used by flow.Step actions) to X11 keysyms
// (used by VNC/RFB). Only the codes the flow runner actually uses are mapped.
var qcodeToKeysym = map[string]uint32{
	"ret": 0xff0d, "tab": 0xff09, "spc": 0x0020,
	"esc": 0xff1b, "backspace": 0xff08, "delete": 0xffff,
	"up": 0xff52, "down": 0xff54, "left": 0xff51, "right": 0xff53,

	"shift":   0xffe1, "shift_l": 0xffe1, "shift_r": 0xffe2,
	"ctrl":    0xffe3, "ctrl_l":  0xffe3, "ctrl_r":  0xffe4,
	"alt":     0xffe9, "alt_l":   0xffe9, "alt_r":   0xffea,
	"meta_l":  0xffe7, "meta_r":  0xffe8,

	"a": 'a', "b": 'b', "c": 'c', "d": 'd', "e": 'e', "f": 'f',
	"g": 'g', "h": 'h', "i": 'i', "j": 'j', "k": 'k', "l": 'l',
	"m": 'm', "n": 'n', "o": 'o', "p": 'p', "q": 'q', "r": 'r',
	"s": 's', "t": 't', "u": 'u', "v": 'v', "w": 'w', "x": 'x',
	"y": 'y', "z": 'z',
	"0": '0', "1": '1', "2": '2', "3": '3', "4": '4',
	"5": '5', "6": '6', "7": '7', "8": '8', "9": '9',

	"minus": '-', "equal": '=', "bracket_left": '[', "bracket_right": ']',
	"semicolon": ';', "apostrophe": '\'', "grave_accent": '`',
	"backslash": '\\', "comma": ',', "dot": '.', "slash": '/',

	"f1": 0xffbe, "f2": 0xffbf, "f3": 0xffc0, "f4": 0xffc1,
	"f5": 0xffc2, "f6": 0xffc3, "f7": 0xffc4, "f8": 0xffc5,
}

type keysymMapping struct {
	keysym  uint32
	shifted bool
}

var runeToKeysym map[rune]keysymMapping

func init() {
	runeToKeysym = map[rune]keysymMapping{
		'-': {'-', false}, '=': {'=', false},
		'[': {'[', false}, ']': {']', false},
		';': {';', false}, '\'': {'\'', false},
		'`': {'`', false}, '\\': {'\\', false},
		',': {',', false}, '.': {'.', false}, '/': {'/', false},

		'!': {'1', true}, '@': {'2', true}, '#': {'3', true},
		'$': {'4', true}, '%': {'5', true}, '^': {'6', true},
		'&': {'7', true}, '*': {'8', true}, '(': {'9', true},
		')': {'0', true}, '_': {'-', true}, '+': {'=', true},
		'{': {'[', true}, '}': {']', true}, ':': {';', true},
		'"': {'\'', true}, '~': {'`', true}, '|': {'\\', true},
		'<': {',', true}, '>': {'.', true}, '?': {'/', true},
	}
}

