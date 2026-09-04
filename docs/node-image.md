# NexusOS Minimal Node Image

**Milestone**: post–Phase 2 freeze — ship a buildable path for a QEMU-bootable **Debian Bookworm** x86_64 node image with **containerd + runc + NexusOS coordinator** (systemd).

**Out of scope**: CometBFT, Phase 3 orchestration, CRIU, UI.

## Tool choice

We use **[mmdebstrap](https://gitlab.mister-muffin.de/josch/mmdebstrap)** to create a Debian Bookworm rootfs, then assemble a BIOS/MBR partitioned **raw** disk and a compressed **qcow2**.

Rationale: single-purpose, few moving parts, works on a normal Linux builder with root; avoids pulling in mkosi’s broader host coupling or debos’ extra YAML/runtime stack. See `docs/decisions.md` (2026-09-04).

## What the image contains

| Component | Role |
|-----------|------|
| Debian Bookworm (minbase + listed packages) | Minimal Linux node OS |
| `linux-image-amd64` + GRUB (BIOS) | Boot under QEMU/`qemu-system-x86_64` |
| containerd + runc | OCI runtime (`--mock=false`) |
| `nexusos-coordinator` + `nexusctl` | From this repo’s `coordination/` build |
| systemd units | `containerd`, `nexusos-coordinator`, `nexusos-ready` (serial marker) |
| `/etc/nexusos/` | Tokens/TLS documentation + env example |

Package list: `image/packages.list`. Overlay: `image/overlay/`.

## Security bar (unchanged from Phase 2)

Outside `--dev` / `--insecure-dev`, the coordinator **requires**:

1. API token (`NEXUS_API_TOKEN` / `--api-token`)
2. Join token (`NEXUS_JOIN_TOKEN` / `--join-token`)
3. TLS cert + key (`--tls-cert` / `--tls-key`)

Production images install a unit that reads `/etc/nexusos/coordinator.env` and expects `/etc/nexusos/tls.crt` + `tls.key`. Copy from `coordinator.env.example`, install real secrets, then enable the service.

**`--dev` is only for local QEMU smoke** (`./image/build.sh --dev-smoke`). That build bakes smoke tokens and passes `--dev` so the coordinator can auto-generate self-signed TLS under `/var/lib/nexusos`. Do not deploy smoke images.

## Host requirements (builder)

- Linux x86_64
- Root (`sudo`) for loop mounts + `grub-install` into the disk image
- Packages: `mmdebstrap`, `qemu-utils`, `qemu-system-x86` (for smoke), `parted`, `e2fsprogs`, `rsync`, `grub-pc` (provides host `grub-install`; `grub-pc-bin` alone is not enough)
- Go 1.22+ (to compile binaries into the image), or prebuild `bin/` and pass `--skip-go-build`
- Network to a Debian mirror (`MIRROR`, default `http://deb.debian.org/debian`)
- Disk: several GB free under `dist/node-image/` (override with `OUT_DIR`)
- For smoke: QEMU TCG works everywhere; set `SMOKE_KVM=1` when `/dev/kvm` is known-good (some hosts expose `/dev/kvm` but guest execution hangs)
- Builder kernels with `loop.max_part=0` need host `grub-install` against the raw file (the build script handles this)

Image build is **manual** — not run in GitHub Actions by default (too heavy / needs privileged loop devices). CI sample for `go test ./...` is in `docs/examples/go-test.yml` (copy to `.github/workflows/` if your token has the `workflow` scope). Image builds stay manual.

## Build

```bash
# Production-oriented image (operator must supply tokens + TLS before coordinator stays up)
sudo ./image/build.sh

# Local QEMU smoke image (--dev + fixed tokens)
sudo ./image/build.sh --dev-smoke

# Rootfs only (no disk image)
sudo ./image/build.sh --rootfs-only
```

Outputs (default `dist/node-image/`):

- `nexusos-node-bookworm-amd64.raw` / `.qcow2`
- or `*-devsmoke.raw` / `.qcow2` with `--dev-smoke`
- `MANIFEST.txt`

Environment knobs: `OUT_DIR`, `WORK_DIR`, `SUITE` (default `bookworm`), `ARCH`, `MIRROR`, `DISK_SIZE` (default `4G`).

## QEMU smoke

```bash
sudo ./image/build.sh --dev-smoke
./scripts/qemu-node-smoke.sh dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2
```

The smoke script:

1. Boots the qcow2 with user-mode networking and `hostfwd` of host `:18080` → guest `:8080`
2. Waits for serial marker `NEXUSOS_READY` (emitted when `/health` succeeds inside the guest)
3. Probes `https://127.0.0.1:18080/health` with `smoke-api-token`

Then:

```bash
./bin/nexusctl --api https://127.0.0.1:18080 --token smoke-api-token --insecure health
./bin/nexusctl --api https://127.0.0.1:18080 --token smoke-api-token --insecure node
```

## Production first boot (non-smoke image)

```bash
# Inside the guest (serial/SSH), as root:
cp /etc/nexusos/coordinator.env.example /etc/nexusos/coordinator.env
# edit NEXUS_API_TOKEN, NEXUS_JOIN_TOKEN, NEXUS_ADVERTISE
openssl req -x509 -newkey rsa:2048 -nodes -keyout /etc/nexusos/tls.key \
  -out /etc/nexusos/tls.crt -days 365 -subj /CN=nexusos-node
chmod 0600 /etc/nexusos/tls.key /etc/nexusos/coordinator.env
systemctl enable --now containerd nexusos-coordinator
```

Peer pairing and ledger sync remain as documented in the README (Phase 2 HTTPS + tokens).

## Layout

```
image/
├── README.md
├── packages.list
├── build.sh
├── hooks/customize-rootfs.sh
└── overlay/          # hostname, grub defaults, systemd units, /etc/nexusos
scripts/qemu-node-smoke.sh
dist/node-image/      # build output (gitignored)
```

## Verified vs needs a real builder

| Check | Where |
|-------|--------|
| `cd coordination && go test ./...` | Any Go host / CI |
| `image/` scripts + docs present and executable | This repo |
| Full `mmdebstrap` → qcow2 build | Linux builder **with root** + mirror access |
| QEMU smoke + `NEXUSOS_READY` | Builder/host with **KVM** (or slow TCG) + the built `*-devsmoke.qcow2` |
| containerd workloads inside the guest | Same as smoke; needs network for image pulls |

If this environment cannot finish a full image build, the tooling and docs above are still the supported path on a proper builder.

## Verification notes (2026-09-04)

On a Debian builder with root + mmdebstrap:

- Full `sudo ./image/build.sh --dev-smoke` completed (mmdebstrap Bookworm rootfs → raw + qcow2).
- `./scripts/qemu-node-smoke.sh` **PASS** under **TCG** (guest printed `NEXUSOS_READY`; hostfwd HTTPS `/health` + `/v1/node` with smoke token).
- `/dev/kvm` was present but guest execution hung with KVM in this environment; smoke defaults to TCG (`SMOKE_KVM=1` to opt in).
- Kernel `loop.max_part=0`: build uses host `grub-install` on the raw file + offset `losetup` for the rootfs partition.
