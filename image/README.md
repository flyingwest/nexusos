# NexusOS node image

Build tooling for a **Debian Bookworm** x86_64 minimal node disk image with:

- containerd + runc
- NexusOS coordinator (systemd)
- Phase 2 security bar: API token + join-token + TLS (outside `--dev`)
- Production first-boot fail-closed: `nexusos-firstboot` + `nexusos-provision` (tokens/TLS required before coordinator starts)

**Image builder**: [mmdebstrap](https://gitlab.mister-muffin.de/josch/mmdebstrap) — chosen as the simplest viable Debian rootfs tool; see `docs/decisions.md` and `docs/node-image.md`.

```bash
# On a Linux host with root; KVM recommended for smoke:
sudo ./image/build.sh                 # production-oriented (coordinator not enabled until provisioned)
sudo ./image/build.sh --dev-smoke     # local QEMU smoke (--dev, auto-start)
./scripts/qemu-node-smoke.sh dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2

# Provision helper unit test (no root / no image rebuild):
./scripts/test-nexusos-provision.sh
```

Full docs: [`docs/node-image.md`](../docs/node-image.md).
