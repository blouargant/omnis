package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const negativeTTL = 30 * time.Second

type cacheEntry struct {
	id      Identity
	err     error
	expires time.Time
}

// CachingValidator memoises an inner Validator by token hash: accepted tokens
// until min(JWT exp, now+ttl), rejections for 30 s, unavailability never.
// Concurrent validations of one token share a single inner call.
type CachingValidator struct {
	inner Validator
	ttl   time.Duration
	Now   func() time.Time

	mu sync.Mutex
	m  map[string]cacheEntry
	sf singleflight.Group
}

func NewCachingValidator(inner Validator, ttl time.Duration) *CachingValidator {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &CachingValidator{inner: inner, ttl: ttl, Now: time.Now, m: map[string]cacheEntry{}}
}

func (c *CachingValidator) Validate(ctx context.Context, token string) (Identity, error) {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	now := c.Now()
	c.mu.Lock()
	if e, ok := c.m[key]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		if e.err != nil {
			return Identity{}, e.err
		}
		id := e.id
		id.Token = token
		return id, nil
	}
	c.mu.Unlock()

	v, err, _ := c.sf.Do(key, func() (any, error) {
		id, err := c.inner.Validate(ctx, token)
		now := c.Now()
		switch {
		case err == nil:
			exp := now.Add(c.ttl)
			if jexp, ok := JWTExp(token); ok && jexp.Before(exp) {
				exp = jexp
			}
			c.store(key, cacheEntry{id: Identity{Login: id.Login, Roles: id.Roles}, expires: exp})
		case errors.Is(err, ErrRejected):
			c.store(key, cacheEntry{err: err, expires: now.Add(negativeTTL)})
		}
		return id, err
	})
	if err != nil {
		return Identity{}, err
	}
	id := v.(Identity)
	id.Token = token
	return id, nil
}

func (c *CachingValidator) store(key string, e cacheEntry) {
	c.mu.Lock()
	c.m[key] = e
	now := c.Now()
	if len(c.m) > 4096 { // bound memory: drop expired entries
		for k, v := range c.m {
			if !now.Before(v.expires) {
				delete(c.m, k)
			}
		}
	}
	c.mu.Unlock()
}

// JWTExp decodes (does NOT verify) a JWT's exp claim. Used only to bound the
// cache lifetime — identity always comes from the validator.
func JWTExp(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}
