package vmstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndList(t *testing.T) {
	dir := t.TempDir()
	st := &State{
		Name:      "test-vm",
		ImagePath: "/tmp/mackit.qcow2",
		PID:       12345,
		Backend:   "qemu",
		StartedAt: time.Now(),
		SSHPort:   20022,
		VNCPort:   25900,
		Accel:     "tcg",
		OutputDir: dir,
	}

	require.NoError(t, Save(dir, st))

	states, err := List(dir)
	require.NoError(t, err)
	require.Len(t, states, 1)
	assert.Equal(t, "test-vm", states[0].Name)
	assert.Equal(t, "/tmp/mackit.qcow2", states[0].ImagePath)
	assert.Equal(t, 12345, states[0].PID)
	assert.Equal(t, uint16(20022), states[0].SSHPort)
}

func TestList_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	states, err := List(dir)
	require.NoError(t, err)
	assert.Empty(t, states)
}

func TestList_NonexistentDir(t *testing.T) {
	states, err := List("/nonexistent/dir")
	require.NoError(t, err)
	assert.Nil(t, states)
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	st := &State{Name: "removeme", PID: 1}
	require.NoError(t, Save(dir, st))

	assert.FileExists(t, filepath.Join(dir, "removeme.json"))
	require.NoError(t, Remove(dir, "removeme"))
	assert.NoFileExists(t, filepath.Join(dir, "removeme.json"))
}

func TestFindByImage(t *testing.T) {
	dir := t.TempDir()
	st := &State{
		Name:      "findme",
		ImagePath: "/images/mackit.qcow2",
		PID:       1,
	}
	require.NoError(t, Save(dir, st))

	found, err := FindByImage(dir, "/images/mackit.qcow2")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "findme", found.Name)

	notFound, err := FindByImage(dir, "/other/image.qcow2")
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestIsAlive(t *testing.T) {
	assert.True(t, IsAlive(os.Getpid()))
	assert.False(t, IsAlive(999999999))
}

func TestUptime(t *testing.T) {
	t.Run("Seconds", func(t *testing.T) {
		st := &State{StartedAt: time.Now().Add(-30 * time.Second)}
		assert.Contains(t, Uptime(st), "s")
	})

	t.Run("Minutes", func(t *testing.T) {
		st := &State{StartedAt: time.Now().Add(-5 * time.Minute)}
		assert.Contains(t, Uptime(st), "m")
	})

	t.Run("Hours", func(t *testing.T) {
		st := &State{StartedAt: time.Now().Add(-2 * time.Hour)}
		assert.Contains(t, Uptime(st), "h")
	})
}
