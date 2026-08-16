#!/usr/bin/env bash
#
# Boots the CHR disk image under QEMU and waits for RouterOS to come up.
#
# Runs the guest on a copy-on-write overlay so a container restart always starts
# from a clean device, and prints the RouterOS login banner to stdout so
# `docker logs` can be used as a readiness signal (the same one CI greps for).

set -euo pipefail

MEMORY="${CHR_MEMORY:-512}"
SMP="${CHR_SMP:-2}"
INTERFACES="${CHR_INTERFACES:-4}"
BOOT_TIMEOUT="${CHR_BOOT_TIMEOUT:-600}"

BASE_IMAGE=/routeros/chr.img
OVERLAY=/tmp/chr-overlay.qcow2
CONSOLE=/tmp/chr-console.log

log() { echo "[chr] $*"; }

# Fresh overlay per boot: the base image stays pristine, so a restarted
# container is a factory-reset router rather than whatever the last test left.
qemu-img create -q -f qcow2 -F raw -b "$BASE_IMAGE" "$OVERLAY"

# KVM where the host offers it; plain emulation otherwise. Emulation is slow to
# boot but fine for the API-level operations the acceptance tests perform.
accel_args=()
if [[ -w /dev/kvm ]]; then
  log "using KVM acceleration"
  accel_args=(-enable-kvm -cpu host)
else
  log "no /dev/kvm, falling back to emulation (slower boot)"
  accel_args=(-cpu qemu64)
fi

# ether1 carries the forwarded management ports; the rest exist so tests that
# need spare interfaces to enslave (bonding, bridge ports) have them.
net_args=(
  -netdev "user,id=n0,hostfwd=tcp::8728-:8728,hostfwd=tcp::8729-:8729,hostfwd=tcp::443-:443"
  -device "virtio-net-pci,netdev=n0"
)
for ((i = 1; i < INTERFACES; i++)); do
  net_args+=(-netdev "user,id=n$i,restrict=on" -device "virtio-net-pci,netdev=n$i")
done

log "booting RouterOS (memory=${MEMORY}M smp=${SMP} interfaces=${INTERFACES})"
qemu-system-x86_64 \
  "${accel_args[@]}" \
  -m "$MEMORY" \
  -smp "$SMP" \
  -drive "file=$OVERLAY,format=qcow2,if=virtio" \
  "${net_args[@]}" \
  -display none \
  -serial "file:$CONSOLE" \
  -pidfile /tmp/chr.pid \
  -daemonize

shutdown_guest() {
  log "stopping RouterOS"
  [[ -f /tmp/chr.pid ]] && kill "$(cat /tmp/chr.pid)" 2>/dev/null || true
}
trap shutdown_guest TERM INT

# QEMU's user-mode hostfwd accepts connections whether or not the guest is
# listening, so a port probe proves nothing. Wait for the login banner instead.
waited=0
while ((waited < BOOT_TIMEOUT)); do
  if [[ -f /tmp/chr.pid ]] && ! kill -0 "$(cat /tmp/chr.pid)" 2>/dev/null; then
    log "QEMU exited during boot"
    cat "$CONSOLE" || true
    exit 1
  fi
  if grep -q 'Login:' "$CONSOLE" 2>/dev/null; then
    log "$(grep -ao 'MikroTik [0-9.]*' "$CONSOLE" | head -1) is up after ${waited}s"
    break
  fi
  sleep 5
  waited=$((waited + 5))
done

if ((waited >= BOOT_TIMEOUT)); then
  log "RouterOS did not boot within ${BOOT_TIMEOUT}s"
  cat "$CONSOLE" || true
  exit 1
fi

# Stream the guest console so `docker logs` shows the banner CI waits on.
tail -f "$CONSOLE" &
wait $!
