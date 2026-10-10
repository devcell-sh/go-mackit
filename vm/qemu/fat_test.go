package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-mackit/cache"
)

// TestReadAllFilesFromFAT_RoundTrip writes a nested tree into a FAT32
// image and reads it back through the directory walker. go-diskfs 1.9
// validates paths with io/fs rules (no leading slash), which used to make
// the root listing fail with "invalid argument".
func TestReadAllFilesFromFAT_RoundTrip(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "disk.raw")
	want := map[string][]byte{
		"/EFI/BOOT/BOOTx64.efi":   []byte("bootloader"),
		"/EFI/OC/config.plist":    []byte("<plist/>"),
		"/EFI/OC/Drivers/Foo.efi": []byte("driver"),
		"/README.txt":             []byte("hello"),
	}
	require.NoError(t, createFATImage(raw, want, 0))

	got, err := readAllFilesFromFAT(raw)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	for p, data := range want {
		var found bool
		for gp, gdata := range got {
			if strings.EqualFold(gp, p) {
				found = true
				require.Equal(t, data, gdata, p)
			}
		}
		require.True(t, found, "missing %s in %v", p, keys(got))
	}

	single, err := readFileFromFAT(raw, "/EFI/OC/config.plist")
	require.NoError(t, err)
	require.Equal(t, want["/EFI/OC/config.plist"], single)
}

// TestReadAllFilesFromFATQcow2_OpenCore reads the real cached OpenCore
// image (GPT with one ESP) when it is present, as the build does before
// patching config.plist.
func TestReadAllFilesFromFATQcow2_OpenCore(t *testing.T) {
	img := filepath.Join(cache.Dir(), "OpenCore-v21.qcow2")
	if _, err := os.Stat(img); err != nil {
		t.Skipf("no cached OpenCore image at %s", img)
	}
	files, err := ReadAllFilesFromFATQcow2(img)
	require.NoError(t, err)
	var config string
	for p := range files {
		if strings.HasSuffix(strings.ToUpper(p), "/EFI/OC/CONFIG.PLIST") {
			config = p
		}
	}
	require.NotEmpty(t, config, "config.plist not found in %v", keys(files))
	require.Contains(t, string(files[config]), "<key>Timeout</key>")
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestReplaceFileInFATQcow2_OpenCore patches config.plist in place in a
// copy of the cached OpenCore image. The grown file forces a new cluster
// allocation. Besides reading back with go-diskfs, the result is checked
// with mtools when installed, since go-diskfs happily reads layouts the
// firmware rejects.
func TestReplaceFileInFATQcow2_OpenCore(t *testing.T) {
	img := filepath.Join(cache.Dir(), "OpenCore-v21.qcow2")
	if _, err := os.Stat(img); err != nil {
		t.Skipf("no cached OpenCore image at %s", img)
	}
	orig, err := ReadFileFromFATQcow2(img, "/EFI/OC/config.plist")
	require.NoError(t, err)

	// Grow the file by well over a cluster.
	patched := append([]byte(nil), orig...)
	patched = append(patched, []byte("\n<!-- "+strings.Repeat("mackit ", 1000)+" -->\n")...)

	dest := filepath.Join(t.TempDir(), "OpenCore.qcow2")
	require.NoError(t, ReplaceFileInFATQcow2(img, dest, "/EFI/OC/config.plist", patched))

	got, err := ReadFileFromFATQcow2(dest, "/EFI/OC/config.plist")
	require.NoError(t, err)
	require.Equal(t, patched, got)

	before, err := ReadAllFilesFromFATQcow2(img)
	require.NoError(t, err)
	after, err := ReadAllFilesFromFATQcow2(dest)
	require.NoError(t, err)
	require.Len(t, after, len(before), "no file may be lost")
	for p, data := range before {
		if strings.EqualFold(p, "/EFI/OC/config.plist") {
			continue
		}
		require.Equal(t, len(data), len(after[p]), p)
	}

	mtype, err := exec.LookPath("mtype")
	if err != nil {
		t.Log("mtools not installed, skipping independent FAT check")
		return
	}
	raw := filepath.Join(t.TempDir(), "disk.raw")
	out, err := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", dest, raw).CombinedOutput()
	require.NoError(t, err, string(out))
	// The ESP starts at LBA 40 in the OpenCore image.
	out, err = exec.Command(mtype, "-i", raw+"@@20480", "::/EFI/OC/config.plist").Output()
	require.NoError(t, err)
	require.Equal(t, patched, out, "mtools must read the patched file back")
	out, err = exec.Command("mdir", "-i", raw+"@@20480", "-/", "-b", "::").Output()
	require.NoError(t, err)
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" && !strings.HasSuffix(l, "/") {
			n++
		}
	}
	require.Equal(t, len(before), n, "mtools must list every file")
}

// TestCreateFATImage_OpenCoreTree writes the full OpenCore file tree (508
// files, nested kext bundles, long names) into a fresh FAT32 image and
// reads it back with go-diskfs. Dot-directories (.fseventsd) are macOS
// metadata that go-diskfs drops; nothing boots from them. Note the image
// go-diskfs creates is NOT valid for firmware (FAT sized short), which is
// why patchOpenCore uses ReplaceFileInFATQcow2 instead.
func TestCreateFATImage_OpenCoreTree(t *testing.T) {
	img := filepath.Join(cache.Dir(), "OpenCore-v21.qcow2")
	if _, err := os.Stat(img); err != nil {
		t.Skipf("no cached OpenCore image at %s", img)
	}
	files, err := ReadAllFilesFromFATQcow2(img)
	require.NoError(t, err)
	require.Greater(t, len(files), 400, "OpenCore image has hundreds of files")

	raw := filepath.Join(t.TempDir(), "oc.raw")
	require.NoError(t, createFATImage(raw, files, 0))

	got, err := readAllFilesFromFAT(raw)
	require.NoError(t, err)
	var missing []string
	for p, data := range files {
		if strings.HasPrefix(p, "/.") {
			continue
		}
		g, ok := got[p]
		if !ok {
			missing = append(missing, p)
			continue
		}
		require.Equal(t, len(data), len(g), p)
	}
	require.Empty(t, missing, "files lost in the round trip")
}
