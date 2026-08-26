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
{"m3u":"https://channelflow.example/iptv/channels.m3u","xmltv":"https://channelflow.example/iptv/epg.xml"}
```

A wrong PIN fails the auth tag. C# can use `SHA256.HashData` and `AesGcm`.

### C# sketch (server Quick Pin tab)

```csharp
static byte[] Key(string pin)
{
    pin = Regex.Replace(pin.ToUpperInvariant(), "[^A-Z0-9]", "");
    return SHA256.HashData(Encoding.ASCII.GetBytes("ChannelFlow QuickPin v1" + pin));
}

static string Encrypt(string pin, string m3u, string xmltv)
{
    var plain = JsonSerializer.SerializeToUtf8Bytes(new { m3u, xmltv });
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
2. Server encrypts its current M3U and XMLTV URLs with that PIN (no key fetch)
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
