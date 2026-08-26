#!/bin/bash
# Create a GCP project + e2-micro VM, open 80/443, store a DuckDNS token,
# point <subdomain>.duckdns.org at the VM, and boot the ChannelFlow pin server.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=paths.sh
source "${ROOT}/scripts/paths.sh"
ZONE="${GCP_ZONE:-us-central1-a}"
VM_NAME="${GCP_VM_NAME:-channelflow-pin}"
PROJECT_DEFAULT="${GCP_PROJECT:-channelflow-pin}"
SUBDOMAIN_DEFAULT="${DUCKDNS_SUBDOMAIN:-channelflow}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing $1. Install the Google Cloud SDK: https://cloud.google.com/sdk/docs/install" >&2
    exit 1
  fi
}
need gcloud

prompt() {
  local var="$1" msg="$2" def="${3:-}"
  local cur="${!var:-}"
  if [[ -n "${cur}" ]]; then
    return
  fi
  if [[ -n "${def}" ]]; then
    read -r -p "${msg} [${def}]: " cur
    cur="${cur:-$def}"
  else
    read -r -p "${msg}: " cur
  fi
  printf -v "${var}" '%s' "${cur}"
}

prompt_secret() {
  local var="$1" msg="$2"
  local cur="${!var:-}"
  if [[ -n "${cur}" ]]; then
    return
  fi
  read -r -s -p "${msg}: " cur
  echo
  printf -v "${var}" '%s' "${cur}"
}

echo "ChannelFlow pin server — Google Cloud setup"
echo
echo "DuckDNS: create an account at https://www.duckdns.org"
echo "Create subdomain \"${SUBDOMAIN_DEFAULT}\" if it is free, then copy your token."
echo

prompt GCP_PROJECT "GCP project id" "${PROJECT_DEFAULT}"
prompt DUCKDNS_SUBDOMAIN "DuckDNS subdomain (no .duckdns.org)" "${SUBDOMAIN_DEFAULT}"
prompt_secret DUCKDNS_TOKEN "DuckDNS token"
if [[ -z "${DUCKDNS_TOKEN}" ]]; then
  echo "A DuckDNS token is required." >&2
  exit 1
fi
prompt GCP_ZONE "GCP zone" "${ZONE}"
ZONE="${GCP_ZONE}"

echo
echo "Project:   ${GCP_PROJECT}"
echo "Zone:      ${ZONE}"
echo "VM:        ${VM_NAME}"
echo "App dir:   ${APP_DIR}"
echo "Hostname:  ${DUCKDNS_SUBDOMAIN}.duckdns.org"
echo

if ! gcloud projects describe "${GCP_PROJECT}" >/dev/null 2>&1; then
  echo "Creating project ${GCP_PROJECT}…"
  gcloud projects create "${GCP_PROJECT}" --name="ChannelFlow pin"
fi
gcloud config set project "${GCP_PROJECT}" >/dev/null

ACCOUNT="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | head -n1 || true)"
if [[ -z "${ACCOUNT}" ]]; then
  echo "Run: gcloud auth login" >&2
  exit 1
fi

BILLING="$(gcloud billing accounts list --format='value(name)' --filter='open=true' 2>/dev/null | head -n1 || true)"
if [[ -n "${BILLING}" ]]; then
  gcloud billing projects link "${GCP_PROJECT}" --billing-account="${BILLING}" >/dev/null 2>&1 || true
fi

echo "Enabling Compute Engine…"
gcloud services enable compute.googleapis.com --project="${GCP_PROJECT}"

if ! gcloud compute firewall-rules describe channelflow-pin-http --project="${GCP_PROJECT}" >/dev/null 2>&1; then
  echo "Opening tcp:80 and tcp:443 to the internet…"
  gcloud compute firewall-rules create channelflow-pin-http \
    --project="${GCP_PROJECT}" \
    --direction=INGRESS \
    --priority=1000 \
    --network=default \
    --action=ALLOW \
    --rules=tcp:80,tcp:443 \
    --source-ranges=0.0.0.0/0 \
    --target-tags=channelflow-pin \
    --description="ChannelFlow pin server HTTP and HTTPS"
fi

