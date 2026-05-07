// Package notifications wires outbound notification channels (today: Pushover).
//
// The Pushover client is intentionally tiny: one POST, one parse, one error.
// We keep the dependency on net/http only — no Pushover SDK — because the
// API surface we use is two fields (token, user) plus title/message.
package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const pushoverAPIURL = "https://api.pushover.net/1/messages.json"

// ErrPushoverDisabled is returned when the client is constructed without an
// app token. Callers should treat this as "feature off" — log and move on,
// not a hard failure. Lets the rest of the app run cleanly without Pushover
// configured (the docker-compose.yml lists it as optional).
var ErrPushoverDisabled = errors.New("pushover: app token not configured")

// ErrInvalidUserKey is returned when Pushover responds with a 4xx that names
// the user field as the problem — i.e. the user pasted a bad key. The
// /profile/pushover/test endpoint surfaces this as a friendly message.
var ErrInvalidUserKey = errors.New("pushover: invalid user key")

// Pushover is a thin client around api.pushover.net. The zero value is not
// usable; construct via NewPushover.
type Pushover struct {
	appToken string
	http     *http.Client
}

// NewPushover returns a client that sends with the given app token. An empty
// token returns a client that is technically valid but whose Send always
// returns ErrPushoverDisabled — callers don't have to nil-check.
func NewPushover(appToken string) *Pushover {
	return &Pushover{
		appToken: strings.TrimSpace(appToken),
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether the client has an app token configured.
func (p *Pushover) Enabled() bool { return p.appToken != "" }

// SendParams are the fields we expose. Pushover supports more (sound,
// priority, attachments) but we only need the basics for reminders.
type SendParams struct {
	UserKey string // user's identity key from their Pushover dashboard
	Title   string
	Message string
	URL     string // optional — link the notification opens when tapped
	URLTitle string // optional — friendly label for URL
}

// pushoverResponse is the JSON shape returned by api.pushover.net.
//
// On success: {"status":1,"request":"<uuid>"}
// On failure: {"status":0,"errors":["user identifier is invalid"], ...}
type pushoverResponse struct {
	Status  int      `json:"status"`
	Errors  []string `json:"errors"`
	Request string   `json:"request"`
}

// Send fires a single Pushover message. Errors:
//   - ErrPushoverDisabled if no app token was configured
//   - ErrInvalidUserKey if Pushover rejects the user key (4xx + status:0)
//   - a wrapped HTTP/transport error for everything else
func (p *Pushover) Send(ctx context.Context, in SendParams) error {
	if !p.Enabled() {
		return ErrPushoverDisabled
	}
	in.UserKey = strings.TrimSpace(in.UserKey)
	if in.UserKey == "" {
		return ErrInvalidUserKey
	}

	form := url.Values{
		"token":   {p.appToken},
		"user":    {in.UserKey},
		"title":   {in.Title},
		"message": {in.Message},
	}
	if in.URL != "" {
		form.Set("url", in.URL)
		if in.URLTitle != "" {
			form.Set("url_title", in.URLTitle)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushoverAPIURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("pushover: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("pushover: post: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var parsed pushoverResponse
	// Body may be empty on 5xx; ignore parse errors and rely on status code.
	_ = json.Unmarshal(body, &parsed)

	if resp.StatusCode == http.StatusOK && parsed.Status == 1 {
		return nil
	}

	// 4xx with status:0 means a user-supplied field was rejected. The most
	// common cause is a bad user key — surface that specifically so the
	// /profile/pushover/test handler can show a "Pushover rejected your
	// user key" message instead of "internal error".
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		for _, e := range parsed.Errors {
			lower := strings.ToLower(e)
			if strings.Contains(lower, "user") || strings.Contains(lower, "identifier") {
				return ErrInvalidUserKey
			}
		}
		return fmt.Errorf("pushover: rejected (%d): %s", resp.StatusCode, strings.Join(parsed.Errors, "; "))
	}

	return fmt.Errorf("pushover: unexpected response (%d): %s", resp.StatusCode, string(body))
}
