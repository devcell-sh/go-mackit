package vzproto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestMarshal(t *testing.T) {
	r := &Request{
		Cmd:        CmdInstall,
		ID:         1,
		IPSW:       "/path/to/restore.ipsw",
		Disk:       "/path/to/disk.raw",
		DiskSizeGB: 64,
		CPUs:       4,
		MemoryGB:   8,
		OutDir:     "/path/out",
	}
	data, err := MarshalRequest(r)
	require.NoError(t, err)

	var decoded Request
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, CmdInstall, decoded.Cmd)
	assert.Equal(t, 1, decoded.ID)
	assert.Equal(t, "/path/to/restore.ipsw", decoded.IPSW)
	assert.Equal(t, uint(4), decoded.CPUs)
}

func TestRequestOmitsEmpty(t *testing.T) {
	r := &Request{Cmd: CmdStop, ID: 5}
	data, err := MarshalRequest(r)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "ipsw")
	assert.NotContains(t, string(data), "keys")
}

func TestMessageResponse(t *testing.T) {
	m := &Message{ID: 1, OK: true, VNCPort: 5901}
	assert.True(t, m.IsResponse())
	assert.False(t, m.IsEvent())
	assert.NoError(t, m.Err())
}

func TestMessageError(t *testing.T) {
	m := &Message{ID: 1, Error: "disk full"}
	assert.True(t, m.IsResponse())
	assert.Error(t, m.Err())
	assert.Contains(t, m.Err().Error(), "disk full")
}

func TestMessageEvent(t *testing.T) {
	m := &Message{Event: EventProgress, Progress: 42, UOM: "pct", Msg: "installing"}
	assert.True(t, m.IsEvent())
	assert.False(t, m.IsResponse())
}

func TestRoundTrip(t *testing.T) {
	m := &Message{
		ID: 3, OK: true,
		Width: 1920, Height: 1080,
		PNGBase64: "iVBORw0KGgoAAAANSUhEUg==",
	}
	data, err := MarshalMessage(m)
	require.NoError(t, err)

	decoded, err := UnmarshalMessage(data)
	require.NoError(t, err)
	assert.Equal(t, 3, decoded.ID)
	assert.Equal(t, 1920, decoded.Width)
	assert.Equal(t, "iVBORw0KGgoAAAANSUhEUg==", decoded.PNGBase64)
}
