package qemu

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

// Guest console sources, used as the "service" attribute on forwarded
// serial lines.
const (
	SerialSourceUEFI     = "uefi"     // EDK2 firmware (BdsDxe, DXE drivers)
	SerialSourceOpenCore = "opencore" // OpenCore (OC:, OCB:, OCABC: ...)
	SerialSourceXNU      = "xnu"      // macOS kernel and kexts (-v serial=1)
)

// TailSerial follows the QEMU serial log at path and forwards each line
// to logger as a Debug record whose message is the line and whose
// "service" attribute names the guest component that printed it. It
// returns when ctx is done. The file may not exist yet when called.
//
// Lines mentioning a kernel panic are logged at Warn so they stand out
// without --debug.
func TailSerial(ctx context.Context, path string, logger *slog.Logger) {
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	var rd *bufio.Reader
	var partial strings.Builder
	cls := &serialClassifier{}
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	for {
		if f == nil {
			var err error
			f, err = os.Open(path)
			if err == nil {
				rd = bufio.NewReader(f)
			}
		}
		if rd != nil {
			for {
				chunk, err := rd.ReadString('\n')
				partial.WriteString(chunk)
				if err != nil {
					break // io.EOF: wait for more
				}
				line := partial.String()
				partial.Reset()
				emitSerialLine(logger, cls, line)
			}
		}
		select {
		case <-ctx.Done():
			// Flush a trailing line without newline.
			if partial.Len() > 0 {
				emitSerialLine(logger, cls, partial.String())
			}
			return
		case <-poll.C:
		}
	}
}

func emitSerialLine(logger *slog.Logger, cls *serialClassifier, raw string) {
	line := strings.TrimRight(stripANSI(raw), "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	src := cls.classify(line)
	level := slog.LevelDebug
	if strings.Contains(line, "panic(") || strings.Contains(line, "Kernel Panic") {
		level = slog.LevelWarn
	}
	logger.Log(context.Background(), level, line, "service", src, "stream", "serial")
}

// serialClassifier attributes console lines to the component that printed
// them. Boot is sequential (firmware, then OpenCore, then the kernel), so
// lines without a recognizable prefix are attributed to the current phase.
type serialClassifier struct {
	phase string
}

var (
	// OpenCore prefixes every log line with two timestamps (since start,
	// since previous line) and a module tag: "00:038 00:030 OCM: ...".
	// Lines before the log is initialised have no timestamps ("OC: ...").
	// BS: is OpenCore's bootstrap launcher (BOOTx64.efi).
	reOpenCore  = regexp.MustCompile(`^(\d\d:\d{3} \d\d:\d{3} )?(OC[A-Z]*|AAPL|BS): `)
	reXNUStamp  = regexp.MustCompile(`^\d{6}\.\d{6} `)
	reXNUMarker = regexp.MustCompile(`Darwin Kernel Version|^(AppleACPI|IOAPIC|IOKit|AppleKeyStore|APFS|HFS|IOUSB|Apple[A-Za-z]+:|vm_page|pci |Kext )`)
)

func (c *serialClassifier) classify(line string) string {
	if c.phase == "" {
		c.phase = SerialSourceUEFI
	}
	switch {
	case reOpenCore.MatchString(line):
		if c.phase != SerialSourceXNU {
			c.phase = SerialSourceOpenCore
		}
		return SerialSourceOpenCore
	case reXNUStamp.MatchString(line) || reXNUMarker.MatchString(line):
		c.phase = SerialSourceXNU
		return SerialSourceXNU
	case false:
		if c.phase != SerialSourceXNU {
			c.phase = SerialSourceOpenCore
		}
		return SerialSourceOpenCore
	case strings.HasPrefix(line, "BdsDxe:") || strings.Contains(line, "UEFI ") && c.phase == SerialSourceUEFI:
		return SerialSourceUEFI
	}
	return c.phase
}

// CSI sequences with any parameter bytes (0x30-0x3F, so "=" and "?" too:
// EDK2 emits ESC[=3h), optional intermediates and a final byte, plus the
// two-byte charset selectors.
var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0-9A-Za-z]`)

// stripANSI removes terminal escape sequences (colors, cursor moves,
// mode sets) that EDK2 and OpenCore emit on the serial console.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// readAllLines is a helper for tests: it reads an existing serial log in
// one pass and returns (source, line) pairs.
func readAllLines(r io.Reader) ([][2]string, error) {
	cls := &serialClassifier{}
	var out [][2]string
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			l := strings.TrimRight(stripANSI(line), "\r\n")
			if strings.TrimSpace(l) != "" {
				out = append(out, [2]string{cls.classify(l), l})
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}
