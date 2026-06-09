package llm

import (
	"errors"
	"net/http"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

// stubFactory builds a factory whose Provider reports the given name, so tests
// can assert that Lookup returned the exact factory they registered.
func stubFactory(name string) Factory {
	return func(spec ProviderSpec) (Provider, error) {
		return newOllamaProvider(name, spec.Config.BaseURL, spec.Models, spec.Client), nil
	}
}

func TestRegisterAndLookup(t *testing.T) {
	const key = "test-register-lookup"
	if err := Register(key, stubFactory("p1")); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	got, ok := Lookup(key)
	if !ok {
		t.Fatalf("Lookup(%q): not found after Register", key)
	}

	provider, err := got(ProviderSpec{
		Config: config.Provider{BaseURL: "http://localhost:1234"},
		Client: &http.Client{},
	})
	if err != nil {
		t.Fatalf("factory: unexpected error: %v", err)
	}
	if provider.Name() != "p1" {
		t.Fatalf("factory built wrong provider: got %q want %q", provider.Name(), "p1")
	}
}

func TestLookupCaseInsensitive(t *testing.T) {
	if err := Register("Test-Case-Fold", stubFactory("cf")); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	for _, key := range []string{"test-case-fold", "TEST-CASE-FOLD", "  Test-Case-Fold  "} {
		if _, ok := Lookup(key); !ok {
			t.Fatalf("Lookup(%q): expected case-insensitive hit", key)
		}
	}
}

func TestRegisterDuplicate(t *testing.T) {
	const key = "test-duplicate"
	if err := Register(key, stubFactory("first")); err != nil {
		t.Fatalf("Register: unexpected first error: %v", err)
	}

	err := Register(key, stubFactory("second"))
	if err == nil {
		t.Fatalf("Register: expected duplicate error, got nil")
	}
	if !errors.Is(err, ErrFactoryRegistered) {
		t.Fatalf("Register: error = %v, want ErrFactoryRegistered", err)
	}

	// The first registration must survive the rejected duplicate.
	got, ok := Lookup(key)
	if !ok {
		t.Fatalf("Lookup(%q): missing after duplicate attempt", key)
	}
	provider, err := got(ProviderSpec{Client: &http.Client{}})
	if err != nil {
		t.Fatalf("factory: unexpected error: %v", err)
	}
	if provider.Name() != "first" {
		t.Fatalf("duplicate Register overwrote entry: got %q want %q", provider.Name(), "first")
	}
}

func TestLookupMissing(t *testing.T) {
	if f, ok := Lookup("test-never-registered"); ok || f != nil {
		t.Fatalf("Lookup: expected miss, got ok=%v factory=%v", ok, f)
	}
}

func TestRegisterInvalid(t *testing.T) {
	if err := Register("   ", stubFactory("blank")); err == nil {
		t.Fatalf("Register: expected error for empty key")
	}
	if err := Register("test-nil-factory", nil); err == nil {
		t.Fatalf("Register: expected error for nil factory")
	}
	// A rejected malformed registration must not leave a phantom entry.
	if _, ok := Lookup("test-nil-factory"); ok {
		t.Fatalf("Lookup: nil-factory key should not be registered")
	}
}

func TestMustRegisterPanics(t *testing.T) {
	const key = "test-must-register"
	MustRegister(key, stubFactory("ok"))

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("MustRegister: expected panic on duplicate key")
		}
	}()
	MustRegister(key, stubFactory("dup"))
}

func TestRegistered(t *testing.T) {
	keys := []string{"test-registered-c", "test-registered-a", "test-registered-b"}
	for _, k := range keys {
		if err := Register(k, stubFactory(k)); err != nil {
			t.Fatalf("Register(%q): %v", k, err)
		}
	}

	listed := Registered()

	// The slice must be sorted and must include every key registered above.
	for i := 1; i < len(listed); i++ {
		if listed[i-1] > listed[i] {
			t.Fatalf("Registered: not sorted at %d: %q > %q", i, listed[i-1], listed[i])
		}
	}
	for _, want := range keys {
		found := false
		for _, k := range listed {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Registered: missing key %q", want)
		}
	}
}
