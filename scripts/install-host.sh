#!/bin/bash
# Copy this source tree onto the HOST at /home/binarygeek119/Projects/channelflow-pin.
# Does not install anything on the Google Cloud VM (/opt/channelflow-pin).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${HOST_PROJECT_DIR:-/home/binarygeek119/Projects/channelflow-pin}"
HOST_USER="${HOST_USER:-binarygeek119}"

if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo --preserve-env=HOST_PROJECT_DIR,HOST_USER "$0" "$@"
fi

if ! id -u "${HOST_USER}" >/dev/null 2>&1; then
  useradd -m -s /bin/bash "${HOST_USER}"
fi

install -d -o "${HOST_USER}" -g "${HOST_USER}" -m 0755 \
  "$(dirname "${DEST}")" "${DEST}"

rsync -a \
  --exclude "/certs/" \
  --exclude "/bin/" \
  --exclude "/pinserver" \
  "${ROOT}/" "${DEST}/"

chown -R "${HOST_USER}:${HOST_USER}" "${DEST}"
echo "Host source: ${DEST}"
