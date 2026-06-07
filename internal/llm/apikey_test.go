package llm

import (
	"errors"
	"testing"
)

// stubKeyring is a test double for keyringReader and keyringWriter. Get returns
// a fixed value (or err); Set records into values (or returns setErr) so writer
// behaviour can be asserted.
type stubKeyring struct {
	values map[string]string // key: account
	err    error
	setErr error
}

func (s *stubKeyring) Get(_, account string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.values[account], nil
}

func (s *stubKeyring) Set(_, account, secret string) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[account] = secret
	return nil
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

func TestHasAPIKey(t *testing.T) {
	t.Run("empty env needs no key", func(t *testing.T) {
		// A local provider (no api_key_env) is always usable.
		if !HasAPIKey("", "ollama") {
			t.Error("HasAPIKey with empty envVar must report true")
		}
	})

	t.Run("env set", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "env-token")
		prev := activeKeyring
		activeKeyring = &stubKeyring{}
		t.Cleanup(func() { activeKeyring = prev })

		if !HasAPIKey("TEST_KEY_ENV", "myprovider") {
			t.Error("HasAPIKey must report true when the env var is set")
		}
	})

	t.Run("keyring set", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "")
		prev := activeKeyring
		activeKeyring = &stubKeyring{values: map[string]string{"myprovider": "keyring-token"}}
		t.Cleanup(func() { activeKeyring = prev })

		if !HasAPIKey("TEST_KEY_ENV", "myprovider") {
			t.Error("HasAPIKey must report true when the keyring holds a token")
		}
	})

	t.Run("neither source", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "")
		prev := activeKeyring
		activeKeyring = &stubKeyring{values: map[string]string{}}
		t.Cleanup(func() { activeKeyring = prev })

		if HasAPIKey("TEST_KEY_ENV", "myprovider") {
			t.Error("HasAPIKey must report false when no key resolves")
		}
	})
}

func TestStoreAPIKey(t *testing.T) {
	t.Run("persists then resolves", func(t *testing.T) {
		t.Setenv("TEST_KEY_ENV", "")
		store := &stubKeyring{}
		prevR, prevW := activeKeyring, activeKeyringWriter
		activeKeyring = store
		activeKeyringWriter = store
		t.Cleanup(func() { activeKeyring = prevR; activeKeyringWriter = prevW })

		if err := StoreAPIKey("myprovider", "stored-token"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// A stored token must be resolvable immediately (lazy resolution).
		got, err := resolveAPIKey("TEST_KEY_ENV", "myprovider")
		if err != nil {
			t.Fatalf("unexpected resolve error: %v", err)
		}
		if got != "stored-token" {
			t.Errorf("got %q, want %q", got, "stored-token")
		}
	})

	t.Run("wraps writer error", func(t *testing.T) {
		prevW := activeKeyringWriter
		activeKeyringWriter = &stubKeyring{setErr: errors.New("keyring locked")}
		t.Cleanup(func() { activeKeyringWriter = prevW })

		err := StoreAPIKey("myprovider", "tok")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := err.Error(); got == "" {
			t.Error("error message must not be empty")
		}
	})
}
