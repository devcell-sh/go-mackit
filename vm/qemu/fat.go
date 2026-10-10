package qemu

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
)

// CreateFATQcow2 creates a qcow2 disk with a FAT32 filesystem containing
// the given files. Mirrors go-winkit's vm/qemu/fat.go.
func CreateFATQcow2(imgPath string, files map[string][]byte, capacity int64) error {
	rawPath := imgPath + ".raw"
	if err := createFATImage(rawPath, files, capacity); err != nil {
		return fmt.Errorf("creating raw FAT32: %w", err)
	}
	defer os.Remove(rawPath)

	cmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawPath, imgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("converting raw to qcow2: %w\n%s", err, out)
	}
	return nil
}

// ReadFileFromFATQcow2 reads a single file from a qcow2-backed FAT32 image.
func ReadFileFromFATQcow2(imgPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "qcow2-read-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "disk.raw")
	cmd := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", imgPath, rawPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("converting qcow2 to raw: %w\n%s", err, out)
	}
	return readFileFromFAT(rawPath, filePath)
}

// ReplaceFileInFATQcow2 copies srcImg to destImg with the file at filePath
// replaced by data, keeping the source's partition table and filesystem
// geometry. Only that file's clusters and directory entry change.
//
// Rebuilding the whole filesystem with CreateFATQcow2 is not an option for
// images the firmware has to read: go-diskfs 1.9 sizes the FAT two entries
// short ("Too few sectors in FAT" in mtools, rejected by dosfstools and by
// EDK2's FAT driver), which broke OpenCore's resource loading.
func ReplaceFileInFATQcow2(srcImg, destImg, filePath string, data []byte) error {
	tmpDir, err := os.MkdirTemp("", "qcow2-patch-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "disk.raw")
	cmd := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", srcImg, rawPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("converting qcow2 to raw: %w\n%s", err, out)
	}
	if err := replaceFileInFAT(rawPath, filePath, data); err != nil {
		return err
	}
	cmd = exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawPath, destImg)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("converting raw to qcow2: %w\n%s", err, out)
	}
	return nil
}

func replaceFileInFAT(imgPath, filePath string, data []byte) error {
	d, err := diskfs.Open(imgPath)
	if err != nil {
		return fmt.Errorf("opening image: %w", err)
	}
	defer d.Close()

	fs, err := getFATFilesystem(d)
	if err != nil {
		return err
	}
	p := fsPath(filePath)
	// Look the file up case-insensitively so callers can pass the path as
	// listed by ReadAllFilesFromFATQcow2 or as written in docs.
	if real, err := lookupFATPath(fs, p); err == nil {
		p = real
	}
	f, err := fs.OpenFile(p, os.O_RDWR|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("opening %s for writing: %w", filePath, err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", filePath, err)
	}
	if fl, ok := f.(dirEntryFlusher); ok {
		fl.SetReadOnly(false)
	}
	if c, ok := f.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return fmt.Errorf("closing %s: %w", filePath, err)
		}
	}
	return nil
}

// lookupFATPath resolves p (io/fs form) against the directory entries,
// case-insensitively, returning the on-disk spelling.
func lookupFATPath(fs filesystem.FileSystem, p string) (string, error) {
	cur := "."
	for _, part := range strings.Split(p, "/") {
		entries, err := fs.ReadDir(cur)
		if err != nil {
			return "", err
		}
		found := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), part) {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return "", fmt.Errorf("%s not found in %s", part, cur)
		}
		if cur == "." {
			cur = found
		} else {
			cur = cur + "/" + found
		}
	}
	return cur, nil
}

// ReadAllFilesFromFATQcow2 reads every file from a qcow2-backed FAT32 image
// and returns them as a path-to-content map.
func ReadAllFilesFromFATQcow2(imgPath string) (map[string][]byte, error) {
	tmpDir, err := os.MkdirTemp("", "qcow2-readall-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "disk.raw")
	cmd := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", imgPath, rawPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("converting qcow2 to raw: %w\n%s", err, out)
	}
	return readAllFilesFromFAT(rawPath)
}

