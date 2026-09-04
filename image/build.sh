#!/usr/bin/env bash
# Build a Debian Bookworm x86_64 NexusOS minimal node disk image (raw + qcow2).
#
# Tool choice: mmdebstrap (see docs/decisions.md).
#
# Host requirements (see docs/node-image.md):
#   - Linux x86_64 with root (sudo)
#   - mmdebstrap, qemu-utils, parted, e2fsprogs, rsync, grub-pc-bin
#   - Go 1.22+ to compile coordinator/nexusctl into the image
#   - Network access to deb.debian.org (or set MIRROR)
#
# Usage:
#   sudo ./image/build.sh              # production image (fail-closed; run nexusos-provision before coordinator)
#   sudo ./image/build.sh --dev-smoke  # QEMU smoke image with --dev + fixed tokens
#   sudo ./image/build.sh --rootfs-only
#
set -euo pipefail

PATH="/usr/sbin:/sbin:/usr/bin:/bin:${PATH:-}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="${OUT_DIR:-$ROOT/dist/node-image}"
WORK_DIR="${WORK_DIR:-$OUT_DIR/work}"
SUITE="${SUITE:-bookworm}"
ARCH="${ARCH:-amd64}"
MIRROR="${MIRROR:-http://deb.debian.org/debian}"
DISK_SIZE="${DISK_SIZE:-4G}"
DEV_SMOKE=0
ROOTFS_ONLY=0
SKIP_GO_BUILD=0
REUSE_ROOTFS=0

usage() {
  sed -n '1,20p' "$0" | sed 's/^# \?//'
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dev-smoke) DEV_SMOKE=1; shift ;;
    --rootfs-only) ROOTFS_ONLY=1; shift ;;
    --reuse-rootfs) REUSE_ROOTFS=1; shift ;;
    --skip-go-build) SKIP_GO_BUILD=1; shift ;;
    --out) OUT_DIR="$2"; WORK_DIR="$OUT_DIR/work"; shift 2 ;;
    --size) DISK_SIZE="$2"; shift 2 ;;
    -h|--help) usage 0 ;;
    *) echo "unknown arg: $1" >&2; usage 1 ;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }; }

need mmdebstrap
need qemu-img
need parted
need mkfs.ext4
need rsync
need losetup
need mount
need umount
need blkid

if [[ "$(id -u)" -ne 0 ]]; then
  echo "image/build.sh must run as root (loop mounts + grub-install)." >&2
  echo "Example: sudo ./image/build.sh --dev-smoke" >&2
  exit 1
fi

PACKAGES="$(
  grep -vE '^\s*(#|$)' "$IMAGE_DIR/packages.list" | tr '\n' ',' | sed 's/,$//'
)"

mkdir -p "$OUT_DIR" "$WORK_DIR"
ROOTFS="$WORK_DIR/rootfs"
DISK_RAW="$OUT_DIR/nexusos-node-${SUITE}-${ARCH}.raw"
DISK_QCOW="$OUT_DIR/nexusos-node-${SUITE}-${ARCH}.qcow2"
if [[ "$DEV_SMOKE" -eq 1 ]]; then
  DISK_RAW="$OUT_DIR/nexusos-node-${SUITE}-${ARCH}-devsmoke.raw"
  DISK_QCOW="$OUT_DIR/nexusos-node-${SUITE}-${ARCH}-devsmoke.qcow2"
fi

echo "==> NexusOS node image build"
echo "    suite=$SUITE arch=$ARCH mirror=$MIRROR"
echo "    out=$OUT_DIR dev_smoke=$DEV_SMOKE"

# --- Go binaries -----------------------------------------------------------
if [[ "$SKIP_GO_BUILD" -eq 0 ]]; then
  echo "==> Building coordinator + nexusctl"
  if ! command -v go >/dev/null 2>&1; then
    echo "Go toolchain not found on PATH; install Go 1.22+ or pass --skip-go-build with prebuilt bin/" >&2
    exit 1
  fi
  mkdir -p "$ROOT/bin"
  ( cd "$ROOT/coordination" && go build -o "$ROOT/bin/coordinator" ./cmd/coordinator )
  ( cd "$ROOT/coordination" && go build -o "$ROOT/bin/nexusctl" ./cmd/nexusctl )
fi
[[ -x "$ROOT/bin/coordinator" && -x "$ROOT/bin/nexusctl" ]] || {
  echo "missing $ROOT/bin/coordinator or nexusctl" >&2
  exit 1
}

# --- Rootfs via mmdebstrap -------------------------------------------------
if [[ "$REUSE_ROOTFS" -eq 1 && -d "$ROOTFS/usr/local/bin" ]]; then
  echo "==> Reusing existing rootfs at $ROOTFS"
