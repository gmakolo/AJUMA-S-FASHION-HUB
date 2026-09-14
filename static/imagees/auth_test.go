package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCredentialRoundTrip runs the real hashing path, at its real cost: this
// is the one place the 600,000-iteration figure is exercised.
func TestCredentialRoundTrip(t *testing.T) {
	const password = "a studio password"
	c, err := NewCredential(password)
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	if !c.Verify(password) {
		t.Error("the right password was refused")
	}
	for _, wrong := range []string{"", "a studio Password", "a studio password ", "nonsense"} {
		if c.Verify(wrong) {
			t.Errorf("%q was accepted as the password", wrong)
		}
	}

	// The encoded form is what travels in a deployment's environment, so it
	// must carry no plaintext and must read back as the same verifier.
	encoded := c.String()
	if strings.Contains(encoded, password) {
		t.Error("the encoded hash contains the password itself")
	}
	if !strings.HasPrefix(encoded, "pbkdf2-sha256$600000$") {
		t.Errorf("encoded hash = %q, want the scheme and work factor first", encoded)
	}
	parsed, err := ParseCredential(encoded)
	if err != nil {
		t.Fatalf("ParseCredential: %v", err)
	}
	if !parsed.Verify(password) || parsed.Verify("nonsense") {
		t.Error("the parsed credential does not behave like the original")
	}
	if parsed.String() != encoded {
		t.Error("encoding is not stable across a round trip")
	}
}

func TestNewCredentialRefusesAWeakPassword(t *testing.T) {
	for _, password := range []string{"", "       ", "short", "  seven  "} {
		if _, err := NewCredential(password); err == nil {
			t.Errorf("NewCredential(%q) was allowed", password)
		}
	}
	// A zero credential must never let anyone in, empty password included.
	var none Credential
	if none.Verify("") || none.Verify("anything") {
		t.Error("an unset credential accepted a password")
	}
}

func TestParseCredentialRejectsRubbish(t *testing.T) {
	tests := map[string]string{
		"empty":              "",
		"not encoded at all": "hunter2",
		"another scheme":     "md5$1000$c2FsdA$a2V5",
		"too few parts":      "pbkdf2-sha256$600000$c2FsdA",
		"iterations absent":  "pbkdf2-sha256$$c2FsdA$a2V5",
		"iterations too low": "pbkdf2-sha256$999$c2FsdA$a2V5",
		"unreadable salt":    "pbkdf2-sha256$600000$not base64$a2V5",
		"unreadable key":     "pbkdf2-sha256$600000$c2FsdA$not base64",
	}
	for name, encoded := range tests {
		if _, err := ParseCredential(encoded); err == nil {
			t.Errorf("%s: ParseCredential(%q) was accepted", name, encoded)
		}
	}
}

func TestSessionsLifecycle(t *testing.T) {
	s := NewSessions()

	token, csrf := s.Start()
	if token == "" || csrf == "" || token == csrf {
		t.Fatalf("Start gave token %q and csrf %q", token, csrf)
	}
	if got, ok := s.Lookup(token); !ok || got != csrf {
		t.Errorf("Lookup returned %q, %v; want the session's own csrf", got, ok)
	}

	other, otherCSRF := s.Start()
	if other == token || otherCSRF == csrf {
		t.Error("two sessions were given the same token")
	}

	for _, unknown := range []string{"", "not-a-token", token + "x"} {
		if _, ok := s.Lookup(unknown); ok {
			t.Errorf("Lookup(%q) found a session", unknown)
		}
	}

	s.End(token)
	if _, ok := s.Lookup(token); ok {
		t.Error("a session survived being ended")
	}
	if _, ok := s.Lookup(other); !ok {
		t.Error("ending one session ended another")
	}
}

func TestSessionFlashes(t *testing.T) {
	s := NewSessions()
	token, _ := s.Start()

	if got := s.TakeFlashes(token); got != nil {
		t.Errorf("a new session already has flashes: %v", got)
	}
	s.Flash(token, "ok", "Adaeze Wrap Dress is live.")
	s.Flash(token, "warn", "It has no photograph yet.")
	s.Flash(token, "ok", "") // empty messages are not queued
	s.Flash("not-a-token", "ok", "dropped on the floor")

	got := s.TakeFlashes(token)
	if len(got) != 2 || got[0].Kind != "ok" || got[1].Kind != "warn" {
		t.Fatalf("flashes came back as %+v", got)
	}
	if !strings.Contains(got[0].Text, "Adaeze") {
		t.Errorf("first flash = %q", got[0].Text)
	}
	if left := s.TakeFlashes(token); left != nil {
		t.Errorf("flashes survived being taken: %v", left)
	}

	// A page that never renders cannot pile up messages without limit.
	for i := 0; i < 20; i++ {
		s.Flash(token, "ok", "another")
	}
	if n := len(s.TakeFlashes(token)); n != 8 {
		t.Errorf("the queue held %d flashes, want it capped at 8", n)
	}
}

