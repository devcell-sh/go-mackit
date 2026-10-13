package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuestNIC(t *testing.T) {
	assert.Equal(t, "vmxnet3", GuestNIC("catalina"))
	assert.Equal(t, "vmxnet3", GuestNIC("mojave"))
	assert.Equal(t, "virtio-net-pci", GuestNIC("bigsur"))
	assert.Equal(t, "virtio-net-pci", GuestNIC("sequoia"))
	assert.Equal(t, "virtio-net-pci", GuestNIC(""))
}
