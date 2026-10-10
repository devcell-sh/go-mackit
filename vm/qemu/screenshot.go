package qemu

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConvertPPMtoPNG reads a PPM (P6 binary) file and writes a PNG.
func ConvertPPMtoPNG(ppmPath, pngPath string) error {
	f, err := os.Open(ppmPath)
	if err != nil {
		return fmt.Errorf("open PPM: %w", err)
	}
	defer f.Close()

	img, err := decodePPM(f)
	if err != nil {
		return fmt.Errorf("decode PPM: %w", err)
	}

	out, err := os.Create(pngPath)
	if err != nil {
		return fmt.Errorf("create PNG: %w", err)
	}
	defer out.Close()

	if err := png.Encode(out, img); err != nil {
		return fmt.Errorf("encode PNG: %w", err)
	}
	return nil
}

func decodePPM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)

	magic, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if magic != "P6" {
		return nil, fmt.Errorf("unsupported PPM format: %s (only P6 supported)", magic)
	}

	widthStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading width: %w", err)
	}
	heightStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading height: %w", err)
	}
	maxvalStr, err := readPPMToken(br)
	if err != nil {
		return nil, fmt.Errorf("reading maxval: %w", err)
	}

	var width, height, maxval int
	fmt.Sscanf(widthStr, "%d", &width)
	fmt.Sscanf(heightStr, "%d", &height)
	fmt.Sscanf(maxvalStr, "%d", &maxval)

	if width <= 0 || height <= 0 || maxval <= 0 || maxval > 65535 {
		return nil, fmt.Errorf("invalid PPM dimensions: %dx%d maxval=%d", width, height, maxval)
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	row := make([]byte, width*3)
	for y := 0; y < height; y++ {
		if _, err := io.ReadFull(br, row); err != nil {
			return nil, fmt.Errorf("reading pixel row %d: %w", y, err)
		}
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: row[x*3], G: row[x*3+1], B: row[x*3+2], A: 255,
			})
		}
	}

	return img, nil
}

func readPPMToken(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		ch, err := r.ReadByte()
		if err != nil {
			if b.Len() > 0 {
				return b.String(), nil
			}
			return "", err
		}
		if ch == '#' {
			for {
				c, err := r.ReadByte()
				if err != nil {
					return "", err
				}
				if c == '\n' {
					break
				}
			}
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if b.Len() > 0 {
				return b.String(), nil
			}
			continue
		}
		b.WriteByte(ch)
	}
}

// ScreenshotTimeLayout is the ISO 8601 basic UTC layout in periodic
// screenshot names (20261010T055434Z-periodic-0001.png), shared with the
// installer flow's per-OCR frames so a run's screenshots sort as one
// timeline.
const ScreenshotTimeLayout = "20060102T150405Z"

// CaptureScreenshots polls the VM's QMP socket and writes a PNG every
// interval until stop is closed, named <UTC time>-periodic-<seq>.png.
// Failures are logged, never fatal.
func CaptureScreenshots(qmpSock, shotDir string, interval time.Duration, stop <-chan struct{}, done chan<- struct{}, logf func(format string, args ...any)) {
	defer close(done)
	if interval == 0 {
		interval = 30 * time.Second
	}
	seq := 0
	tick := time.NewTicker(interval)
	defer tick.Stop()
	shoot := func() {
		if _, err := os.Stat(qmpSock); err != nil {
			return
		}
		seq++
		name := fmt.Sprintf("%s-periodic-%04d", time.Now().UTC().Format(ScreenshotTimeLayout), seq)
		ppm := filepath.Join(shotDir, name+".ppm")
		if err := QMPScreendump(qmpSock, ppm); err != nil {
			logf("screendump %d: %v", seq, err)
			return
		}
		pngPath := ppm[:len(ppm)-3] + "png"
		if err := ConvertPPMtoPNG(ppm, pngPath); err == nil {
			os.Remove(ppm)
		}
	}
	shoot()
	for {
		select {
		case <-stop:
			shoot()
			return
		case <-tick.C:
			shoot()
		}
	}
}
