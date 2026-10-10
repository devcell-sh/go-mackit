package media

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodePlist_Dict(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Name</key>
	<string>macOS</string>
	<key>Version</key>
	<integer>15</integer>
</dict>
</plist>`

	v, err := decodePlist(strings.NewReader(input))
	require.NoError(t, err)

	d := plistDict(v)
	require.NotNil(t, d)
	assert.Equal(t, "macOS", plistString(d["Name"]))
	assert.Equal(t, int64(15), plistInt(d["Version"]))
}

func TestDecodePlist_NestedDictArray(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Products</key>
	<dict>
		<key>001-12345</key>
		<dict>
			<key>Packages</key>
			<array>
				<dict>
					<key>URL</key>
					<string>https://example.com/BaseSystem.dmg</string>
					<key>Size</key>
					<integer>700000000</integer>
				</dict>
			</array>
			<key>Distributions</key>
			<dict>
				<key>English</key>
				<string>https://example.com/English.dist</string>
			</dict>
		</dict>
	</dict>
</dict>
</plist>`

	v, err := decodePlist(strings.NewReader(input))
	require.NoError(t, err)

	root := plistDict(v)
	products := plistDict(root["Products"])
	require.NotNil(t, products)

	product := plistDict(products["001-12345"])
	require.NotNil(t, product)

	packages := plistArray(product["Packages"])
	require.Len(t, packages, 1)

	pkg := plistDict(packages[0])
	assert.Equal(t, "https://example.com/BaseSystem.dmg", plistString(pkg["URL"]))
	assert.Equal(t, int64(700000000), plistInt(pkg["Size"]))

	dists := plistDict(product["Distributions"])
	assert.Equal(t, "https://example.com/English.dist", plistString(dists["English"]))
}

func TestDecodePlist_Boolean(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Enabled</key>
	<true/>
	<key>Disabled</key>
	<false/>
</dict>
</plist>`

	v, err := decodePlist(strings.NewReader(input))
	require.NoError(t, err)

	d := plistDict(v)
	assert.Equal(t, true, d["Enabled"])
	assert.Equal(t, false, d["Disabled"])
}

func TestDecodePlist_EmptyDict(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
</dict>
</plist>`

	v, err := decodePlist(strings.NewReader(input))
	require.NoError(t, err)

	d := plistDict(v)
	assert.NotNil(t, d)
	assert.Empty(t, d)
}
