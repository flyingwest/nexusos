# NexusOS node image

Build tooling for a **Debian Bookworm** x86_64 minimal node disk image with:

- containerd + runc
- NexusOS coordinator (systemd)
- Phase 2 security bar: API token + join-token + TLS (outside `--dev`)

**Image builder**: [mmdebstrap](https://gitlab.mister-muffin.de/josch/mmdebstrap) — chosen as the simplest viable Debian rootfs tool; see `docs/decisions.md` and `docs/node-image.md`.

```bash
# On a Linux host with root; KVM recommended for smoke:
sudo ./image/build.sh                 # production-oriented
sudo ./image/build.sh --dev-smoke     # local QEMU smoke (--dev)
./scripts/qemu-node-smoke.sh dist/node-image/nexusos-node-bookworm-amd64-devsmoke.qcow2
```

Full docs: [`docs/node-image.md`](../docs/node-image.md).
