const SEED = "ChannelFlow QuickPin v1";

function normalizePin(raw) {
  return (raw || "").toUpperCase().replace(/[^A-Z0-9]/g, "");
}

function displayPin(pin) {
  const n = normalizePin(pin);
  if (n.length !== 8) return pin;
  return n.slice(0, 4) + "-" + n.slice(4);
}

function b64encode(bytes) {
  let s = "";
  bytes.forEach((b) => {
    s += String.fromCharCode(b);
  });
  return btoa(s);
}

function b64decode(str) {
  const bin = atob(str);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function stripSlash(url) {
  return String(url || "").replace(/\/+$/, "");
}

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function linkRow(label, url) {
  if (!url) return "";
  const safe = escapeHtml(url);
  return `${escapeHtml(label)}: <a href="${safe}">${safe}</a>`;
}

// Same rule as ChannelFlow-Server QuickPinController: local is primary when
// the pairs differ (pairing on the LAN); public when they are identical.
function derivePrimary(publicUrls, localUrls) {
  const same =
    stripSlash(publicUrls.m3u) === stripSlash(localUrls.m3u) &&
    stripSlash(publicUrls.xmltv) === stripSlash(localUrls.xmltv);
  return same ? publicUrls : localUrls;
}

function renderLinks(links) {
  const primaryM3u = links.m3u || "";
  const primaryXmltv = links.xmltv || "";
  const pubM3u = links.m3uPublic || "";
  const pubXmltv = links.xmltvPublic || "";
  const locM3u = links.m3uLocal || "";
  const locXmltv = links.xmltvLocal || "";
  const hasVariants = pubM3u || pubXmltv || locM3u || locXmltv;
  const same =
    !hasVariants ||
    (stripSlash(pubM3u) === stripSlash(locM3u) && stripSlash(pubXmltv) === stripSlash(locXmltv));

  if (same) {
    return `<strong>Links received</strong><br>${linkRow("M3U", primaryM3u)}<br>${linkRow("XMLTV", primaryXmltv)}`;
  }

  const group = (title, m3u, xmltv) =>
    `<div class="link-group"><span class="link-group-title">${escapeHtml(title)}</span><br>${linkRow("M3U", m3u)}<br>${linkRow("XMLTV", xmltv)}</div>`;

  return `<strong>Links received</strong>${group("Primary", primaryM3u, primaryXmltv)}${group("Public (internet)", pubM3u, pubXmltv)}${group("Local (same network)", locM3u, locXmltv)}`;
}

async function pinKey(pin) {
  const n = normalizePin(pin);
  if (n.length !== 8) throw new Error("PIN must be 8 letters or numbers");
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(SEED + n));
  return crypto.subtle.importKey("raw", hash, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
}

async function encryptLinks(pin, urls) {
  const key = await pinKey(pin);
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const plain = new TextEncoder().encode(JSON.stringify(urls));
  const sealed = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv: nonce }, key, plain));
  const out = new Uint8Array(12 + sealed.length);
  out.set(nonce, 0);
  out.set(sealed, 12);
  return b64encode(out);
}

async function decryptLinks(pin, ciphertext) {
  const key = await pinKey(pin);
  const raw = b64decode(ciphertext);
  if (raw.length < 28) throw new Error("ciphertext too short");
  const nonce = raw.slice(0, 12);
  const sealed = raw.slice(12);
  const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv: nonce }, key, sealed);
  return JSON.parse(new TextDecoder().decode(plain));
}

const appPin = document.getElementById("app-pin");
const appMeta = document.getElementById("app-meta");
const appResult = document.getElementById("app-result");
const btnConnect = document.getElementById("app-connect");
const btnDrop = document.getElementById("app-drop");
const srvPin = document.getElementById("srv-pin");
const srvM3uPublic = document.getElementById("srv-m3u-public");
const srvXmltvPublic = document.getElementById("srv-xmltv-public");
const srvM3uLocal = document.getElementById("srv-m3u-local");
const srvXmltvLocal = document.getElementById("srv-xmltv-local");
const btnSend = document.getElementById("srv-send");
const srvMeta = document.getElementById("srv-meta");
const waitingEl = document.getElementById("waiting");

let ws = null;
let currentPin = "";
let expiresAt = 0;
let tickTimer = null;

function setMeta(el, text, kind) {
  el.textContent = text;
  el.className = "meta" + (kind ? " " + kind : "");
}

function showPin(text, placeholder) {
  if (placeholder) {
    appPin.innerHTML = `<span class="muted">${text}</span>`;
  } else {
    appPin.textContent = text;
  }
}

function stopTick() {
  if (tickTimer) {
    clearInterval(tickTimer);
    tickTimer = null;
  }
}

