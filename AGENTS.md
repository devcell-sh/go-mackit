# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

mackit builds macOS VM images from a YAML config, unattended:
fetches install media (BaseSystem.dmg from Apple's catalog), assembles an
OpenCore-based boot chain in QEMU, provisions over SSH, and hands back a
bootable qcow2. It is the macOS layer behind
[devcell](https://github.com/DimmKirr/devcell).

Two build modes: **Full** (complete macOS install, ~40 GB qcow2) and
**Recovery** (macOS Recovery environment only, much smaller).

QEMU is the only VM backend. Works on any host hardware:
macOS (HVF), Linux (KVM), or containers (TCG).

## Build and test

```sh
task build           # builds the host CLI -> ./mackit
task install         # install to $GOPATH/bin
task test            # unit tests
task test:race       # with race detector
task test:cover      # with coverage report
go test ./vm/...     # single package
go test -run TestFoo ./internal/config/   # single test
```

## Package map

| Package | Purpose |
|---------|---------|
| `.` (mackit) | Library entry point: `Build()` and `Start()` |
| `build/` | Build orchestration: Full/Recovery flows, SSH provisioning |
| `build/buildopts/` | Build options: hooks, ports, stages, media source detection |
| `cache/` | Cache directory resolution (`~/.cache/mackit`) |
| `cmd/mackit/` | CLI binary entry point |
| `installer/` | Installer media prep: DMG validation, IPSW extraction, OpenCore |
| `installer/flow/` | OCR-driven installer UI: per-macOS-version screen catalogs (`Catalina` verified, `Sequoia` best guess), element positions, step runner (keys, clicks, typing) |
| `internal/cli/` | Cobra command tree: build, start, stop, status, ssh, logs |
| `internal/config/` | YAML config parsing (`mackit.yaml`) and conversion to BuildOpts |
| `media/` | macOS installer fetch from Apple software update catalog, plist parsing |
| `vm/` | Backend-agnostic VM interfaces (`VMBackend`, `VM`) |
| `vm/qemu/` | QEMU backend: spec, command builders, disk ops, QMP, screendump + keyboard/pointer input (`QMPInput`) |
| `vm/vmstate/` | VM state persistence (JSON in `~/.mackit/run/`) |

## Boot sequence (QEMU + OpenCore)

EDK2 UEFI firmware -> OpenCore.qcow2 -> macOS boot.efi -> macOS kernel

Before each build the cached OpenCore image is copied with its
`EFI/OC/config.plist` patched in place (`installer.PatchOpenCorePlist`,
`qemu.ReplaceFileInFATQcow2`): picker hidden, OpenCore's own log to console
and serial (`Misc.Debug.Target` 0x0B, `DisplayLevel` 0x80000042,
`Misc.Serial.Init`), and `-v serial=1` added to boot-args so the kernel
prints its verbose console on screen and to COM1. `ScanPolicy` stays as
shipped: 0 puts OpenCore's own ESP first and, with no picker, OpenCore
chainloads itself and hangs black ("Already started"). The image ships
RELEASE OpenCore binaries, so OpenCore itself only logs warnings and
errors; the kernel console is the bulk of the serial log. Patch in place,
not rebuild: go-diskfs 1.9 writes FAT32 images whose FAT is two entries short,
which go-diskfs reads back fine but dosfstools, mtools and the firmware
reject (OpenCore then fails to load its resources and hangs on a black
screen). go-diskfs also wants io/fs paths (`EFI/OC`, root `.`), never a
leading slash. `vm/qemu/fat_test.go` cross-checks with mtools when
installed.

## Logging

Builds and runs log through `log/slog`. Every record carries a `service`
attribute naming the component that wrote it (`internal/logx` adds
`service=mackit` to anything untagged):

| service | Source |
|---------|--------|
| `mackit` | mackit itself; `component` narrows it (`flow`, `screenshot`) |
| `textshot` | the OCR engine |
| `uefi`, `opencore`, `xnu` | guest console lines forwarded from `<work>/serial.log` by `qemu.TailSerial` (`stream=serial`, Debug level, kernel panics at Warn) |

`TailSerial` attributes lines by prefix (`BdsDxe:`, OpenCore's
`00:038 00:030 OCM:` timestamps, kernel `000003.339260` timestamps) and
otherwise by boot phase. In e2e runs everything lands in
`test/results/<run>/build.jsonl`; `TestBoot_VerboseSerial` is the short
boot check that the verbose console shows on screen (OCR identifies
`boot.verbose`) and that OpenCore and kernel lines reach the log.

## Config file

`mackit.yaml` (or `.yml`). Fields:

- `from`: media source (`macos/sequoia`, `./path.dmg`, `./path.ipsw`)
- `hostname`: guest hostname (RFC 1123)
- `recovery`: bool, build recovery environment only
- `ports`: `ssh` (default 20022), `vnc` (default 25900), `forward` list
- `commands`: flat list (boot phase) or map (`firstboot`, `boot`)

## Hook phases

| Phase | When |
|-------|------|
| `firstboot` | After initial macOS install, first boot |
| `boot` | Every subsequent boot |

## Port defaults

| Service | Guest | Host default |
|---------|-------|-------------|
| SSH | 22 | 20022 |
| VNC | 5900 | 25900 |

## Environment variables

| Variable | Purpose |
|----------|---------|
| `MACKIT_VM_BACKEND` | Override VM backend (default: qemu) |
| `MACKIT_CACHE_DIR` | Override cache directory |
| `TEXTSHOT_ONNXRUNTIME_LIB` | Use a local `libonnxruntime` for OCR instead of the downloaded one |
| `TEXTSHOT_CACHE_DIR` | Override the OCR cache (default `<mackit cache>/textshot`) |

## Screen detection (OCR)

Recovery and Full builds read the guest display over QMP and OCR it with
[go-textshot](https://github.com/devcell-sh/go-textshot) (ONNX Runtime +
PP-OCRv6, downloaded on first use into `<cache>/textshot`). `build/screen.go`
builds a `flow.Runner` from the catalog for the configured macOS version:

1. Wait for `recovery.utilities` (macOS Utilities), pressing Enter whenever the
   OpenCore picker is seen (a `Reaction`); the blind Enter after 10 s stays as
   a safety net. Logs `macOS Recovery reached`.
2. Recovery builds run `build.Config.RecoverySteps` (for example
   `flow.OpenTerminal`). Full builds run `flow.InstallStepsFor(version, 64)`:
   on Catalina `flow.EraseDisk` (Terminal, `diskutil eraseDisk` on the disk
   whose `diskutil list` size matches 64 GiB) then `flow.CatalinaReinstall`,
   select the volume, Install, wait for `installer.progress` and then for
   the display to switch to the installed system's mode (`Step.Display`,
   1280x800 against the installer's 1920x1080, held 30 s because every boot
   passes through it briefly; log: `macOS installed, installed system
   booted`). The build then stops that VM, starts a fresh one with the
   install media still attached (which boots Recovery: the installer's
   bless does not survive a QEMU restart) and runs `flow.HeadlessSetup`:
   Terminal, one typed script that creates the admin user on the Data
   volume with `dscl`, copies the home from the User Template, adds a
   sudoers drop-in, touches `.AppleSetupDone`, and clears sshd's launchd
   override (label `com.openssh.sshd`), then `shutdown -h now` (log:
   `headless setup done, ...`). Last, the installed disk boots alone, the
   build waits for the SSH banner, runs hooks as that user over password
   SSH (`golang.org/x/crypto/ssh`), shuts down with `sudo`, and flattens.
   `TestExampleFull_Install` with `MACKIT_INSTALL_UNTIL=all` stops at
   `build complete`.

Catalina guests need the `vmxnet3` NIC (`build.GuestNIC`, `qemu.Spec.NIC`):
on `virtio-net-pci` the installed system's en0 sends DHCP discovers from a
random MAC and never sees slirp's offers, although Recovery downloads the
installer over the same device. `mackit start` picks the NIC from the macOS
codename in the image name, or `--nic`.

The install flow was learned one step at a time against a live VM:
`build.Config.StopAfterStep` runs the flow up to a named step and then idles
with the VM up (`KeepVM` leaves it running when the build returns), and the
e2e test exposes that as `MACKIT_INSTALL_UNTIL=<step>|all` with the verified
frontier in `installFrontier`. `MACKIT_VM_MEMORY_GB` shrinks the VM for a
memory-capped container (a 4 GiB cgroup needs 3; the installer touches all
guest memory and the kernel kills QEMU silently). From the container the VM
is reachable over QEMU's VNC on the configured port (`.scratch/tools/vnc-lab`
for screenshots; vncdo typing garbles shifted characters) and over its QMP
socket from `work/qemu.log` (`.scratch/tools/flow-lab click|keys|type|shot`,
the runner's own input path). Read the saved `work/ocr/*.json` for element
boxes and styles, try the action, then codify it as a selector-based step.

When OCR is unavailable (no cgo, unsupported platform, download failure) the
build logs why and falls back to the blind Enter after 10 s. With `--debug`
(or the e2e logger) every frame the driver OCRs is saved to
`<work>/screenshots/<ISO 8601 basic UTC>-<screen>-<seq>.png` (for example
`20261010T055339Z-recovery.utilities-0002.png`) and what OCR read from it
to `<work>/ocr/<same base>.json` (`flow.Reading`: screen, size, frame path,
text lines with boxes and scores, and when the detector ran the theme,
backdrop, and each element with its measured style: colours, contrast,
disabled, highlighted, focused). The periodic captures use the
same scheme with the label `periodic`
(`20261010T055434Z-periodic-0001.png`), so a run's screenshots sort as one
timeline. The basic profile has no colons, which Finder shows as slashes.

Catalogs are per macOS version in `installer/flow/<version>.go`: screens are
matched by normalized OCR text (`All` and `Any` phrases, first match wins,
most specific first). Steps are data: `{Screen, Elements, Text, Timeout, Optional,
Actions}` (`Text` waits for phrases among the OCR lines without the
element detector) with actions `Keys`, `Chord`, `Click(selector)`, `ClickAt`, `Type`,
`Sleep`, and `Reactions` for screens that may interrupt the wait. Elements
are addressed by selector: `text:Reinstall macOS`, `button:Continue`,
`icon@700,310` (textshot element keys), or a catalog element name. A step
that lists selectors in `Elements` waits until they resolve, so
`{Screen: "recovery.utilities", Elements: ["text:Reinstall macOS"], Actions:
[Click("text:Reinstall macOS")]}` reads "when Reinstall macOS is on screen,
click it". Selectors also accept textshot keys verbatim (`icon:Disk Utility`,
`button:Agree#2`).

