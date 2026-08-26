#!/bin/bash
# First boot on the ChannelFlow pin VM: Docker, data dirs, optional compose.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  ca-certificates curl docker.io docker-compose
systemctl enable --now docker

install -d -m 0755 /opt/channelflow-pin
install -d -m 0700 /var/lib/channelflow-pin/certs

# Token and subdomain from instance metadata (set by gcp-setup.sh).
META=http://metadata.google.internal/computeMetadata/v1/instance/attributes
hdr=(-H "Metadata-Flavor: Google")
sub="$(curl -sf "${hdr[@]}" "${META}/duckdns-subdomain" || true)"
token="$(curl -sf "${hdr[@]}" "${META}/duckdns-token" || true)"
if [[ -n "${sub}" && -n "${token}" ]]; then
  umask 077
  cat >/opt/channelflow-pin/.env <<EOF
DUCKDNS_SUBDOMAIN=${sub}
DUCKDNS_TOKEN=${token}
CERT_DIR=/var/lib/channelflow-pin/certs
EOF
  ip="$(curl -sf -H "Metadata-Flavor: Google" \
    http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip || true)"
  if [[ -n "${ip}" ]]; then
    curl -sf "https://www.duckdns.org/update?domains=${sub}&token=${token}&ip=${ip}" >/dev/null || true
  fi
fi

# If gcp-setup.sh already copied the app, start it.
if [[ -f /opt/channelflow-pin/docker-compose.yml ]]; then
  cd /opt/channelflow-pin
  docker-compose up -d --build || true
fi