function startTick() {
  stopTick();
  const render = () => {
    const left = Math.max(0, expiresAt - Date.now() / 1000);
    const m = Math.floor(left / 60);
    const s = Math.floor(left % 60);
    setMeta(appMeta, `Waiting for ChannelFlow-Server · ${m}:${String(s).padStart(2, "0")} left`);
    if (left <= 0) stopTick();
  };
  render();
  tickTimer = setInterval(render, 250);
}

function disconnect() {
  stopTick();
  if (ws) {
    ws.onclose = null;
    ws.close();
    ws = null;
  }
  currentPin = "";
  btnConnect.disabled = false;
  btnDrop.disabled = true;
}

btnConnect.addEventListener("click", () => {
  disconnect();
  appResult.hidden = true;
  showPin("Connecting…", true);
  setMeta(appMeta, "Opening a wait session");
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/v1/wait`);
  btnConnect.disabled = true;
  btnDrop.disabled = false;
  ws.onmessage = async (ev) => {
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      setMeta(appMeta, "Bad message from pin server", "err");
      return;
    }
    if (msg.type === "pin") {
      currentPin = msg.pin;
      expiresAt = msg.expiresAt || Date.now() / 1000 + (msg.expiresIn || 600);
      showPin(msg.display || displayPin(msg.pin));
      startTick();
      return;
    }
    if (msg.type === "expired") {
      stopTick();
      showPin("Expired", true);
      setMeta(appMeta, "That PIN lasted 10 minutes. Get a new one.", "warn");
      disconnect();
      btnConnect.disabled = false;
      return;
    }
    if (msg.type === "payload") {
      stopTick();
      try {
        const links = await decryptLinks(currentPin, msg.ciphertext);
        appResult.hidden = false;
        appResult.innerHTML = renderLinks(links);
        setMeta(appMeta, "Decrypted with the same PIN. The pin server never saw these URLs.", "ok");
        showPin(displayPin(currentPin));
      } catch {
        setMeta(appMeta, "Could not decrypt. The PIN on the server did not match.", "err");
      }
      disconnect();
      btnConnect.disabled = false;
    }
  };
  ws.onerror = () => setMeta(appMeta, "Connection failed", "err");
  ws.onclose = () => {
    if (currentPin) {
      stopTick();
      setMeta(appMeta, "Disconnected. That PIN is gone.", "warn");
      showPin("Not connected", true);
    }
    btnConnect.disabled = false;
    btnDrop.disabled = true;
    ws = null;
    currentPin = "";
  };
});

btnDrop.addEventListener("click", () => {
  disconnect();
  showPin("Not connected", true);
  setMeta(appMeta, "Disconnected. That PIN was dropped.");
});

btnSend.addEventListener("click", async () => {
  const pin = normalizePin(srvPin.value);
  const publicUrls = { m3u: srvM3uPublic.value.trim(), xmltv: srvXmltvPublic.value.trim() };
  const localUrls = { m3u: srvM3uLocal.value.trim(), xmltv: srvXmltvLocal.value.trim() };
  if (pin.length !== 8) {
    setMeta(srvMeta, "Enter the 8-character PIN shown on the app.", "err");
    return;
  }
  if (!publicUrls.m3u || !publicUrls.xmltv || !localUrls.m3u || !localUrls.xmltv) {
    setMeta(srvMeta, "Public and local M3U and XMLTV URLs are all required.", "err");
    return;
  }
  const primary = derivePrimary(publicUrls, localUrls);
  const urls = {
    m3u: primary.m3u,
    xmltv: primary.xmltv,
    m3uPublic: publicUrls.m3u,
    xmltvPublic: publicUrls.xmltv,
    m3uLocal: localUrls.m3u,
    xmltvLocal: localUrls.xmltv,
  };
  btnSend.disabled = true;
  setMeta(srvMeta, "Encrypting…");
  try {
    const ciphertext = await encryptLinks(pin, urls);
    const res = await fetch(`/v1/pins/${encodeURIComponent(pin)}/deliver`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ciphertext }),
    });
    if (res.status === 204) {
      setMeta(srvMeta, "Sent. The waiting app can decrypt with that PIN.", "ok");
    } else if (res.status === 404) {
      setMeta(srvMeta, "No app is waiting for that PIN (unknown or expired).", "err");
    } else {
      setMeta(srvMeta, "Could not deliver.", "err");
    }
  } catch (err) {
    setMeta(srvMeta, err.message || "Encrypt failed", "err");
  } finally {
    btnSend.disabled = false;
  }
});

srvPin.addEventListener("input", () => {
  const n = normalizePin(srvPin.value);
  if (n.length === 8) srvPin.value = displayPin(n);
});

async function refreshWaiting() {
  try {
    const res = await fetch("/v1/status");
    const data = await res.json();
    waitingEl.textContent = "Waiting apps: " + data.waiting;
  } catch {
    waitingEl.textContent = "Waiting apps: —";
  }
}
refreshWaiting();
setInterval(refreshWaiting, 4000);
