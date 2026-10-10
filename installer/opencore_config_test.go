package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatchOpenCorePlist(t *testing.T) {
	input := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Misc</key>
	<dict>
		<key>Boot</key>
		<dict>
			<key>ShowPicker</key>
			<true/>
			<key>Timeout</key>
			<integer>0</integer>
		</dict>
		<key>Debug</key>
		<dict>
			<key>AppleDebug</key>
			<false/>
			<key>DisplayLevel</key>
			<integer>2147483650</integer>
			<key>LogModules</key>
			<string>*</string>
			<key>Target</key>
			<integer>3</integer>
		</dict>
		<key>Security</key>
		<dict>
			<key>ScanPolicy</key>
			<integer>17760515</integer>
		</dict>
		<key>Serial</key>
		<dict>
			<key>Init</key>
			<false/>
			<key>Override</key>
			<false/>
		</dict>
	</dict>
	<key>NVRAM</key>
	<dict>
		<key>Add</key>
		<dict>
			<key>7C436110-AB2A-4BBB-A880-FE41995C9F82</key>
			<dict>
				<key>boot-args</key>
				<string>keepsyms=1</string>
			</dict>
		</dict>
	</dict>
</dict>
</plist>`)

	patched, err := PatchOpenCorePlist(input, 5)
	require.NoError(t, err)

	s := string(patched)
	require.True(t, strings.Contains(s, "<integer>5</integer>"), "Timeout should be 5")
	require.Contains(t, s, "<key>ScanPolicy</key>\n\t\t\t<integer>17760515</integer>", "ScanPolicy must stay as shipped (0 makes OpenCore boot itself)")
	require.Contains(t, s, "<key>Target</key>\n\t\t\t<integer>11</integer>", "Debug.Target should log to console and serial")
	require.Contains(t, s, "<key>DisplayLevel</key>\n\t\t\t<integer>2147483714</integer>", "DisplayLevel should include DEBUG_INFO")
	require.Contains(t, s, "<key>Init</key>\n\t\t\t<true/>", "Serial.Init should be on")
	require.Contains(t, s, "<key>Override</key>\n\t\t\t<false/>", "Serial.Override must stay off")
	require.Contains(t, s, "<key>AppleDebug</key>\n\t\t\t<false/>", "unrelated Debug keys stay")
	require.Contains(t, s, "<key>ShowPicker</key>\n\t\t\t<false/>", "ShowPicker should be false")
	require.True(t, strings.Contains(s, "-v"), "boot-args should include -v")
	require.True(t, strings.Contains(s, "serial=1"), "boot-args should include serial=1")
	require.True(t, strings.Contains(s, "keepsyms=1"), "boot-args should preserve existing args")
}

func TestPatchOpenCorePlist_AlreadyPatched(t *testing.T) {
	input := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Boot</key>
	<dict>
		<key>ShowPicker</key>
		<false/>
		<key>Timeout</key>
		<integer>5</integer>
	</dict>
	<key>Debug</key>
	<dict>
		<key>DisplayLevel</key>
		<integer>2147483714</integer>
		<key>Target</key>
		<integer>11</integer>
	</dict>
	<key>Security</key>
	<dict>
		<key>ScanPolicy</key>
		<integer>18809603</integer>
	</dict>
	<key>Serial</key>
	<dict>
		<key>Init</key>
		<true/>
	</dict>
	<key>NVRAM</key>
	<dict>
		<key>boot-args</key>
		<string>keepsyms=1 -v serial=1</string>
	</dict>
</dict>
</plist>`)

	patched, err := PatchOpenCorePlist(input, 5)
	require.NoError(t, err)
	require.Equal(t, string(input), string(patched))
}

// TestPatchOpenCorePlist_RealConfig patches the config.plist shipped in
// the OpenCore image mackit downloads, so regex anchoring is checked
// against the real key order.
func TestPatchOpenCorePlist_RealConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "test", "testdata", "opencore", "config.plist"))
	require.NoError(t, err)

	patched, err := PatchOpenCorePlist(data, 5)
	require.NoError(t, err)
	s := string(patched)

	require.Equal(t, 1, strings.Count(s, "<integer>11</integer>"), "exactly one Target patched")
	require.Equal(t, 1, strings.Count(s, "<integer>2147483714</integer>"), "exactly one DisplayLevel patched")
	require.NotContains(t, s, "<integer>2147483650</integer>")
	require.Contains(t, s, "<key>Serial</key>\n\t\t<dict>\n\t\t\t<key>Init</key>\n\t\t\t<true/>")
	require.Contains(t, s, "<string>keepsyms=1 -v serial=1</string>")
	require.Contains(t, s, "<key>ShowPicker</key>\n\t\t\t<false/>")
	require.Contains(t, s, "<key>Timeout</key>\n\t\t\t<integer>5</integer>")
	require.Contains(t, s, "<key>ScanPolicy</key>\n\t\t\t<integer>18809603</integer>", "ScanPolicy untouched")
	// The patch must not touch keys outside Misc.Debug/Misc.Serial.
	require.Equal(t, strings.Count(string(data), "<key>"), strings.Count(s, "<key>"))
}
