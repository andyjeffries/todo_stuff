package auth

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/andyjessop/todostuff/internal/models"
)

// HTTP Digest (RFC 7616, MD5, qop=auth) for the CalDAV endpoint. iOS won't
// send a Basic password over plain HTTP, but it will answer a Digest
// challenge, which never puts the password on the wire. MD5 is what Apple's
// client speaks; the secret is a random app password, not the account one.

// DigestRealm is fixed: it's baked into every stored HA1.
const DigestRealm = "TodoStuff"

// digestNonceTTL bounds how long a nonce is accepted. An expired one gets a
// stale=true challenge, which clients answer without asking the user.
const digestNonceTTL = time.Hour

// digestSecret signs nonces, so they need no server-side state. A restart
// makes every nonce stale, which clients retry transparently.
var digestSecret = func() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}()

// digestHA1 is what's stored per app password. The password is normalised
// like hashAppPassword's input, but dashes are kept: Digest hashes exactly
// what the client typed, so we store the form we hand out.
func digestHA1(email, pw string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email)) + ":" + DigestRealm + ":" + strings.ToLower(pw)))
	return hex.EncodeToString(sum[:])
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newNonce is "<unix seconds>.<HMAC of them>".
func newNonce(now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + "." + nonceMAC(ts)
}

func nonceMAC(ts string) string {
	m := hmac.New(sha256.New, digestSecret)
	m.Write([]byte(ts))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// checkNonce reports whether the nonce is ours (valid) and still fresh.
func checkNonce(nonce string, now time.Time) (valid, fresh bool) {
	ts, mac, ok := strings.Cut(nonce, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(nonceMAC(ts))) {
		return false, false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false, false
	}
	return true, now.Sub(time.Unix(n, 0)) < digestNonceTTL
}

// DigestChallenge is the WWW-Authenticate value for a 401.
func DigestChallenge(stale bool) string {
	c := fmt.Sprintf(`Digest realm="%s", qop="auth", algorithm=MD5, nonce="%s"`, DigestRealm, newNonce(time.Now()))
	if stale {
		c += ", stale=true"
	}
	return c
}

// ErrStaleNonce means the credentials may be right but the nonce expired:
// answer with DigestChallenge(true).
var ErrStaleNonce = errors.New("auth: stale digest nonce")

// parseDigest splits `Digest k="v", k=v, …` into its parameters.
func parseDigest(header string) (map[string]string, bool) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Digest") {
		return nil, false
	}
	params := map[string]string{}
	for rest = strings.TrimSpace(rest); rest != ""; {
		k, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		var v string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				return nil, false
			}
			v, rest = after[1:1+end], after[2+end:]
		} else {
			v, rest, _ = strings.Cut(after, ",")
			v = strings.TrimSpace(v)
		}
		params[k] = v
		rest = strings.TrimLeft(strings.TrimSpace(rest), ",")
		rest = strings.TrimSpace(rest)
	}
	return params, true
}

// AuthenticateDigest checks an `Authorization: Digest …` header for method
// and request URI against the user's app passwords. ErrStaleNonce asks the
// caller to re-challenge with stale=true; ErrInvalidCredentials is a plain
// rejection.
func (s *Service) AuthenticateDigest(ctx context.Context, method, requestURI, header string) (*models.User, error) {
	p, ok := parseDigest(header)
	if !ok || p["realm"] != DigestRealm || p["username"] == "" || p["nonce"] == "" || p["response"] == "" {
		return nil, ErrInvalidCredentials
	}
	if a := p["algorithm"]; a != "" && !strings.EqualFold(a, "MD5") {
		return nil, ErrInvalidCredentials
	}
	// The digest covers the URI the client signed; it must be this request's.
	if p["uri"] != requestURI {
		return nil, ErrInvalidCredentials
	}
	valid, fresh := checkNonce(p["nonce"], time.Now())
	if !valid {
		return nil, ErrInvalidCredentials
	}

	email := strings.ToLower(strings.TrimSpace(p["username"]))
	rows, err := s.db.QueryContext(ctx, `
        SELECT ap.id, ap.user_id, ap.digest_ha1, ap.last_used_at
          FROM app_passwords ap
          JOIN users u ON u.id = ap.user_id
         WHERE u.email = ?`, email)
	if err != nil {
		return nil, fmt.Errorf("digest lookup: %w", err)
	}
	type cand struct {
		id, userID, ha1 string
		lastUsed        sql.NullTime
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.userID, &c.ha1, &c.lastUsed); err != nil {
			rows.Close()
			return nil, fmt.Errorf("digest scan: %w", err)
		}
		cands = append(cands, c)
	}
	rows.Close()

	ha2 := md5hex(method + ":" + p["uri"])
	for _, c := range cands {
		var want string
		if p["qop"] == "" {
			want = md5hex(c.ha1 + ":" + p["nonce"] + ":" + ha2)
		} else {
			want = md5hex(c.ha1 + ":" + p["nonce"] + ":" + p["nc"] + ":" + p["cnonce"] + ":" + p["qop"] + ":" + ha2)
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(p["response"]))) == 1 {
			if !fresh {
				return nil, ErrStaleNonce
			}
			s.touchAppPassword(ctx, c.id, c.lastUsed)
			return s.FindUserByID(ctx, c.userID)
		}
	}
	return nil, ErrInvalidCredentials
}
