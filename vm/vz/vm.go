//go:build darwin && arm64

package vz

import (
	"fmt"
	"sync"

	"github.com/Code-Hex/vz/v3"
)

type vmHandle struct {
	machine   *vz.VirtualMachine
	outputDir string
	sshPort   uint16

	mu     sync.Mutex
	doneCh chan struct{}
	err    error
}

func newVMHandle(machine *vz.VirtualMachine, outputDir string, sshPort uint16) *vmHandle {
	return &vmHandle{
		machine:   machine,
		outputDir: outputDir,
		sshPort:   sshPort,
		doneCh:    make(chan struct{}),
	}
}

func (h *vmHandle) Stop() error {
	select {
	case <-h.doneCh:
		_ = h.machine.Stop()
		return nil
	default:
	}
	if err := h.machine.Stop(); err != nil {
		return err
	}
	<-h.doneCh
	return nil
}

func (h *vmHandle) Wait() error {
	<-h.doneCh
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *vmHandle) SSHAddr() string       { return fmt.Sprintf("127.0.0.1:%d", h.sshPort) }
func (h *vmHandle) OutputDir() string     { return h.outputDir }
func (h *vmHandle) PID() int              { return 0 }
func (h *vmHandle) Done() <-chan struct{} { return h.doneCh }

// Machine returns the underlying VZ machine for VNC server setup.
func (h *vmHandle) Machine() *vz.VirtualMachine { return h.machine }

func (h *vmHandle) markDone() {
	select {
	case <-h.doneCh:
	default:
		close(h.doneCh)
	}
}

func (h *vmHandle) setErr(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}