else
  echo "==> mmdebstrap $SUITE ($PACKAGES)"
  rm -rf "$ROOTFS"
  mkdir -p "$ROOTFS"

  # Include contrib/non-free-firmware only if needed later; keep main for minimal.
  mmdebstrap \
    --variant=minbase \
    --architectures="$ARCH" \
    --include="$PACKAGES" \
    --components=main \
    --skip=cleanup/apt/lists \
    "$SUITE" \
    "$ROOTFS" \
    "$MIRROR"

  echo "==> Applying overlay"
  rsync -a "$IMAGE_DIR/overlay/" "$ROOTFS/"

  echo "==> Installing NexusOS binaries"
  install -m 0755 "$ROOT/bin/coordinator" "$ROOTFS/usr/local/bin/nexusos-coordinator"
  install -m 0755 "$ROOT/bin/nexusctl" "$ROOTFS/usr/local/bin/nexusctl"
  # Keep a copy of the repo unit for operators who prefer the deploy/ wording.
  install -d -m 0755 "$ROOTFS/usr/share/nexusos"
  install -m 0644 "$ROOT/deploy/nexusos-coordinator.service" "$ROOTFS/usr/share/nexusos/nexusos-coordinator.service.example"

  echo "==> customize-rootfs (dev_smoke=$DEV_SMOKE)"
  # Bind-mount essentials for chroot systemctl enable
  mount --bind /dev "$ROOTFS/dev"
  mount --bind /proc "$ROOTFS/proc"
  mount --bind /sys "$ROOTFS/sys"
  cleanup_chroot_mounts() {
    umount -l "$ROOTFS/dev" 2>/dev/null || true
    umount -l "$ROOTFS/proc" 2>/dev/null || true
    umount -l "$ROOTFS/sys" 2>/dev/null || true
  }
  trap cleanup_chroot_mounts EXIT

  "$IMAGE_DIR/hooks/customize-rootfs.sh" "$ROOTFS" "$DEV_SMOKE"

  # Ensure getty + networking basics
  chroot "$ROOTFS" systemctl enable containerd.service
  chroot "$ROOTFS" systemctl enable systemd-networkd.service 2>/dev/null || true
  chroot "$ROOTFS" systemctl enable systemd-resolved.service 2>/dev/null || true

  # Simple DHCP on ens* / eth0 via systemd-networkd
  install -d -m 0755 "$ROOTFS/etc/systemd/network"
  cat > "$ROOTFS/etc/systemd/network/20-dhcp.network" <<'NET'
[Match]
Name=en* eth*

[Network]
DHCP=yes
NET

  # Prefer resolv.conf from systemd-resolved when present
  if [[ -e "$ROOTFS/lib/systemd/systemd-resolved" || -e "$ROOTFS/usr/lib/systemd/systemd-resolved" ]]; then
    ln -sfn /run/systemd/resolve/stub-resolv.conf "$ROOTFS/etc/resolv.conf" || true
  fi

  cleanup_chroot_mounts
  trap - EXIT
fi

# Always refresh binaries into rootfs (cheap, keeps --reuse-rootfs useful)
install -m 0755 "$ROOT/bin/coordinator" "$ROOTFS/usr/local/bin/nexusos-coordinator"
install -m 0755 "$ROOT/bin/nexusctl" "$ROOTFS/usr/local/bin/nexusctl"

if [[ "$ROOTFS_ONLY" -eq 1 ]]; then
  echo "==> --rootfs-only: rootfs at $ROOTFS"
  exit 0
fi

# --- Disk image ------------------------------------------------------------
echo "==> Creating raw disk $DISK_RAW ($DISK_SIZE)"
rm -f "$DISK_RAW" "$DISK_QCOW"
qemu-img create -f raw "$DISK_RAW" "$DISK_SIZE"

# BIOS/MBR single partition for simplest QEMU boot
parted -s "$DISK_RAW" mklabel msdos
parted -s "$DISK_RAW" mkpart primary ext4 1MiB 100%
parted -s "$DISK_RAW" set 1 boot on

LOOP="$(losetup --find --show --partscan "$DISK_RAW")"
PART=""
cleanup_loop() {
  sync || true
  umount -l "$WORK_DIR/mnt" 2>/dev/null || true
  if [[ -n "${PART_LOOP:-}" ]]; then
    losetup -d "$PART_LOOP" 2>/dev/null || true
  fi
  kpartx -d "$LOOP" 2>/dev/null || true
  losetup -d "$LOOP" 2>/dev/null || true
}
trap cleanup_loop EXIT

# Ensure partition device nodes exist (some hosts lack udevadm/partscan).
if command -v partx >/dev/null 2>&1; then
  partx -u "$LOOP" 2>/dev/null || partx -a "$LOOP" 2>/dev/null || true
fi
if command -v kpartx >/dev/null 2>&1; then
  kpartx -av "$LOOP" 2>/dev/null || true