MACHINE=e2-micro
if ! gcloud compute instances describe "${VM_NAME}" --zone="${ZONE}" --project="${GCP_PROJECT}" >/dev/null 2>&1; then
  echo "Creating ${MACHINE} VM ${VM_NAME}…"
  if ! gcloud compute instances create "${VM_NAME}" \
    --project="${GCP_PROJECT}" \
    --zone="${ZONE}" \
    --machine-type="${MACHINE}" \
    --tags=channelflow-pin \
    --image-family=debian-12 \
    --image-project=debian-cloud \
    --boot-disk-size=20GB \
    --boot-disk-type=pd-standard \
    --metadata=duckdns-subdomain="${DUCKDNS_SUBDOMAIN}",duckdns-token="${DUCKDNS_TOKEN}" \
    --metadata-from-file=startup-script="${ROOT}/scripts/gcp-startup.sh"; then
    echo "e2-micro failed; trying e2-small…"
    gcloud compute instances create "${VM_NAME}" \
      --project="${GCP_PROJECT}" \
      --zone="${ZONE}" \
      --machine-type=e2-small \
      --tags=channelflow-pin \
      --image-family=debian-12 \
      --image-project=debian-cloud \
      --boot-disk-size=20GB \
      --boot-disk-type=pd-standard \
      --metadata=duckdns-subdomain="${DUCKDNS_SUBDOMAIN}",duckdns-token="${DUCKDNS_TOKEN}" \
      --metadata-from-file=startup-script="${ROOT}/scripts/gcp-startup.sh"
  fi
else
  echo "VM ${VM_NAME} already exists; updating DuckDNS metadata…"
  gcloud compute instances add-metadata "${VM_NAME}" \
    --project="${GCP_PROJECT}" \
    --zone="${ZONE}" \
    --metadata=duckdns-subdomain="${DUCKDNS_SUBDOMAIN}",duckdns-token="${DUCKDNS_TOKEN}"
fi

echo "Waiting for SSH…"
for i in $(seq 1 36); do
  if gcloud compute ssh "${VM_NAME}" --project="${GCP_PROJECT}" --zone="${ZONE}" --command="true" >/dev/null 2>&1; then
    break
  fi
  sleep 5
  if [[ "${i}" -eq 36 ]]; then
    echo "SSH never became ready." >&2
    exit 1
  fi
done

echo "Waiting for Docker on the VM…"
for i in $(seq 1 36); do
  if gcloud compute ssh "${VM_NAME}" --project="${GCP_PROJECT}" --zone="${ZONE}" --command="command -v docker >/dev/null"; then
    break
  fi
  sleep 5
done

echo "Preparing ${APP_DIR} on the VM…"
gcloud compute ssh "${VM_NAME}" --project="${GCP_PROJECT}" --zone="${ZONE}" --command="
  sudo useradd -m -s /bin/bash ${APP_USER} 2>/dev/null || true
  sudo mkdir -p ${APP_DIR} ${CERT_DIR}
  sudo chown -R \$(whoami):\$(whoami) /home/${APP_USER}/Projects
"
echo "Copying pin server to ${APP_DIR}…"
gcloud compute scp --project="${GCP_PROJECT}" --zone="${ZONE}" --recurse \
  "${ROOT}/cmd" "${ROOT}/internal" "${ROOT}/web" \
  "${ROOT}/go.mod" "${ROOT}/Dockerfile" "${ROOT}/docker-compose.yml" \
  "${ROOT}/.dockerignore" \
  "${VM_NAME}:${APP_DIR}/"
if [[ -f "${ROOT}/go.sum" ]]; then
  gcloud compute scp --project="${GCP_PROJECT}" --zone="${ZONE}" \
    "${ROOT}/go.sum" "${VM_NAME}:${APP_DIR}/"
fi

REMOTE_ENV="$(mktemp)"
umask 077
cat >"${REMOTE_ENV}" <<ENVFILE
DUCKDNS_SUBDOMAIN=${DUCKDNS_SUBDOMAIN}
DUCKDNS_TOKEN=${DUCKDNS_TOKEN}
CERT_DIR=/certs
ENVFILE
gcloud compute scp --project="${GCP_PROJECT}" --zone="${ZONE}" \
  "${REMOTE_ENV}" "${VM_NAME}:${APP_DIR}/.env"
rm -f "${REMOTE_ENV}"

echo "Building and starting the pin server…"
gcloud compute ssh "${VM_NAME}" --project="${GCP_PROJECT}" --zone="${ZONE}" --command="
  sudo chown -R ${APP_USER}:${APP_USER} /home/${APP_USER}/Projects
  sudo chmod 600 ${APP_DIR}/.env
  cd ${APP_DIR}
  sudo docker-compose up -d --build
"

IP="$(gcloud compute instances describe "${VM_NAME}" --project="${GCP_PROJECT}" --zone="${ZONE}" --format='get(networkInterfaces[0].accessConfigs[0].natIP)')"
echo "Updating DuckDNS ${DUCKDNS_SUBDOMAIN}.duckdns.org → ${IP}…"
curl -sf "https://www.duckdns.org/update?domains=${DUCKDNS_SUBDOMAIN}&token=${DUCKDNS_TOKEN}&ip=${IP}" >/dev/null

echo
echo "Pin server URL for ChannelFlow-Server and the app:"
echo "  https://${DUCKDNS_SUBDOMAIN}.duckdns.org"
echo
echo "Let's Encrypt may take a minute the first time. Health check:"
echo "  curl -sS https://${DUCKDNS_SUBDOMAIN}.duckdns.org/health"
