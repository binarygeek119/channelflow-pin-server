#!/bin/bash
# First boot on the ChannelFlow pin VM: user, Projects dir, Docker, optional compose.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

APP_USER=binarygeek119
APP_DIR=/home/binarygeek119/Projects/channelflow-pin
CERT_DIR=/home/binarygeek119/Projects/channelflow-pin/certs

apt-get update -y
apt-get install -y --no-install-recommends \
  ca-certificates curl docker.io docker-compose
systemctl enable --now docker

if ! id -u "${APP_USER}" >/dev/null 2>&1; then
  useradd -m -s /bin/bash "${APP_USER}"
fi
install -d -o "${APP_USER}" -g "${APP_USER}" -m 0755 /home/binarygeek119/Projects "${APP_DIR}"
install -d -o "${APP_USER}" -g "${APP_USER}" -m 0700 "${CERT_DIR}"
usermod -aG docker "${APP_USER}" || true

# Token and subdomain from instance metadata (set by gcp-setup.sh).
META=http://metadata.google.internal/computeMetadata/v1/instance/attributes
hdr=(-H "Metadata-Flavor: Google")
sub="$(curl -sf "${hdr[@]}" "${META}/duckdns-subdomain" || true)"
token="$(curl -sf "${hdr[@]}" "${META}/duckdns-token" || true)"
if [[ -n "${sub}" && -n "${token}" ]]; then
  umask 077
  cat >"${APP_DIR}/.env" <<EOF
DUCKDNS_SUBDOMAIN=${sub}
DUCKDNS_TOKEN=${token}
CERT_DIR=/certs
EOF
  chown "${APP_USER}:${APP_USER}" "${APP_DIR}/.env"
  chmod 600 "${APP_DIR}/.env"
  ip="$(curl -sf -H "Metadata-Flavor: Google" \
    http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip || true)"
  if [[ -n "${ip}" ]]; then
    curl -sf "https://www.duckdns.org/update?domains=${sub}&token=${token}&ip=${ip}" >/dev/null || true
  fi
fi

if [[ -f "${APP_DIR}/docker-compose.yml" ]]; then
  cd "${APP_DIR}"
  docker-compose up -d --build || true
fi
