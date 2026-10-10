package buildopts

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type HookPhase int

const (
	FirstBoot HookPhase = iota
	Boot
)

func (p HookPhase) String() string {
	switch p {
	case FirstBoot:
		return "firstboot"
	case Boot:
		return "boot"
	default:
		return fmt.Sprintf("HookPhase(%d)", int(p))
	}
}

type FileInject struct {
	Src  string
	Dest string
}

type Hook struct {
	Phase          HookPhase
	Label          string
	Cmd            string
	Files          []FileInject
	Env            map[string]string
	ValidExitCodes []int
	Timeout        time.Duration
	Retries        int
}

func (h *Hook) applyDefaults() {
	if h.Timeout == 0 {
		h.Timeout = 5 * time.Minute
	}
	if len(h.ValidExitCodes) == 0 {
		h.ValidExitCodes = []int{0}
	}
}

const (
	DefaultSSHPort uint16 = 20022
	DefaultVNCPort uint16 = 25900
)

type Ports struct {
	SSH     uint16
	VNC     uint16
	Forward []string
}

func ParseForward(spec string) (host, guest uint16, err error) {
	h, g, ok := strings.Cut(strings.TrimSpace(spec), ":")
	if !ok {
		return 0, 0, fmt.Errorf("forward %q: want host:guest", spec)
	}
	parse := func(name, s string) (uint16, error) {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n < 1 || n > 65535 {
			return 0, fmt.Errorf("forward %q: %s port must be 1-65535", spec, name)
		}
		return uint16(n), nil
	}
	if host, err = parse("host", h); err != nil {
		return 0, 0, err
	}
	if guest, err = parse("guest", g); err != nil {
		return 0, 0, err
	}
	return host, guest, nil
}

func (p Ports) SSHOrDefault() uint16 {
	if p.SSH != 0 {
		return p.SSH
	}
	return DefaultSSHPort
}

func (p Ports) VNCOrDefault() uint16 {
	if p.VNC != 0 {
		return p.VNC
	}
	return DefaultVNCPort
}

type Stage string

const (
	StageFull     Stage = "full"
	StageRecovery Stage = "recovery"
)

type FromKind int

const (
	FromCatalog FromKind = iota
	FromDMG
	FromIPSW
)

func DetectFrom(from string) FromKind {
	lower := strings.ToLower(from)
	switch {
	case strings.HasSuffix(lower, ".dmg"):
		return FromDMG
	case strings.HasSuffix(lower, ".ipsw"):
		return FromIPSW
	default:
		return FromCatalog
	}
}

type BuildOpts struct {
	From            string
	Recovery        bool
	Hooks           []Hook
	Ports           Ports
	Hostname        string
	OpenCoreVersion string
}

func (o *BuildOpts) Validate() error {
	if o.Recovery {
		for _, h := range o.Hooks {
			if h.Phase != Boot {
				return fmt.Errorf("recovery mode only supports boot phase hooks, got %s", h.Phase)
			}
		}
	}
	return nil
}

func (o *BuildOpts) ApplyDefaults() {
	if o.From == "" {
		o.From = DefaultFrom()
	}
	for i := range o.Hooks {
		o.Hooks[i].applyDefaults()
	}
}

func (o *BuildOpts) SortHooks() {
	sort.SliceStable(o.Hooks, func(i, j int) bool {
		return o.Hooks[i].Phase < o.Hooks[j].Phase
	})
}

func (o *BuildOpts) Stage() Stage {
	if o.Recovery {
		return StageRecovery
	}
	return StageFull
}

func DefaultFrom() string {
	switch runtime.GOARCH {
	case "amd64":
		return "macos/sequoia-x86_64"
	default:
		return "macos/sequoia-arm64"
	}
}