The runner spends model time only where it pays off (`installer/flow/runner.go`):

- A capture that has not changed since the last frame read with the models
  (`textshot.Diff` below `ChangeThreshold`, 0.05%) reuses that reading; no
  OCR, no frame saved. The comparison is against the last *read* frame, not
  the previous capture, so slow changes (Terminal output, a progress bar)
  add up and get read; a static screen's cursor or spinner stays under
  0.02% and does not.
- Screen identification uses cheap OCR text. The element detector (several
  seconds per frame) runs only when the step needs elements *and* the text
  already shows the wanted screen and every text selector; `detectorWanted`
  logs why it was skipped.
- `DetectScreen` is composed from the lines already read plus
  `DetectInteractables` (`flow.NewEngineOCR`), so OCR never runs twice.
- Element `Style` is used: a greyed-out button (`Style.Disabled`) never
  resolves, so a step waiting for `button:Install` waits until it is enabled
  and the timeout error says "on screen but disabled"; a highlighted
  (default) button is pressed with Enter instead of a click
  (`EnterOnDefault`).
- After each action the runner waits for the screen to stop changing
  (`SettleTimeout`, captures every `SettleDelay`) instead of a fixed pause.

Guest input goes over QMP to the USB keyboard and tablet on the chipset EHCI
controller (`-usb`), addressed by device id. Under `qemu-xhci` macOS Recovery
ignored both devices. Verified screenshots live
in `test/testdata/screens/<version>/`. The `examples/recovery` config boots
Catalina, which is why that catalog is the verified one.

During development go-textshot is consumed through `go.work` (unpublished).

## Build stages

| Stage | Output | Use case |
|-------|--------|----------|
| `full` | ~40GB qcow2 | Persistent macOS environment |
| `recovery` | Small qcow2 | macOS Recovery for testing |

## Accelerators

| Accel | Platform | Speed |
|-------|----------|-------|
| `hvf` | macOS | Near-native |
| `kvm` | Linux | Near-native |
| `tcg` | Any (containers) | Software emulation, slow |

## Architecture

Auto-detects host architecture for default guest:
- `arm64` host -> `macos/sequoia-arm64`
- `amd64` host -> `macos/sequoia-x86_64`

Both architectures supported equally via QEMU.