func readFileFromFAT(imgPath, filePath string) ([]byte, error) {
	d, err := diskfs.Open(imgPath)
	if err != nil {
		return nil, fmt.Errorf("opening image: %w", err)
	}

	fs, err := getFATFilesystem(d)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	candidates := []string{filePath, strings.ToUpper(filePath)}

	var rdr io.ReadCloser
	var openErr error
	for _, p := range candidates {
		rdr, openErr = fs.OpenFile(p, os.O_RDONLY)
		if openErr == nil {
			break
		}
	}
	if openErr != nil {
		return nil, fmt.Errorf("opening %s: %w", filePath, openErr)
	}

	data, err := io.ReadAll(rdr)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}
	return data, nil
}

func readAllFilesFromFAT(imgPath string) (map[string][]byte, error) {
	d, err := diskfs.Open(imgPath)
	if err != nil {
		return nil, fmt.Errorf("opening image: %w", err)
	}

	fs, err := getFATFilesystem(d)
	if err != nil {
		return nil, err
	}

	files := make(map[string][]byte)
	if err := walkFATDir(fs, "/", files); err != nil {
		return nil, err
	}
	return files, nil
}

func getFATFilesystem(d *disk.Disk) (filesystem.FileSystem, error) {
	fs, err := d.GetFilesystem(0)
	if err == nil {
		return fs, nil
	}
	fs, err = d.GetFilesystem(1)
	if err == nil {
		return fs, nil
	}
	return nil, fmt.Errorf("no FAT filesystem found")
}

// fsPath converts a slash-rooted path ("/EFI/OC", "/") into the io/fs
// form go-diskfs validates against ("EFI/OC", "."). go-diskfs 1.9 rejects
// a leading slash with fs.ErrInvalid.
func fsPath(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return p
}

func walkFATDir(fs filesystem.FileSystem, dir string, files map[string][]byte) error {
	entries, err := fs.ReadDir(fsPath(dir))
	if err != nil {
		return fmt.Errorf("listing %s: %w", dir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		fullPath := path.Join(dir, name)
		if entry.IsDir() {
			if err := walkFATDir(fs, fullPath, files); err != nil {
				return err
			}
		} else {
			rdr, err := fs.OpenFile(fullPath, os.O_RDONLY)
			if err != nil {
				return fmt.Errorf("opening %s: %w", fullPath, err)
			}
			data, err := io.ReadAll(rdr)
			if err != nil {
				return fmt.Errorf("reading %s: %w", fullPath, err)
			}
			files[fullPath] = data
		}
	}
	return nil
}

// dirEntryFlusher matches go-diskfs FAT files that expose a setter which
// forces a directory-entry rewrite. Without this, go-diskfs v1.9.4 keeps
// stale cluster-rounded sizes on disk.
type dirEntryFlusher interface {
	SetReadOnly(bool) error
}

func createFATImage(imgPath string, files map[string][]byte, minSize int64) error {
	var totalSize int64
	for _, data := range files {
		totalSize += int64(len(data))
	}

	diskSize := totalSize + 10*1024*1024
	if diskSize < minSize {
		diskSize = minSize
	}
	if diskSize < 64*1024*1024 {
		diskSize = 64 * 1024 * 1024
	}

	os.Remove(imgPath)
	d, err := diskfs.Create(imgPath, diskSize, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("creating FAT disk image: %w", err)
	}

	fspec := disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeFat32,
		VolumeLabel: "UEFIBOOT",
	}
	fs, err := d.CreateFilesystem(fspec)
	if err != nil {
		return fmt.Errorf("creating FAT32 filesystem: %w", err)
	}

	if err := addFilesToFAT(fs, files); err != nil {
		return err
	}

	if err := d.Close(); err != nil {
		return fmt.Errorf("finalizing FAT image: %w", err)
	}
	return nil
}

func addFilesToFAT(fs filesystem.FileSystem, files map[string][]byte) error {
	dirs := map[string]bool{}
	for filePath := range files {
		dir := path.Dir(filePath)
		for dir != "/" && dir != "." && !dirs[dir] {
			dirs[dir] = true
			dir = path.Dir(dir)
		}
	}

	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)

	for _, dir := range sortedDirs {
		if err := fs.Mkdir(dir); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	for filePath, data := range files {
		rw, err := fs.OpenFile(filePath, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("creating %s: %w", filePath, err)
		}
		if _, err := rw.Write(data); err != nil {
			return fmt.Errorf("writing %s: %w", filePath, err)
		}
		if f, ok := rw.(dirEntryFlusher); ok {
			f.SetReadOnly(false)
		}
		if c, ok := rw.(io.Closer); ok {
			c.Close()
		}
	}
	return nil
}
