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
2. Run `build.Config.RecoverySteps` (for example `flow.OpenTerminal`).

When OCR is unavailable (no cgo, unsupported platform, download failure) the
build logs why and falls back to the blind Enter after 10 s. With `--debug`
(or the e2e logger) every frame the driver OCRs is saved to
`<work>/screenshots/<ISO 8601 basic UTC>-<screen>-<seq>.png` (for example
`20261010T055339Z-recovery.utilities-0002.png`) and what OCR read from it
to `<work>/ocr/<same base>.json` (`flow.Reading`: screen, size, frame path,
text lines with boxes and scores, detected elements when the detector ran). The periodic captures use the
same scheme with the label `periodic`
(`20261010T055434Z-periodic-0001.png`), so a run's screenshots sort as one
timeline. The basic profile has no colons, which Finder shows as slashes.

Catalogs are per macOS version in `installer/flow/<version>.go`: screens are
matched by normalized OCR text (`All` and `Any` phrases, first match wins,
most specific first). Steps are data: `{Screen, Elements, Timeout, Optional,
Actions}` with actions `Keys`, `Chord`, `Click(selector)`, `ClickAt`, `Type`,
`Sleep`, and `Reactions` for screens that may interrupt the wait. Elements
are addressed by selector: `text:Reinstall macOS`, `button:Continue`,
`icon@700,310` (textshot element keys), or a catalog element name. A step
that lists selectors in `Elements` waits until they resolve, so
`{Screen: "recovery.utilities", Elements: ["text:Reinstall macOS"], Actions:
[Click("text:Reinstall macOS")]}` reads "when Reinstall macOS is on screen,
click it". Screen identification uses cheap OCR text; the UI element detector
(several seconds per frame) only runs for steps that use selectors.

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
