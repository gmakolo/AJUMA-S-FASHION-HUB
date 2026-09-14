package main

import (
	"os"
	"strings"
	"testing"
)

func TestEnvOr(t *testing.T) {
	const key = "AJUMA_TEST_VALUE"
	cases := []struct {
		name, set, want string
	}{
		{"unset falls back", "", ":8080"},
		{"a value is used", ":9000", ":9000"},
		{"whitespace is trimmed", "  :9000  ", ":9000"},
		{"a value of only whitespace falls back", "   ", ":8080"},
	}
	for _, c := range cases {
		t.Setenv(key, c.set)
		if got := envOr(key, ":8080"); got != c.want {
			t.Errorf("%s: envOr = %q, want %q", c.name, got, c.want)
		}
	}
	os.Unsetenv(key)
	if got := envOr(key, ":8080"); got != ":8080" {
		t.Errorf("envOr with nothing set = %q, want the fallback", got)
	}
}

func TestPortOf(t *testing.T) {
	cases := []struct{ addr, want string }{
		{":8080", ":8080"},
		{"127.0.0.1:8080", ":8080"},
		{"0.0.0.0:80", ":80"},
		{"8080", ":8080"},
		{"[::1]:8080", ":8080"},
	}
	for _, c := range cases {
		if got := portOf(c.addr); got != c.want {
			t.Errorf("portOf(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

// TestAdminCredentialSources walks the three ways the studio password can be
// settled: a stored hash, a password in the environment, or one invented for
// this run and printed once.
func TestAdminCredentialSources(t *testing.T) {
	const password = "a password long enough to pass"

	t.Run("a stored hash is used as it is", func(t *testing.T) {
		want, err := NewCredential(password)
		if err != nil {
			t.Fatalf("NewCredential: %v", err)
		}
		t.Setenv("AJUMA_ADMIN_PASSWORD_HASH", "  "+want.String()+"  ")
		t.Setenv("AJUMA_ADMIN_PASSWORD", "ignored because the hash wins")

		got, err := adminCredential()
		if err != nil {
			t.Fatalf("adminCredential: %v", err)
		}
		if got.String() != want.String() {
			t.Error("the credential was not the one the hash described")
		}
		if !got.Verify(password) {
			t.Error("the stored hash does not verify its own password")
		}
	})

	t.Run("a hash that cannot be read is refused by name", func(t *testing.T) {
		t.Setenv("AJUMA_ADMIN_PASSWORD_HASH", "pbkdf2-sha256$not-a-number$salt$key")
		if _, err := adminCredential(); err == nil {
			t.Fatal("a hash that cannot be read was accepted")
		} else if !strings.Contains(err.Error(), "AJUMA_ADMIN_PASSWORD_HASH") {
			t.Errorf("error = %v, want it to name the variable at fault", err)
		}
	})

	t.Run("a password in the environment is hashed", func(t *testing.T) {
		t.Setenv("AJUMA_ADMIN_PASSWORD_HASH", "")
		t.Setenv("AJUMA_ADMIN_PASSWORD", password)

		got, err := adminCredential()
		if err != nil {
			t.Fatalf("adminCredential: %v", err)
		}
		if !got.Verify(password) || got.Verify("something else") {
			t.Error("the credential does not match the password it was given")
		}
	})

	t.Run("a password too short to be useful is refused by name", func(t *testing.T) {
		t.Setenv("AJUMA_ADMIN_PASSWORD_HASH", "")
		t.Setenv("AJUMA_ADMIN_PASSWORD", "short")
		if _, err := adminCredential(); err == nil {
			t.Fatal("a five-character password was accepted")
		} else if !strings.Contains(err.Error(), "AJUMA_ADMIN_PASSWORD") {
			t.Errorf("error = %v, want it to name the variable at fault", err)
		}
	})
}