func TestThrottleLocksOutAfterABurst(t *testing.T) {
	th := NewThrottle()
	const key = "203.0.113.9"

	if _, blocked := th.Blocked(key); blocked {
		t.Fatal("a client that has never failed is blocked")
	}
	for i := 1; i < loginBurst; i++ {
		th.Failed(key)
		if _, blocked := th.Blocked(key); blocked {
			t.Fatalf("blocked after only %d wrong passwords, want %d", i, loginBurst)
		}
	}

	th.Failed(key)
	wait, blocked := th.Blocked(key)
	if !blocked {
		t.Fatalf("still not blocked after %d wrong passwords", loginBurst)
	}
	if wait <= 0 || wait > loginLockout {
		t.Errorf("wait = %s, want something up to %s", wait, loginLockout)
	}

	// One neighbour's mistakes do not lock anyone else out.
	if _, blocked := th.Blocked("198.51.100.4"); blocked {
		t.Error("a different client was caught by the lockout")
	}

	// The right password clears the record.
	th.Passed(key)
	if _, blocked := th.Blocked(key); blocked {
		t.Error("the lockout survived a correct password")
	}
}

func TestGeneratePassword(t *testing.T) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789-"
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		password := GeneratePassword()
		if len(password) != 17 {
			t.Fatalf("password %q is %d characters, want 17", password, len(password))
		}
		if groups := strings.Split(password, "-"); len(groups) != 3 ||
			len(groups[0]) != 5 || len(groups[1]) != 5 || len(groups[2]) != 5 {
			t.Fatalf("password %q is not three groups of five", password)
		}
		if strings.ContainsAny(password, "ilo01") {
			t.Errorf("password %q uses a character that can be misread", password)
		}
		for _, r := range password {
			if !strings.ContainsRune(alphabet, r) {
				t.Fatalf("password %q contains %q", password, r)
			}
		}
		seen[password] = true
	}
	if len(seen) != 200 {
		t.Errorf("only %d of 200 generated passwords were distinct", len(seen))
	}
}

func TestRandomTokenIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		token := randomToken()
		if len(token) != 43 {
			t.Fatalf("token %q is %d characters, want 43", token, len(token))
		}
		if strings.ContainsAny(token, "+/=") {
			t.Errorf("token %q is not URL-safe", token)
		}
		if seen[token] {
			t.Fatalf("token %q came up twice", token)
		}
		seen[token] = true
	}
}

func TestClientKey(t *testing.T) {
	newRequest := func(remote, forwarded string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
		r.RemoteAddr = remote
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		return r
	}
	tests := []struct {
		name       string
		remote     string
		forwarded  string
		trustProxy bool
		want       string
	}{
		{"the socket, with its port dropped", "203.0.113.9:53142", "", false, "203.0.113.9"},
		{"a forwarded header is ignored by default", "203.0.113.9:53142", "198.51.100.4", false, "203.0.113.9"},
		{"and trusted when the operator says so", "203.0.113.9:53142", "198.51.100.4", true, "198.51.100.4"},
		{"only the first hop of a chain counts", "203.0.113.9:53142", "198.51.100.4, 203.0.113.9", true, "198.51.100.4"},
		{"an empty header falls back to the socket", "203.0.113.9:53142", "", true, "203.0.113.9"},
		{"an address with no port is used whole", "unix-socket", "", false, "unix-socket"},
	}
	for _, tc := range tests {
		got := clientKey(newRequest(tc.remote, tc.forwarded), tc.trustProxy)
		if got != tc.want {
			t.Errorf("%s: clientKey = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsSecureRequest(t *testing.T) {
	plain := httptest.NewRequest(http.MethodGet, "/admin", nil)
	if isSecureRequest(plain, false) || isSecureRequest(plain, true) {
		t.Error("a plain HTTP request was called secure")
	}

	proxied := httptest.NewRequest(http.MethodGet, "/admin", nil)
	proxied.Header.Set("X-Forwarded-Proto", "HTTPS")
	if isSecureRequest(proxied, false) {
		t.Error("a proxy header was believed without being trusted")
	}
	if !isSecureRequest(proxied, true) {
		t.Error("a trusted proxy header was not believed")
	}

	direct := httptest.NewRequest(http.MethodGet, "/admin", nil)
	direct.TLS = &tls.ConnectionState{}
	if !isSecureRequest(direct, false) {
		t.Error("a TLS connection was not recognised")
	}
}