fi
for _ in $(seq 1 50); do
  if [[ -b "${LOOP}p1" ]]; then
    PART="${LOOP}p1"
    break
  fi
  # kpartx mapper path
  base="$(basename "$LOOP")"
  if [[ -b "/dev/mapper/${base}p1" ]]; then
    PART="/dev/mapper/${base}p1"
    break
  fi
  sleep 0.1
done
if [[ -z "$PART" ]]; then
  # Fallback: losetup with partition offset from parted
  start_b="$(parted -ms "$DISK_RAW" unit B print | awk -F: '/^1:/{gsub(/B/,"",$2); print $2; exit}')"
  if [[ -z "$start_b" ]]; then
    echo "partition node missing and could not parse start offset" >&2
    exit 1
  fi
  PART_LOOP="$(losetup --find --show -o "$start_b" "$DISK_RAW")"
  PART="$PART_LOOP"
  echo "==> Using offset losetup $PART (start=${start_b}B)"
fi

echo "==> Formatting $PART"
mkfs.ext4 -F -L nexusos-root "$PART"
ROOT_UUID="$(blkid -s UUID -o value "$PART")"

mkdir -p "$WORK_DIR/mnt"
mount "$PART" "$WORK_DIR/mnt"

echo "==> Copying rootfs to disk (uuid=$ROOT_UUID)"
rsync -aHAX --numeric-ids "$ROOTFS/" "$WORK_DIR/mnt/"
sed -i "s/UUID=CHANGEME/UUID=$ROOT_UUID/" "$WORK_DIR/mnt/etc/fstab"

# GRUB install into the disk MBR from the guest userspace tools
mount --bind /dev "$WORK_DIR/mnt/dev"
mount --bind /proc "$WORK_DIR/mnt/proc"
mount --bind /sys "$WORK_DIR/mnt/sys"

echo "==> grub-install + update-grub"
# Prefer host grub-install against the raw disk file. Many CI/cloud kernels set
# loop max_part=0 so chroot grub-install on a losetup device cannot see partitions.
if command -v grub-install >/dev/null 2>&1; then
  grub-install --target=i386-pc     --boot-directory="$WORK_DIR/mnt/boot"     --modules="biosdisk part_msdos ext2 search search_fs_uuid"     --recheck     "$DISK_RAW"
else
  chroot "$WORK_DIR/mnt" grub-install --target=i386-pc --recheck --boot-directory=/boot "$LOOP"
fi
# Make GRUB see the correct root UUID
cat > "$WORK_DIR/mnt/boot/grub/grub.cfg" <<GRUB
set timeout=1
set default=0
# Keep GRUB on the platform console; Linux takes serial via console=ttyS0.
# Avoid GRUB serial here: it hangs if serial.mod is not in core.img.

menuentry "NexusOS node (${SUITE})" {
  insmod ext2
  search --no-floppy --fs-uuid --set=root $ROOT_UUID
  linux /vmlinuz root=UUID=$ROOT_UUID ro console=tty0 console=ttyS0,115200n8
  initrd /initrd.img
}
GRUB

# Prefer versioned kernel paths if /vmlinuz link is absent
if [[ ! -e "$WORK_DIR/mnt/vmlinuz" ]]; then
  KVER="$(ls -1 "$WORK_DIR/mnt/boot"/vmlinuz-* 2>/dev/null | sed 's|.*/vmlinuz-||' | tail -1 || true)"
  if [[ -n "$KVER" ]]; then
    sed -i "s|linux /vmlinuz|linux /boot/vmlinuz-$KVER|; s|initrd /initrd.img|initrd /boot/initrd.img-$KVER|" \
      "$WORK_DIR/mnt/boot/grub/grub.cfg"
  fi
fi

umount -l "$WORK_DIR/mnt/dev" 2>/dev/null || true
umount -l "$WORK_DIR/mnt/proc" 2>/dev/null || true
umount -l "$WORK_DIR/mnt/sys" 2>/dev/null || true
umount "$WORK_DIR/mnt"
losetup -d "$LOOP"
trap - EXIT

echo "==> Converting to qcow2"
qemu-img convert -c -f raw -O qcow2 "$DISK_RAW" "$DISK_QCOW"

# Manifest for operators / CI
cat > "$OUT_DIR/MANIFEST.txt" <<MAN
suite=$SUITE
arch=$ARCH
mirror=$MIRROR
dev_smoke=$DEV_SMOKE
raw=$(basename "$DISK_RAW")
qcow2=$(basename "$DISK_QCOW")
root_uuid=$ROOT_UUID
built_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
tool=mmdebstrap
MAN

echo "==> Done"
echo "    raw:   $DISK_RAW"
echo "    qcow2: $DISK_QCOW"
echo "    Boot smoke: ./scripts/qemu-node-smoke.sh $DISK_QCOW"
