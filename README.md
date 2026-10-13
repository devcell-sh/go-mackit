# go-mackit

A macOS virtual machine from one YAML file, built without clicking through
the installer, on a Mac, a Linux box, or inside a container.

mackit fetches the installer from Apple's software update catalog, assembles
an OpenCore boot chain, boots it in QEMU, reads the guest screen with OCR to
drive the install UI, runs your commands over SSH, and hands back a qcow2
you can boot or throw away. It is the macOS layer behind
[devcell](https://github.com/DimmKirr/devcell), the sibling of
[go-winkit](https://github.com/devcell-sh/go-winkit) for Windows, and it
works on its own.

## Try it

```sh
brew install qemu           # or apt install qemu-system-x86 on Linux
task install                # builds ./bin/mackit and copies it to ~/.local/bin

mackit build --recovery -f examples/recovery/mackit.yaml macos-recovery.qcow2
mackit start macos-recovery.qcow2
mackit status
mackit stop macos-recovery
```

The first build downloads BaseSystem.dmg from Apple (about 500 MB) and the
OpenCore image, plus the OCR runtime and models on first use. Everything is
cached under `~/.cache/mackit` (`MACKIT_CACHE_DIR` overrides it), so later
builds only pay for the boot. Under software emulation (TCG, the only
option inside a container) booting to macOS Recovery takes about ten
minutes; with HVF or KVM it is a couple of minutes.

## Who this is for

- You need macOS Recovery or a macOS install to appear and disappear on
  command, from a script, with no license dialogs and no hands on a VNC
  client. CI that tests installers, disk tooling, or recovery workflows.
- You maintain a dev environment tool and want macOS as a target without
  owning the boot chain. Use the Go API; devcell does.
- You want to script the macOS installer itself. The screen catalogs and
  step runner in `installer/flow` are data you can extend.

If you want a macOS desktop to use by hand on Apple Silicon, UTM or
[Tart](https://github.com/cirruslabs/tart) are a better fit. mackit is for
the unattended path and for hosts that are not Macs.

## Two ways to build

| Mode | `mackit.yaml` | What you get | Disk |
|---|---|---|---|
| Recovery | `recovery: true` | The macOS Recovery environment (Terminal, Disk Utility, the installer), booted and recognized by OCR. Ephemeral. | ~8 GB sparse |
| Full | default | A real macOS install on a 64 GB disk. On Catalina mackit erases the disk from Recovery's Terminal, runs Reinstall macOS, waits through the installer's reboots, then boots Recovery once more to create your `user`, skip Setup Assistant and enable Remote Login from Terminal, and finally boots the installed system and runs your commands over SSH. | ~40 GB used |

Recovery builds are driven end to end: mackit hides the OpenCore picker,
waits for the Utilities screen, and runs the steps you configure, such as
opening Terminal or clicking through Reinstall macOS. Full builds are
unattended end to end on Catalina (the install, then a headless setup
from Recovery instead of Setup Assistant); see [Not yet](#not-yet) for
the other versions.

## The config

```yaml
from: macos/sequoia         # or macos/catalina, ./BaseSystem.dmg, ./installer.ipsw
hostname: mackit
recovery: false
commands:
  firstboot:
    - systemsetup -setremotelogin on
  boot:
    - cmd: softwareupdate --install-rosetta --agree-to-license
      valid-exit-codes: [0, 1]
      timeout: 15m
ports:
  ssh: 20022                # guest :22
  vnc: 25900                # guest :5900
  forward: ["28080:8080"]
```

`mackit init` writes a fully commented template. `from` accepts any
codename Apple still serves (`catalina` through `tahoe`), a local DMG, or
an IPSW; `mackit images --all` lists what the catalog offers. Commands run
per phase (`firstboot`, `boot`) with per command timeout, retries, and
accepted exit codes, or as script files under `hooks/<phase>/` next to the
config.

## Examples

Each directory is a complete `mackit.yaml`. They are loaded by the e2e
tests so they cannot drift from the schema.

- [`recovery`](examples/recovery): macOS Catalina Recovery, the mode the
  OCR screen catalog is verified against.
- [`full-catalina`](examples/full-catalina): a full Catalina install,
  driven from Recovery to an SSH-reachable system.
- [`plain`](examples/plain): a full Sequoia install with SSH enabled.
- [`nix`](examples/nix): a full install with Rosetta and the Nix installer
  run as boot commands.
- [`library-consumer`](examples/library-consumer): the Go API, one
  `mackit.Build` call with no YAML.

## Beyond build and start

`mackit status`, `logs`, `ssh`, and `stop` manage running VMs; `logs -f`
follows the serial console. `--debug` on `build` saves every frame the
OCR driver reads to `<work>/screenshots/` and what it read from each
frame as JSON to `<work>/ocr/`, named by UTC time, screen, and sequence
so the two line up. The guest console (firmware, OpenCore, and the kernel
with `-v serial=1`) is forwarded into the build log, each line tagged with
its source (`uefi`, `opencore`, `xnu`) next to mackit's own records
(`service=mackit`).

## As a library

```sh
go get github.com/devcell-sh/go-mackit
```

`mackit.Build` runs a build from a `build.Config`; `mackit.Start` boots a
built image and returns a `vm.VM`; `mackit.Status` lists what is running.
Underneath, `media` fetches installers from Apple's catalog, `installer`
prepares DMGs, IPSWs, and the OpenCore image, `installer/flow` holds the
per-version screen catalogs and the step runner, `vm/qemu` is the QEMU
backend (QMP, screendumps, keyboard and tablet input, serial), and
`build` ties them together.

Screen reading comes from
[go-textshot](https://github.com/devcell-sh/go-textshot), which is not
tagged yet: until it is, clone it next to this repo and point a `go.work`
at both. There is no v1 tag of mackit either. Pin a commit if you depend
on it.

## Not yet

| Gap | Status | Where |
|---|---|---|
| Setup Assistant | never shown: after the install the build boots Recovery again and `flow.HeadlessSetup` creates the user, marks setup done and enables Remote Login from Terminal. The user's post-login prompts (iCloud, analytics) are untouched and appear at a GUI login | `installer/flow/headless_setup.go`, `build/build.go` |
| Screen catalogs for versions other than Catalina | Sequoia is a best guess, unverified; other versions are missing. A catalog is a list of screens and phrases | `installer/flow/<version>.go` |
| arm64 guests | not supported. OpenCore boots x86_64 only; Apple Silicon macOS would need QEMU's `vmapple` machine | `vm/qemu`, `installer/opencore.go` |
| OpenCore's own log | the bundled OpenCore is a RELEASE build and logs only warnings and errors; the kernel console is what you get | `installer/opencore_config.go` |
| Unattended Full build in one run | every step is verified live on Catalina (2026-10-10): the install flow through the installer's reboots, the headless setup, and SSH login to the installed system (which needs the `vmxnet3` NIC; see `build.GuestNIC`). The end-to-end run (`MACKIT_INSTALL_UNTIL=all`) is pending; the flow is verified a step at a time (`build.Config.StopAfterStep`, `MACKIT_INSTALL_UNTIL`, `MACKIT_VM_MEMORY_GB`) | `installer/flow/catalina_install.go`, `test/e2e/full_e2e_test.go` |

Small PRs that add a screen catalog or a step come with a recorded
screenshot under `test/testdata/screens/<version>/` and a unit test
against its OCR lines, which is how the Catalina catalog was built.

## Limits

- QEMU only: HVF on macOS, KVM on Linux, TCG anywhere else. No
  Virtualization.framework, no Hyper-V.
- Apple's software license is yours to comply with. mackit downloads
  public installer media from Apple and does not patch or activate the OS.
- OCR needs cgo and a platform ONNX Runtime ships for (linux and darwin,
  amd64 and arm64). Without it, builds fall back to a blind Enter at the
  picker and cannot run Recovery steps.

## Testing

| Tier | Command | Needs |
|---|---|---|
| Unit | `task test` | nothing |
| FAT and OpenCore image | `go test ./vm/qemu/ ./installer/` | the cached OpenCore image for the real-image cases, `mtools` for the firmware cross-check (skipped otherwise) |
| Boot | `MACKIT_E2E=1 go test -run TestBoot_VerboseSerial -timeout 20m -v ./test/e2e/` | QEMU, network for the first download |
| Recovery flow | `MACKIT_E2E=1 go test -run TestRecoveryFlow -timeout 50m -v ./test/e2e/` | the above, about ten minutes per test under TCG |
| Full install | `MACKIT_E2E=1 MACKIT_INSTALL_UNTIL=all go test -run TestExampleFull_Install -timeout 420m -v ./test/e2e/` | the above plus network in the guest; about an hour under HVF, four under TCG, to a flattened image with SSH |

Boot tests write `build.jsonl`, the serial log, screenshots, and OCR
readings under `test/results/<timestamp>-<Test>/`, which is gitignored
and kept so a failure can be read after the fact.
