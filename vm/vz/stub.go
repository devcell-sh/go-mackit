//go:build !(darwin && arm64)

// Package vz provides an experimental Apple Virtualization.framework backend.
//
// Status: not production-ready. mackit's primary backend is QEMU (vm/qemu),
// which handles x86_64 and arm64 via HVF, KVM, or TCG.
package vz

import (
	"context"
	"fmt"
	"image"
	"log/slog"

	"github.com/devcell-sh/go-mackit/vm"
)

var errUnavailable = fmt.Errorf("vz backend requires darwin/arm64 and is experimental; use the QEMU backend")

type Backend struct {
	Logger *slog.Logger
}

func (b *Backend) DefaultDiskFormat() vm.DiskFormat                                         { return vm.DiskFormatRaw }
func (b *Backend) CreateDisk(path string, sizeGB int, format vm.DiskFormat) error           { return errUnavailable }
func (b *Backend) CreateOverlay(path, base string) error                                    { return errUnavailable }
func (b *Backend) Flatten(src, dst string) error                                            { return errUnavailable }
func (b *Backend) StartInstall(ctx context.Context, cfg vm.VMInstallConfig) (vm.VM, error)  { return nil, errUnavailable }
func (b *Backend) StartRun(ctx context.Context, cfg vm.VMRunConfig) (vm.VM, error)          { return nil, errUnavailable }

func StartRecovery(ctx context.Context, cfg vm.VMRunConfig, logger *slog.Logger) (vm.VM, error) { return nil, errUnavailable }
func FetchIPSW(ctx context.Context, cacheDir string, logger *slog.Logger) (string, error)      { return "", errUnavailable }

func StartVNCServer(v vm.VM) (int, error) { return 0, errUnavailable }

// VNCClient stub for non-darwin builds.
type VNCClient struct{}

func DialVNC(addr string, logger ...*slog.Logger) (*VNCClient, error)    { return nil, errUnavailable }
func (c *VNCClient) Capture(ctx context.Context) (image.Image, error)    { return nil, errUnavailable }
func (c *VNCClient) Close() error                                        { return nil }

// VNCInput stub.
type VNCInput struct{}

func NewVNCInput(client *VNCClient) *VNCInput                                                    { return nil }
func (v *VNCInput) SendKeys(ctx context.Context, chord ...string) error                          { return errUnavailable }
func (v *VNCInput) Click(ctx context.Context, pt image.Point, display image.Rectangle) error     { return errUnavailable }
func (v *VNCInput) RightClick(ctx context.Context, pt image.Point, display image.Rectangle) error { return errUnavailable }
func (v *VNCInput) Type(ctx context.Context, text string) error                                  { return errUnavailable }
