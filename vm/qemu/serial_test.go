package qemu

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sampleSerial is shaped like a real mackit serial.log: EDK2 escapes and
// BdsDxe lines, OpenCore lines with and without its timestamp prefix, the
// bootstrap launcher (BS:), then the kernel.
const sampleSerial = "\x1b[2J\x1b[01;01H\x1b[=3h\x1b[=3hBdsDxe: loading Boot0001 \"UEFI QEMU HARDDISK QM00013 \" from PciRoot(0x0)/Pci(0x2,0x0)/Sata(0x0,0xFFFF,0x0)\r\n" +
	"BdsDxe: starting Boot0001 \"UEFI QEMU HARDDISK QM00013 \" from PciRoot(0x0)/Pci(0x2,0x0)/Sata(0x0,0xFFFF,0x0)\r\n" +
	"OC: Starting OpenCore...\r\n" +
	"00:000 00:000 OCUI: Failed to load images for Acidanthera\\GoldenGate\r\n" +
	"    continuation line without prefix\r\n" +
	"00:043 00:004 BS: Failed to start OpenCore image - Already started\r\n" +
	"Darwin Kernel Version 19.6.0: Mon Aug 31 22:12:52 PDT 2020; root:xnu-6153.141.2~1/RELEASE_X86_64\r\n" +
	"000003.339260 AppleUSBLegacyRoot@: AppleUSBLegacyRoot::init: enabling legacy matching\r\n" +
	"IOAPIC: Version 0x20 Vectors 64:87\r\n" +
	"random junk after the kernel started\r\n" +
	"panic(cpu 0 caller 0xffffff80): \"something\"\r\n"

func TestSerialClassifier(t *testing.T) {
	lines, err := readAllLines(strings.NewReader(sampleSerial))
	require.NoError(t, err)
	want := []string{
		SerialSourceUEFI, SerialSourceUEFI,
		SerialSourceOpenCore, SerialSourceOpenCore, SerialSourceOpenCore, SerialSourceOpenCore,
		SerialSourceXNU, SerialSourceXNU, SerialSourceXNU, SerialSourceXNU, SerialSourceXNU,
	}
	require.Len(t, lines, len(want))
	for i, l := range lines {
		require.Equal(t, want[i], l[0], l[1])
	}
	require.Equal(t, `BdsDxe: loading Boot0001 "UEFI QEMU HARDDISK QM00013 " from PciRoot(0x0)/Pci(0x2,0x0)/Sata(0x0,0xFFFF,0x0)`, lines[0][1], "ANSI escapes stripped")
}

func TestTailSerial_ForwardsLinesWithService(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { TailSerial(ctx, path, logger); close(done) }()

	// The file appears after the tailer started, and grows in two writes,
	// the second splitting a line.
	time.Sleep(300 * time.Millisecond)
	f, err := os.Create(path)
	require.NoError(t, err)
	parts := strings.SplitN(sampleSerial, "Vectors", 2)
	_, err = f.WriteString(parts[0])
	require.NoError(t, err)
	time.Sleep(400 * time.Millisecond)
	_, err = f.WriteString("Vectors" + parts[1])
	require.NoError(t, err)
	f.Close()

	require.Eventually(t, func() bool { return strings.Count(buf.String(), "\n") >= 11 }, 5*time.Second, 50*time.Millisecond, buf.String())
	cancel()
	<-done

	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		recs = append(recs, m)
	}
	require.Len(t, recs, 11)
	require.Equal(t, "uefi", recs[0]["service"])
	require.Equal(t, "serial", recs[0]["stream"])
	require.Equal(t, "OC: Starting OpenCore...", recs[2]["msg"])
	require.Equal(t, "opencore", recs[2]["service"])
	require.Equal(t, "IOAPIC: Version 0x20 Vectors 64:87", recs[8]["msg"], "split line reassembled")
	require.Equal(t, "xnu", recs[8]["service"])
	require.Equal(t, "DEBUG", recs[0]["level"], "console lines are debug records")
	require.Equal(t, "WARN", recs[10]["level"], "panic lines are warnings")
}
