// Package quickpin implements PIN-seeded AES-256-GCM for ChannelFlow M3U/XMLTV URLs.
// The pin server never calls these functions; they exist so the wire format is
// documented in tests and matches PROTOCOL.md.
package quickpin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

const (
	seedPrefix = "ChannelFlow QuickPin v1"
	nonceSize  = 12
	pinLen     = 8
	alphabet   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var (
	ErrPin        = errors.New("invalid pin")
	ErrCiphertext = errors.New("invalid ciphertext")
	ErrDecrypt    = errors.New("decrypt failed")
)

// Links is the plaintext JSON the ChannelFlow server encrypts.
type Links struct {
	M3U    string `json:"m3u"`
	XMLTV  string `json:"xmltv"`
}

// NormalizePin uppercases A–Z / 0–9 and strips dashes, spaces, and other junk.
func NormalizePin(pin string) (string, error) {
	var b strings.Builder
	b.Grow(pinLen)
	for _, r := range pin {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(unicode.ToUpper(r))
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) != pinLen {
		return "", ErrPin
	}
	return out, nil
}

// DisplayPin formats an 8-character pin as XXXX-XXXX.
func DisplayPin(pin string) string {
	n, err := NormalizePin(pin)
	if err != nil {
		return pin
	}
	return n[:4] + "-" + n[4:]
}

func deriveKey(pin string) []byte {
	sum := sha256.Sum256([]byte(seedPrefix + pin))
	return sum[:]
}

// EncryptLinks encrypts m3u/xmltv URLs using the pin as seed.
// Wire format (base64): 12-byte nonce || ciphertext || 16-byte GCM tag.
func EncryptLinks(pin, m3u, xmltv string) (string, error) {
	pin, err := NormalizePin(pin)
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(Links{M3U: m3u, XMLTV: xmltv})
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(pin))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	out := make([]byte, 0, nonceSize+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// DecryptLinks opens ciphertext produced by EncryptLinks.
func DecryptLinks(pin, ciphertext string) (Links, error) {
	var zero Links
	pin, err := NormalizePin(pin)
	if err != nil {
		return zero, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil || len(raw) < nonceSize+16 {
		return zero, ErrCiphertext
	}
	block, err := aes.NewCipher(deriveKey(pin))
	if err != nil {
		return zero, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return zero, err
	}
	plain, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return zero, ErrDecrypt
	}
	var links Links
	if err := json.Unmarshal(plain, &links); err != nil {
		return zero, ErrCiphertext
	}
	return links, nil
}

// RandomPin returns an 8-character pin from A–Z and 0–9.
func RandomPin() (string, error) {
	const n = len(alphabet)
	out := make([]byte, pinLen)
	for i := 0; i < pinLen; {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		// alphabet is 36 chars; reject the 4 leftover values in 0–255
		if int(b[0]) >= n*(256/n) {
			continue
		}
		out[i] = alphabet[int(b[0])%n]
		i++
	}
	return string(out), nil
}
