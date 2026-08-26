# ChannelFlow pin server

Small always-on pin server for a Google Cloud VM. ChannelFlow apps connect and receive a random **8-character quick PIN**. The user types that PIN on ChannelFlow-Server’s **Quick Pin** tab. The server encrypts its M3U and XMLTV URLs with the PIN as the seed; this process forwards the ciphertext to the waiting app and never opens it.

[ChannelFlow-Server](https://github.com/binarygeek119/ChannelFlow) stays on your home machine. Live TV does not go through this VM.

Protocol for the C# Quick Pin tab and the WIP client: [PROTOCOL.md](PROTOCOL.md).

## Local preview

Needs Go 1.22+.

```bash
RELAY_DEV=1 PORT=43123 go run ./cmd/pinserver
```

Open http://127.0.0.1:43123

- **App** — get a PIN (stands in for the ChannelFlow client)
- **Quick Pin** — paste your M3U and XMLTV URLs, type the PIN, send (stands in for ChannelFlow-Server)

Docker:

```bash
cp .env.example .env
# for local HTTP only:
# RELAY_DEV=1 PORT=43123
docker compose up --build
```

## Production (DuckDNS + TLS)

On the VM, with ports **80** and **443** open:

```bash
export DUCKDNS_SUBDOMAIN=channelflow
export DUCKDNS_TOKEN=your-token-from-duckdns.org
# RELAY_DEV must be unset
./pinserver
```

The process updates `channelflow.duckdns.org` every 5 minutes and gets a Let’s Encrypt certificate for that name.

## Google Cloud

You need:

- [gcloud CLI](https://cloud.google.com/sdk/docs/install) and `gcloud auth login`
- A Google Cloud billing account
- A [DuckDNS](https://www.duckdns.org) account with subdomain `channelflow` (or another name if that one is taken) and its token

From this repo:

```bash
chmod +x scripts/gcp-setup.sh
./scripts/gcp-setup.sh
```

The script creates a GCP project, an e2-micro VM, firewall rules for 80/443, stores the DuckDNS token on the VM, points `channelflow.duckdns.org` at the VM, and starts the pin server. ChannelFlow-Server and the app should use:

`https://channelflow.duckdns.org`

## Environment

| Variable | Purpose |
| --- | --- |
| `RELAY_DEV` | `1` for local HTTP (no TLS, no DuckDNS) |
| `PORT` | Listen port in dev (default `43123`) |
| `BIND` | Listen address in dev (default `0.0.0.0`) |
| `DUCKDNS_SUBDOMAIN` | DuckDNS name without `.duckdns.org` |
| `DUCKDNS_TOKEN` | DuckDNS token |
| `CERT_DIR` | Let’s Encrypt cache (default `/var/lib/channelflow-pin/certs`) |
| `ACME_EMAIL` | Optional contact for Let’s Encrypt |

No request logs, IPs, PINs, or URLs are written. Fatal errors only go to stderr.
