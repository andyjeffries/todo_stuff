package auth

import (
	"strconv"
	"testing"
	"time"
)

func TestNonce(t *testing.T) {
	now := time.Now()
	if valid, fresh := checkNonce(newNonce(now), now); !valid || !fresh {
		t.Fatal("fresh nonce rejected")
	}
	if valid, fresh := checkNonce(newNonce(now.Add(-2*time.Hour)), now); !valid || fresh {
		t.Fatal("old nonce should be valid but stale")
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	if valid, _ := checkNonce(ts+".forged", now); valid {
		t.Fatal("forged nonce accepted")
	}
}

func TestParseDigest(t *testing.T) {
	p, ok := parseDigest(`Digest username="andy@example.com", realm="TodoStuff", nonce="1.x", uri="/dav/a,b/", response="abc", algorithm=MD5, qop=auth, nc=00000001, cnonce="c"`)
	if !ok {
		t.Fatal("not parsed")
	}
	want := map[string]string{"username": "andy@example.com", "uri": "/dav/a,b/", "qop": "auth", "nc": "00000001", "cnonce": "c", "algorithm": "MD5"}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %q, want %q", k, p[k], v)
		}
	}
}
