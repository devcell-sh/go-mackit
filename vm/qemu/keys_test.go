package qemu

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeysForText(t *testing.T) {
	chords, err := KeysForText("aZ1 _!\n")
	require.NoError(t, err)
	require.Equal(t, [][]string{
		{"a"}, {"shift", "z"}, {"1"}, {"spc"}, {"shift", "minus"}, {"shift", "1"}, {"ret"},
	}, chords)

	chords, err = KeysForText("launchctl load -w /x.plist")
	require.NoError(t, err)
	require.Len(t, chords, len("launchctl load -w /x.plist"))

	_, err = KeysForText("é")
	require.ErrorContains(t, err, "no QEMU key code")
}

func TestTabletIndex(t *testing.T) {
	idx, active, ok := tabletIndex("  Mouse #4: QEMU HID Tablet (absolute)\r\n* Mouse #2: QEMU PS/2 Mouse\r\n")
	require.True(t, ok)
	require.Equal(t, "4", idx)
	require.False(t, active)

	idx, active, ok = tabletIndex("  Mouse #2: QEMU PS/2 Mouse\r\n* Mouse #4: QEMU HID Tablet (absolute)\r\n")
	require.True(t, ok)
	require.Equal(t, "4", idx)
	require.True(t, active)

	_, _, ok = tabletIndex("* Mouse #2: QEMU PS/2 Mouse\r\n")
	require.False(t, ok)
}
