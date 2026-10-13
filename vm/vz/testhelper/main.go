// Command testhelper is a mock mackit-vz helper for protocol testing.
// It reads JSONL requests from stdin and writes canned responses to stdout.
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"time"

	"github.com/devcell-sh/go-mackit/vm/vz/vzproto"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		req, err := vzproto.UnmarshalRequest(scanner.Bytes())
		if err != nil {
			fmt.Fprintf(os.Stderr, "testhelper: bad request: %v\n", err)
			continue
		}
		handleRequest(req)
	}
}

func handleRequest(req *vzproto.Request) {
	switch req.Cmd {
	case vzproto.CmdInstall:
		handleInstall(req)
	case vzproto.CmdBoot:
		handleBoot(req)
	case vzproto.CmdScreenshot:
		handleScreenshot(req)
	case vzproto.CmdSendKeys:
		respond(&vzproto.Message{ID: req.ID, OK: true})
	case vzproto.CmdClick:
		respond(&vzproto.Message{ID: req.ID, OK: true})
	case vzproto.CmdType:
		respond(&vzproto.Message{ID: req.ID, OK: true})
	case vzproto.CmdStop:
		respond(&vzproto.Message{ID: req.ID, OK: true})
		os.Exit(0)
	default:
		respond(&vzproto.Message{ID: req.ID, Error: fmt.Sprintf("unknown command: %s", req.Cmd)})
	}
}

func handleInstall(req *vzproto.Request) {
	for _, pct := range []int{0, 25, 50, 75, 100} {
		emitEvent(&vzproto.Message{
			Event:    vzproto.EventProgress,
			Progress: pct,
			UOM:      "pct",
			Msg:      "installing macOS",
		})
		time.Sleep(10 * time.Millisecond)
	}
	respond(&vzproto.Message{ID: req.ID, OK: true})
}

func handleBoot(req *vzproto.Request) {
	emitEvent(&vzproto.Message{
		Event:   vzproto.EventBooted,
		VNCPort: 15900,
	})
	respond(&vzproto.Message{ID: req.ID, OK: true, VNCPort: 15900})
}

func handleScreenshot(req *vzproto.Request) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 50, G: 100, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	respond(&vzproto.Message{
		ID:        req.ID,
		OK:        true,
		Width:     320,
		Height:    240,
		PNGBase64: b64,
	})
}

func respond(msg *vzproto.Message) {
	data, _ := vzproto.MarshalMessage(msg)
	os.Stdout.Write(append(data, '\n'))
}

func emitEvent(msg *vzproto.Message) {
	data, _ := vzproto.MarshalMessage(msg)
	os.Stdout.Write(append(data, '\n'))
}
