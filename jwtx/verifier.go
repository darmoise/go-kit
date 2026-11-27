package jwtx

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrInvalidToken means the token failed verification.
	ErrInvalidToken = errors.New("jwtx: invalid token")
	// ErrKeysUnavailable means the signing keys are unavailable (no cache and the JWKS could not be loaded).
	// It does not wrap ErrInvalidToken: callers respond to these cases differently.
	ErrKeysUnavailable = errors.New("jwtx: signing keys unavailable")
)

const maxJWKSBody = 64 << 10

// VerifierOptions are the verifier settings. Values <= 0 and nil are replaced
// with defaults; in particular a zero Leeway cannot be set.
type VerifierOptions struct {
	// JWKSURL is the JWKS address (required for NewVerifier).
	JWKSURL string
	// Issuer is the expected iss (required).
	Issuer string
	// Audience is the expected aud (required); for several aud values one match is enough.
	Audience string
	// AuthorizedParty is the expected azp; empty means "do not check".
	AuthorizedParty string
	// HTTPClient is the client for loading the JWKS (default: 5 s timeout).
	HTTPClient *http.Client
	// RefreshInterval is the key cache lifetime (default 10 min).
	RefreshInterval time.Duration
	// MinRefetchGap is the minimum interval between load attempts (default 30 s).
	MinRefetchGap time.Duration
	// Leeway is the time tolerance for exp/nbf/iat (default 30 s).
	Leeway time.Duration
	// Now is the time source (default time.Now).
	Now func() time.Time
}

// Verifier verifies access JWTs. Safe for concurrent use.
type Verifier struct {
	opts   VerifierOptions
	static bool

	mu          sync.RWMutex
	keys        map[string]ed25519.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time

	fetchMu sync.Mutex
}

func (o VerifierOptions) withDefaults() VerifierOptions {
	if o.RefreshInterval <= 0 {
		o.RefreshInterval = 10 * time.Minute
	}
	if o.MinRefetchGap <= 0 {
		o.MinRefetchGap = 30 * time.Second
	}
	if o.Leeway <= 0 {
		o.Leeway = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	return o
}

func checkRequired(o VerifierOptions) {
	if o.Issuer == "" {
		panic("jwtx: VerifierOptions.Issuer is required")
	}
	if o.Audience == "" {
		panic("jwtx: VerifierOptions.Audience is required")
	}
}

// NewVerifier creates a verifier that loads keys from o.JWKSURL and
// caches them. It panics if Issuer, Audience or JWKSURL is empty.
func NewVerifier(o VerifierOptions) *Verifier {
	checkRequired(o)
	if o.JWKSURL == "" {
		panic("jwtx: VerifierOptions.JWKSURL is required")
	}
	return &Verifier{opts: o.withDefaults(), keys: map[string]ed25519.PublicKey{}}
}

// NewStaticVerifier creates a verifier with a fixed set of keys, without network access.
// JWKSURL is ignored. It panics if Issuer or Audience is empty.
func NewStaticVerifier(o VerifierOptions, keys ...ed25519.PublicKey) *Verifier {
	checkRequired(o)
	m := make(map[string]ed25519.PublicKey, len(keys))
	for _, k := range keys {
		m[KeyID(k)] = k
	}
	return &Verifier{opts: o.withDefaults(), static: true, keys: m}
}

// Verify checks the signature (EdDSA only), iss, aud, exp, iat, nbf, the presence of
// sub and (if set) azp. Errors: ErrKeysUnavailable (keys unavailable) or
// ErrInvalidToken; error text does not contain the token.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrInvalidToken)
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(v.opts.Issuer),
		jwt.WithAudience(v.opts.Audience),
		jwt.WithLeeway(v.opts.Leeway),
		jwt.WithTimeFunc(v.opts.Now),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	claims := &Claims{}
	_, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return v.key(ctx, kid)
	})
	if err != nil {
		if errors.Is(err, ErrKeysUnavailable) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidToken, reason(err))
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: missing sub", ErrInvalidToken)
	}
	if v.opts.AuthorizedParty != "" && claims.AuthorizedParty != v.opts.AuthorizedParty {
		return nil, fmt.Errorf("%w: unexpected azp", ErrInvalidToken)
	}
	return claims, nil
}

