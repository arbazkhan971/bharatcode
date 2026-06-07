package llm

import (
	"errors"
	"testing"
)

// stubKeyring is a test double for keyringReader that returns a fixed value.
type stubKeyring struct {
	values map[string]string // key: account
	err    error
}

func (s *stubKeyring) Get(_, account string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.values[account], nil
}

func TestResolveAPIKey(t *testing.T) {
	t.Run("env wins over keyring", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "env-token")
		prev := activeKeyring
		activeKeyring = &stubKeyring{values: map[string]string{"myprovider": "keyring-token"}}
		t.Cleanup(func() { activeKeyring = prev })

		got, err := resolveAPIKey("TEST_KEY_ENV", "myprovider")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "env-token" {
			t.Errorf("got %q, want %q", got, "env-token")
		}
	})

	t.Run("env empty, keyring used", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "")
		prev := activeKeyring
		activeKeyring = &stubKeyring{values: map[string]string{"myprovider": "keyring-token"}}
		t.Cleanup(func() { activeKeyring = prev })

		got, err := resolveAPIKey("TEST_KEY_ENV", "myprovider")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "keyring-token" {
			t.Errorf("got %q, want %q", got, "keyring-token")
		}
	})

	t.Run("both empty returns ErrAuth", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "")
		prev := activeKeyring
		activeKeyring = &stubKeyring{values: map[string]string{}}
		t.Cleanup(func() { activeKeyring = prev })

		_, err := resolveAPIKey("TEST_KEY_ENV", "myprovider")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrAuth) {
			t.Errorf("error %v does not wrap ErrAuth", err)
		}
	})

	t.Run("keyring error falls through to ErrAuth", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "")
		prev := activeKeyring
		activeKeyring = &stubKeyring{err: errors.New("keyring locked")}
		t.Cleanup(func() { activeKeyring = prev })

		_, err := resolveAPIKey("TEST_KEY_ENV", "myprovider")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrAuth) {
			t.Errorf("error %v does not wrap ErrAuth", err)
		}
	})
}
