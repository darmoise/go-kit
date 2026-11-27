// Package webhook signs and sends outgoing events (HMAC-SHA256).
//
// Signature header: "t=<unix seconds>,v1=<hex(HMAC-SHA256(secret, "<unix>.<body>"))>".
// Package errors do not contain the secret, body, signature or URL.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidSignature means the signature header is malformed or the signature did not match.
	ErrInvalidSignature = errors.New("webhook: invalid signature")
	// ErrTimestampOutOfRange means the signature is valid but the time is out of tolerance.
	ErrTimestampOutOfRange = errors.New("webhook: timestamp outside tolerance")
)

func mac(secret string, unix int64, body []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(unix, 10)))
	m.Write([]byte{'.'})
	m.Write(body)
	return m.Sum(nil)
}

// Sign returns the signature header value for time t and body body.
func Sign(secret string, t time.Time, body []byte) string {
	unix := t.Unix()
	return "t=" + strconv.FormatInt(unix, 10) + ",v1=" + hex.EncodeToString(mac(secret, unix, body))
}

// Verify checks the signature header. Several v1 values are allowed (secret
// rotation), unknown keys are ignored. The signature is checked before the time:
// with a wrong signature it is always ErrInvalidSignature; with a valid signature and
// |now - t| > tolerance — ErrTimestampOutOfRange.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	if secret == "" {
		return ErrInvalidSignature
	}
	var (
		ts     int64
		haveTS bool
		sigs   []string
	)
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			if haveTS {
				return ErrInvalidSignature
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || v[0] < '0' || v[0] > '9' {
				return ErrInvalidSignature
			}
			ts, haveTS = n, true
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if !haveTS || len(sigs) == 0 {
		return ErrInvalidSignature
	}
	want := mac(secret, ts, body)
	matched := false
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err != nil {
			continue // not hex: simply does not match
		}
		if hmac.Equal(got, want) {
			matched = true
		}
	}
	if !matched {
		return ErrInvalidSignature
	}
	diff := now.Sub(time.Unix(ts, 0))
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		return ErrTimestampOutOfRange
	}
	return nil
}

// Send sends a POST with body body and the headers Content-Type, X-Event-Id and
// X-Signature. It returns the HTTP status of any received response (including
// 4xx/5xx) and nil; the retry decision is made by the caller. A transport error
// is returned as (0, err) without the URL. c == nil means a client with a 10 s timeout.
func Send(ctx context.Context, c *http.Client, target, secret, eventID string, body []byte) (status int, err error) {
	if secret == "" {
		return 0, errors.New("webhook: empty secret")
	}
	if eventID == "" {
		return 0, errors.New("webhook: empty event id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("webhook: invalid url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Id", eventID)
	req.Header.Set("X-Signature", Sign(secret, time.Now(), body))
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}
