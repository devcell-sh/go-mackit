package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFrom_VersionOnly(t *testing.T) {
	version, arch, err := ParseFrom("macos/sequoia")
	require.NoError(t, err)
	assert.Equal(t, "sequoia", version)
	assert.NotEmpty(t, arch)
}

func TestParseFrom_WithArch(t *testing.T) {
	version, arch, err := ParseFrom("macos/sequoia-arm64")
	require.NoError(t, err)
	assert.Equal(t, "sequoia", version)
	assert.Equal(t, "arm64", arch)
}

func TestParseFrom_X86(t *testing.T) {
	version, arch, err := ParseFrom("macos/sonoma-x86_64")
	require.NoError(t, err)
	assert.Equal(t, "sonoma", version)
	assert.Equal(t, "x86_64", arch)
}

func TestParseFrom_AmdToX86(t *testing.T) {
	version, arch, err := ParseFrom("macos/ventura-amd64")
	require.NoError(t, err)
	assert.Equal(t, "ventura", version)
	assert.Equal(t, "x86_64", arch)
}

func TestParseFrom_UnknownVersion(t *testing.T) {
	_, _, err := ParseFrom("macos/mavericks")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown macOS version")
}

func TestParseFrom_InvalidPrefix(t *testing.T) {
	_, _, err := ParseFrom("windows/11")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected macos/")
}

func TestVersionNumber(t *testing.T) {
	assert.Equal(t, "15", VersionNumber("sequoia"))
	assert.Equal(t, "14", VersionNumber("sonoma"))
	assert.Equal(t, "13", VersionNumber("ventura"))
	assert.Equal(t, "26", VersionNumber("tahoe"))
	assert.Equal(t, "11", VersionNumber("bigsur"))
	assert.Equal(t, "12", VersionNumber("monterey"))
	assert.Equal(t, "", VersionNumber("mavericks"))
}

func TestFindProduct(t *testing.T) {
	products := []CatalogProduct{
		{ID: "001", Version: "14.3", BaseSystemURL: "https://example.com/14.3/BaseSystem.dmg"},
		{ID: "002", Version: "15.0", BaseSystemURL: "https://example.com/15.0/BaseSystem.dmg"},
		{ID: "003", Version: "15.1", BaseSystemURL: "https://example.com/15.1/BaseSystem.dmg"},
		{ID: "004", Version: "13.6", BaseSystemURL: "https://example.com/13.6/BaseSystem.dmg"},
	}

	t.Run("FindsLatestSequoia", func(t *testing.T) {
		p := FindProduct(products, "sequoia")
		require.NotNil(t, p)
		assert.Equal(t, "003", p.ID)
		assert.Equal(t, "15.1", p.Version)
	})

	t.Run("FindsSonoma", func(t *testing.T) {
		p := FindProduct(products, "sonoma")
		require.NotNil(t, p)
		assert.Equal(t, "001", p.ID)
		assert.Equal(t, "14.3", p.Version)
	})

	t.Run("FindsVentura", func(t *testing.T) {
		p := FindProduct(products, "ventura")
		require.NotNil(t, p)
		assert.Equal(t, "004", p.ID)
	})

	t.Run("UnknownVersionReturnsNil", func(t *testing.T) {
		p := FindProduct(products, "mavericks")
		assert.Nil(t, p)
	})
}