// reason returns a short reason; jwt messages do not contain the token itself.
func reason(err error) string {
	switch {
	case errors.Is(err, jwt.ErrTokenMalformed):
		return "malformed"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return "bad signature"
	case errors.Is(err, jwt.ErrTokenExpired):
		return "expired"
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return "not valid yet"
	case errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return "issued in the future"
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return "unexpected iss"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "unexpected aud"
	case errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return "required claim missing"
	case errors.Is(err, jwt.ErrTokenUnverifiable):
		return "unknown or missing key"
	default:
		return "verification failed"
	}
}

// key returns the key by kid, taking the cache and load restrictions into account.
func (v *Verifier) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	if v.static {
		if k, ok := v.lookup(kid); ok {
			return k, nil
		}
		return nil, errUnknownKey
	}
	now := v.opts.Now()
	if k, ok, fresh := v.cached(kid, now); ok && fresh {
		return k, nil
	}

	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()
	// Another call may have already refreshed the cache.
	now = v.opts.Now()
	if k, ok, fresh := v.cached(kid, now); ok && fresh {
		return k, nil
	}
	v.mu.RLock()
	last := v.lastAttempt
	v.mu.RUnlock()
	var fetchErr error
	if last.IsZero() || now.Sub(last) >= v.opts.MinRefetchGap {
		fetchErr = v.fetch(ctx, now)
	}
	// Decision based on the current cache (after a successful load it is already refreshed).
	v.mu.RLock()
	k, ok := v.keys[kid]
	empty := len(v.keys) == 0
	v.mu.RUnlock()
	switch {
	case ok:
		return k, nil
	case empty:
		if fetchErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrKeysUnavailable, fetchErr)
		}
		return nil, fmt.Errorf("%w: no keys loaded", ErrKeysUnavailable)
	default:
		return nil, errUnknownKey
	}
}

var errUnknownKey = errors.New("unknown kid")

func (v *Verifier) lookup(kid string) (ed25519.PublicKey, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	k, ok := v.keys[kid]
	return k, ok
}

// cached returns the key, whether it was found and whether the cache is fresh.
func (v *Verifier) cached(kid string, now time.Time) (key ed25519.PublicKey, ok, fresh bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	key, ok = v.keys[kid]
	fresh = !v.fetchedAt.IsZero() && now.Sub(v.fetchedAt) < v.opts.RefreshInterval
	return key, ok, fresh
}

// fetch loads the JWKS. Called under fetchMu. An error with a cancelled ctx does not
// update lastAttempt, so that cancellation of one caller does not block the others.
func (v *Verifier) fetch(ctx context.Context, now time.Time) error {
	keys, err := v.load(ctx)
	v.mu.Lock()
	defer v.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			v.lastAttempt = now
		}
		return err
	}
	v.lastAttempt = now
	v.fetchedAt = now
	v.keys = keys
	return nil
}

func (v *Verifier) load(ctx context.Context) (map[string]ed25519.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.opts.JWKSURL, nil)
	if err != nil {
		return nil, errors.New("invalid jwks url")
	}
	resp, err := v.opts.HTTPClient.Do(req)
	if err != nil {
		// The client error contains the URL; it is not secret, but the network details are sufficient.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("jwks request failed: %v", ue.Err)
		}
		return nil, errors.New("jwks request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBody))
	if err != nil {
		return nil, errors.New("jwks read failed")
	}
	keys, err := parseJWKS(data)
	if err != nil {
		return nil, errors.New("jwks is not valid json")
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks has no valid keys")
	}
	return keys, nil
}
