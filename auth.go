package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "ajuma_session"
	sessionPath       = "/admin"
	sessionTTL        = 8 * time.Hour
	pbkdf2Iterations  = 600_000 // OWASP's 2023 figure for PBKDF2-HMAC-SHA256
	pbkdf2KeyLen      = 32
	loginBurst        = 5
	loginLockout      = time.Minute
)

// Credential is a verifier for the admin password. Only the derived key is
// ever held, so the password itself is not sitting in memory or in a file.
// The encoded form — pbkdf2-sha256$iterations$salt$key — is what goes into
// the environment of a deployed instance, so no plaintext travels with it.
type Credential struct {
	iterations int
	salt       []byte
	key        []byte
}

// NewCredential derives a fresh verifier for a password.
func NewCredential(password string) (Credential, error) {
	if len(strings.TrimSpace(password)) < 8 {
		return Credential{}, errors.New("choose a password of at least 8 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Credential{}, fmt.Errorf("read random salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return Credential{}, fmt.Errorf("derive key: %w", err)
	}
	return Credential{iterations: pbkdf2Iterations, salt: salt, key: key}, nil
}

// ParseCredential reads the encoded form produced by String.
func ParseCredential(encoded string) (Credential, error) {
	parts := strings.Split(strings.TrimSpace(encoded), "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return Credential{}, errors.New("password hash is not in pbkdf2-sha256$iterations$salt$key form")
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1000 {
		return Credential{}, errors.New("password hash has an implausible iteration count")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return Credential{}, errors.New("password hash has an unreadable salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return Credential{}, errors.New("password hash has an unreadable key")
	}
	return Credential{iterations: iterations, salt: salt, key: key}, nil
}

// String renders the credential for storage in an environment variable.
func (c Credential) String() string {
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", c.iterations,
		base64.RawStdEncoding.EncodeToString(c.salt),
		base64.RawStdEncoding.EncodeToString(c.key))
}

// Verify checks a submitted password in constant time.
func (c Credential) Verify(password string) bool {
	if len(c.key) == 0 {
		return false
	}
	candidate, err := pbkdf2.Key(sha256.New, password, c.salt, c.iterations, len(c.key))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(candidate, c.key) == 1
}

// session is one signed-in admin. The CSRF token is bound to the session so
// a form posted from anywhere else fails even if the cookie rides along.
type session struct {
	csrf    string
	flashes []Flash
	expires time.Time
}

// Sessions holds signed-in admins in memory. Restarting the server signs the
// owner out, which is the right trade for a single-operator shop: there is no
// session secret on disk to leak.
type Sessions struct {
	mu    sync.Mutex
	items map[string]*session
}

// NewSessions builds an empty session table.
func NewSessions() *Sessions { return &Sessions{items: map[string]*session{}} }

// Start opens a session and returns its cookie token.
func (s *Sessions) Start() (token, csrf string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	token, csrf = randomToken(), randomToken()
	s.items[token] = &session{csrf: csrf, expires: time.Now().Add(sessionTTL)}
	return token, csrf
}

// Lookup returns the CSRF token for a live session, extending its life.
func (s *Sessions) Lookup(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[token]
	if !ok {
		return "", false
	}
	if time.Now().After(item.expires) {
		delete(s.items, token)
		return "", false
	}
	item.expires = time.Now().Add(sessionTTL)
	return item.csrf, true
}

// End signs a session out.
func (s *Sessions) End(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, token)
}

func (s *Sessions) sweepLocked() {
	now := time.Now()
	for token, item := range s.items {
		if now.After(item.expires) {
			delete(s.items, token)
		}
	}
}

// Throttle slows password guessing to a crawl: a handful of tries per client
// address, then a lockout that doubles each time it is hit.
type Throttle struct {
	mu    sync.Mutex
	items map[string]*attempt
}

type attempt struct {
	failures int
	until    time.Time
	seen     time.Time
}

// NewThrottle builds an empty throttle.
func NewThrottle() *Throttle { return &Throttle{items: map[string]*attempt{}} }

// Blocked reports whether a client must wait, and for how long.
func (t *Throttle) Blocked(key string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	item, ok := t.items[key]
	if !ok {
		return 0, false
	}
	if wait := time.Until(item.until); wait > 0 {
		return wait.Round(time.Second), true
	}
	return 0, false
}

// Failed records a wrong password.
func (t *Throttle) Failed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked()
	item, ok := t.items[key]
	if !ok {
		item = &attempt{}
		t.items[key] = item
	}
	item.failures++
	item.seen = time.Now()
	if item.failures >= loginBurst {
		backoff := loginLockout << min(item.failures-loginBurst, 5)
		item.until = time.Now().Add(backoff)
	}
}

// Passed clears the record after a correct password.
func (t *Throttle) Passed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.items, key)
}

func (t *Throttle) sweepLocked() {
	cutoff := time.Now().Add(-time.Hour)
	for key, item := range t.items {
		if item.seen.Before(cutoff) && time.Now().After(item.until) {
			delete(t.items, key)
		}
	}
}

// randomToken returns 32 bytes of randomness, URL-safe.
func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// GeneratePassword invents a readable password for a first run, from an
// alphabet with no characters that can be misread aloud or in a terminal.
func GeneratePassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	buf := make([]byte, 15)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	var b strings.Builder
	for i, v := range buf {
		if i > 0 && i%5 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return b.String()
}

// clientKey identifies the caller for throttling. A proxy header is only
// trusted when the operator has said the server sits behind one.
func clientKey(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if first, _, ok := strings.Cut(fwd, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(fwd)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isSecureRequest reports whether the browser reached us over TLS, so the
// session cookie can be marked Secure without breaking a plain-HTTP dev run.
func isSecureRequest(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	return trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
