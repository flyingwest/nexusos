#!/usr/bin/env bash
# Runs against an unpacked rootfs directory ($1) on the build host (not necessarily chrooted).
set -euo pipefail
ROOTFS="${1:?rootfs path}"
DEV_SMOKE="${2:-0}"

install -d -m 0755 "$ROOTFS/etc/nexusos" "$ROOTFS/var/lib/nexusos" "$ROOTFS/usr/local/bin" \
  "$ROOTFS/usr/local/sbin" "$ROOTFS/usr/local/lib/nexusos"

# Enable serial getty for QEMU -serial stdio
if [[ -d "$ROOTFS/etc/systemd/system" ]]; then
  chroot "$ROOTFS" systemctl enable containerd.service || true
  chroot "$ROOTFS" systemctl enable nexusos-ready.service || true
  chroot "$ROOTFS" systemctl enable ssh.service || true
  chroot "$ROOTFS" systemctl enable serial-getty@ttyS0.service || true
  if [[ "$DEV_SMOKE" == "1" ]]; then
    # Smoke: auto-start coordinator with --dev (see unit override below).
    chroot "$ROOTFS" systemctl enable nexusos-coordinator.service || true
    chroot "$ROOTFS" systemctl disable nexusos-firstboot.service 2>/dev/null || true
  else
    # Production: do NOT enable coordinator until provisioned.
    # firstboot prints serial instructions when tokens/TLS are missing.
    chroot "$ROOTFS" systemctl disable nexusos-coordinator.service 2>/dev/null || true
    chroot "$ROOTFS" systemctl enable nexusos-firstboot.service || true
  fi
fi

# Permit root login on serial for recovery (password set below only for smoke).
if [[ -f "$ROOTFS/etc/ssh/sshd_config" ]]; then
  sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin prohibit-password/' "$ROOTFS/etc/ssh/sshd_config" || true
fi

# fstab placeholder filled by build.sh with the real UUID
if [[ ! -f "$ROOTFS/etc/fstab" ]] || ! grep -q 'NEXUSOS_ROOT' "$ROOTFS/etc/fstab" 2>/dev/null; then
  cat > "$ROOTFS/etc/fstab" <<'FSTAB'
# NEXUSOS_ROOT — rewritten by image/build.sh with the root filesystem UUID
UUID=CHANGEME / ext4 rw,relatime,errors=remount-ro 0 1
FSTAB
fi

if [[ "$DEV_SMOKE" == "1" ]]; then
  cat > "$ROOTFS/etc/nexusos/coordinator.env" <<'ENV'
NEXUS_API_TOKEN=smoke-api-token
NEXUS_JOIN_TOKEN=smoke-join-token
NEXUS_ADVERTISE=https://10.0.2.15:8080
ENV
  chmod 0600 "$ROOTFS/etc/nexusos/coordinator.env"
  # Override unit for --dev local QEMU only (no TLS file Conditions; --dev auto-TLS).
  cat > "$ROOTFS/etc/systemd/system/nexusos-coordinator.service" <<'UNIT'
[Unit]
Description=NexusOS Coordination Service (DEV SMOKE)
Documentation=https://github.com/flyingwest/nexusos-phase2
After=network-online.target containerd.service
Wants=network-online.target
Requires=containerd.service

[Service]
Type=simple
User=root
EnvironmentFile=-/etc/nexusos/coordinator.env
ExecStart=/usr/local/bin/nexusos-coordinator --dev --mock=false --listen :8080 --data-dir /var/lib/nexusos --api-token ${NEXUS_API_TOKEN} --join-token ${NEXUS_JOIN_TOKEN} --advertise ${NEXUS_ADVERTISE}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
  # trivial root password for serial console recovery in smoke images only
  chroot "$ROOTFS" bash -c 'echo "root:nexusos" | chpasswd' || true
  echo "nexusos-node-smoke" > "$ROOTFS/etc/hostname"
fi

# Regenerate SSH host keys on first boot
rm -f "$ROOTFS"/etc/ssh/ssh_host_* 2>/dev/null || true
if [[ -d "$ROOTFS/etc/systemd/system" ]]; then
  chroot "$ROOTFS" systemctl enable ssh.service 2>/dev/null || true
fi

# machine-id empty so first boot regenerates
: > "$ROOTFS/etc/machine-id"
rm -f "$ROOTFS/var/lib/dbus/machine-id"
ln -sf /etc/machine-id "$ROOTFS/var/lib/dbus/machine-id" 2>/dev/null || true
