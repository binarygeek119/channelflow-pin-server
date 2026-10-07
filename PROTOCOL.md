# ChannelFlow Quick Pin protocol

This is the contract for [ChannelFlow-Server](https://github.com/binarygeek119/ChannelFlow)’s **Quick Pin** tab and the ChannelFlow app. The pin server only routes ciphertext. It never encrypts, decrypts, or logs URLs.

Public origin after GCP setup: `https://channelflow.duckdns.org` (or the DuckDNS name you chose).

## PIN

- 8 characters from `A–Z` and `0–9`
- Random, unique among waiting apps
- Display as `XXXX-XXXX`; the wire value has **no dash**
- Treat input as case-insensitive (normalize to uppercase, strip dashes and spaces)
- Lives **10 minutes** from issue (`expiresIn: 600`), then it is dropped
- Disconnecting the app drops the PIN immediately
- A successful deliver consumes the PIN; it cannot be reused

## Encryption (PIN is the seed)

Both the ChannelFlow app and ChannelFlow-Server derive the same key locally. They never send the key or the plaintext URLs to the pin server.

```
key = SHA256( ASCII("ChannelFlow QuickPin v1") || ASCII(normalized PIN) )
```

- AES-256-GCM
- 12-byte random nonce
- 16-byte GCM tag
- Wire ciphertext (JSON string, standard Base64): `nonce || ciphertext || tag`
- Plaintext UTF-8 JSON:

```json
{
  "m3u": "http://192.168.1.10:8096/iptv/channels.m3u?api_key=…",
  "xmltv": "http://192.168.1.10:8096/iptv/epg.xml?api_key=…",
  "m3uPublic": "https://channelflow.example/iptv/channels.m3u?api_key=…",
  "xmltvPublic": "https://channelflow.example/iptv/epg.xml?api_key=…",
  "m3uLocal": "http://192.168.1.10:8096/iptv/channels.m3u?api_key=…",
  "xmltvLocal": "http://192.168.1.10:8096/iptv/epg.xml?api_key=…"
}
```

Field meaning:

- `m3u` / `xmltv` — **primary** pair the app should try first
- `m3uPublic` / `xmltvPublic` — internet-reachable Live TV URLs
- `m3uLocal` / `xmltvLocal` — same-LAN Live TV URLs

ChannelFlow-Server always sends all six keys. It sets the primary pair to **local** when the admin is pairing from a host other than the configured public URL (same network); otherwise primary is the **public** pair. The variants are included either way so the app can fall back.

Apps should:

1. Connect with `m3u` / `xmltv` first
2. Ignore unknown JSON keys
3. Treat missing variant keys as empty (old servers send only `m3u` and `xmltv`)
4. If the primary pair fails and both variants are present, try the other pair

A wrong PIN fails the auth tag. C# can use `SHA256.HashData` and `AesGcm`.

The HTTP envelope is unchanged: deliver is still `{"ciphertext":"<base64>"}`, and the app still receives `{"type":"payload","ciphertext":"<base64>"}`.

### Compatibility

| Side | Behavior |
| --- | --- |
| New server, old app | Extra keys are ignored; the app uses primary `m3u` / `xmltv` |
| Old server, new app | Only two keys; the app must not require variants |
| New server, new app | All six keys; primary plus public/local fallback |

### C# sketch (server Quick Pin tab)

```csharp
static byte[] Key(string pin)
{
    pin = Regex.Replace(pin.ToUpperInvariant(), "[^A-Z0-9]", "");
    return SHA256.HashData(Encoding.ASCII.GetBytes("ChannelFlow QuickPin v1" + pin));
}

static string Encrypt(string pin, string m3u, string xmltv, string m3uPublic, string xmltvPublic, string m3uLocal, string xmltvLocal)
{
    var plain = JsonSerializer.SerializeToUtf8Bytes(new Dictionary<string, string>
    {
        ["m3u"] = m3u,
        ["xmltv"] = xmltv,
        ["m3uPublic"] = m3uPublic,
        ["xmltvPublic"] = xmltvPublic,
        ["m3uLocal"] = m3uLocal,
        ["xmltvLocal"] = xmltvLocal,
    });
    var nonce = RandomNumberGenerator.GetBytes(12);
    var ct = new byte[plain.Length];
    var tag = new byte[16];
    using var gcm = new AesGcm(Key(pin), 16);
    gcm.Encrypt(nonce, plain, ct, tag);
    var blob = new byte[12 + ct.Length + 16];
    nonce.CopyTo(blob, 0);
    ct.CopyTo(blob, 12);
    tag.CopyTo(blob, 12 + ct.Length);
    return Convert.ToBase64String(blob);
}
```

## App: wait for a PIN

WebSocket (preferred):

```
GET /v1/wait
Upgrade: websocket
```

First server message:

```json
{"type":"pin","pin":"K7M2Q9AB","display":"K7M2-Q9AB","expiresIn":600,"expiresAt":1730000000}
```

Later:

```json
{"type":"payload","ciphertext":"<base64>"}
```

or

```json
{"type":"expired"}
```

Decrypt `ciphertext` with the PIN you were issued.

HTTP fallback:

1. `POST /v1/wait` → same pin object (`pin`, `display`, `expiresIn`, `expiresAt`)
2. `GET /v1/wait?pin=K7M2Q9AB` long-polls (~25s). Responses:
   - `{"type":"payload","ciphertext":"..."}`
   - `{"type":"expired"}`
   - `{"type":"retry"}` (still waiting; call GET again)
   - `404` unknown PIN

## ChannelFlow-Server: Quick Pin tab

1. User types the PIN shown on the app
2. Server encrypts its public and local M3U/XMLTV URLs with that PIN (no key fetch). Primary is local when pairing on the LAN, public otherwise.
3. Deliver:

```
POST /v1/pins/{pin}/deliver
Content-Type: application/json

{"ciphertext":"<base64>"}
```

- `204` — an app was waiting; ciphertext was forwarded
- `404` — unknown, expired, or already used (no extra detail)
- `{pin}` in the path may include a dash (`K7M2-Q9AB`)

Set the pin server origin to `https://channelflow.duckdns.org` (or your subdomain).

## Other

- `GET /health` → `ok`
- `GET /v1/status` → `{"waiting":N}` (count only; no PINs or URLs)
- CORS is open so a browser app can call the API
- The pin server writes no access logs, IPs, PINs, or URLs
