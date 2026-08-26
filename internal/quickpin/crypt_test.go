package quickpin

import (
	"strings"
	"testing"
)

func TestNormalizeAndDisplay(t *testing.T) {
	n, err := NormalizePin("k7m2-q9ab")
	if err != nil {
		t.Fatal(err)
	}
	if n != "K7M2Q9AB" {
		t.Fatalf("got %s", n)
	}
	if DisplayPin("k7m2q9ab") != "K7M2-Q9AB" {
		t.Fatalf("display %s", DisplayPin("k7m2q9ab"))
	}
	if _, err := NormalizePin("SHORT"); err != ErrPin {
		t.Fatalf("short: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	ct, err := EncryptLinks("K7M2Q9AB", "https://home.example/channels.m3u", "https://home.example/epg.xml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(ct), "m3u") || strings.Contains(strings.ToLower(ct), "home.example") {
		t.Fatal("plaintext leaked into ciphertext")
	}
	links, err := DecryptLinks("k7m2-q9ab", ct)
	if err != nil {
		t.Fatal(err)
	}
	if links.M3U != "https://home.example/channels.m3u" || links.XMLTV != "https://home.example/epg.xml" {
		t.Fatalf("%+v", links)
	}
	if _, err := DecryptLinks("ZZZZZZZZ", ct); err != ErrDecrypt {
		t.Fatalf("wrong pin: %v", err)
	}
}

func TestRandomPin(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := RandomPin()
		if err != nil {
			t.Fatal(err)
		}
		n, err := NormalizePin(p)
		if err != nil || n != p {
			t.Fatalf("pin %q", p)
		}
		seen[p] = true
	}
	if len(seen) < 40 {
		t.Fatalf("not random enough: %d unique", len(seen))
	}
}
