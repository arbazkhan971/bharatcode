package offline

import (
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

func TestEnabledFromEnv(t *testing.T) {
	cases := map[string]bool{
		"1":     true,
		"true":  true,
		"TRUE":  true,
		" yes ": true,
		"on":    true,
		"0":     false,
		"false": false,
		"":      false,
		"nope":  false,
	}
	for value, want := range cases {
		t.Setenv(EnvVar, value)
		if got := EnabledFromEnv(); got != want {
			t.Errorf("EnabledFromEnv() with %q = %v, want %v", value, got, want)
		}
	}
}

func TestIsLoopbackURL(t *testing.T) {
	local := []string{
		"http://localhost:11434",
		"http://127.0.0.1:1234/v1",
		"https://LOCALHOST/v1",
		"http://[::1]:8080",
		"http://127.5.6.7",
	}
	for _, u := range local {
		if !isLoopbackURL(u) {
			t.Errorf("isLoopbackURL(%q) = false, want true", u)
		}
	}
	remote := []string{
		"https://api.openai.com/v1",
		"https://api.anthropic.com/v1",
		"http://10.0.0.5:11434",
		"http://example.com",
		"",
		"::not a url::",
		"http://", // no host
	}
	for _, u := range remote {
		if isLoopbackURL(u) {
			t.Errorf("isLoopbackURL(%q) = true, want false", u)
		}
	}
}

func TestCheckProvidersAcceptsLocal(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		{Name: "ollama", Type: config.ProviderOllama},     // default localhost
		{Name: "lmstudio", Type: config.ProviderLMStudio}, // default localhost
		{Name: "local-oai", Type: config.ProviderOpenAICompatible, BaseURL: "http://127.0.0.1:8000/v1"},
		{Name: "local-anthropic", Type: config.ProviderAnthropic, BaseURL: "http://localhost:9000"},
	}}
	if err := CheckProviders(cfg); err != nil {
		t.Fatalf("CheckProviders() = %v, want nil", err)
	}
}

func TestCheckProvidersRejectsRemote(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		{Name: "anthropic", Type: config.ProviderAnthropic}, // remote default
		{Name: "openai", Type: config.ProviderOpenAI},       // remote default
		{Name: "remote-compat", Type: config.ProviderOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1"},
		{Name: "codex", Type: config.ProviderCodexOAuth, BaseURL: "http://localhost:1/auth"}, // always remote
		{Name: "local", Type: config.ProviderOllama},                                         // local: must NOT appear
	}}
	err := CheckProviders(cfg)
	if err == nil {
		t.Fatal("CheckProviders() = nil, want error")
	}
	msg := err.Error()
	for _, name := range []string{"anthropic", "openai", "remote-compat", "codex"} {
		if !strings.Contains(msg, name) {
			t.Errorf("error %q does not mention rejected provider %q", msg, name)
		}
	}
	if strings.Contains(msg, "\"local\"") {
		t.Errorf("error %q wrongly mentions local provider", msg)
	}
}

func TestCheckProvidersIgnoresDisabled(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		{Name: "anthropic", Type: config.ProviderAnthropic, Disabled: true},
		{Name: "ollama", Type: config.ProviderOllama},
	}}
	if err := CheckProviders(cfg); err != nil {
		t.Fatalf("CheckProviders() with disabled remote = %v, want nil", err)
	}
}

func TestCheckProvidersNilConfig(t *testing.T) {
	if err := CheckProviders(nil); err != nil {
		t.Fatalf("CheckProviders(nil) = %v, want nil", err)
	}
}

func TestCheckMCPServersAcceptsLocal(t *testing.T) {
	cfg := &config.Config{MCP: []config.MCPServer{
		{Name: "fs", Transport: "stdio", Command: "npx"},                       // local child process
		{Name: "remote-url-but-stdio", Transport: "stdio", URL: "https://x.y"}, // transport wins
		{Name: "local-http", Transport: "http", URL: "http://localhost:8080/mcp"},
		{Name: "local-sse", Transport: "sse", URL: "http://127.0.0.1:9000/sse"},
	}}
	if err := CheckMCPServers(cfg); err != nil {
		t.Fatalf("CheckMCPServers() = %v, want nil", err)
	}
}

func TestCheckMCPServersRejectsRemote(t *testing.T) {
	cfg := &config.Config{MCP: []config.MCPServer{
		{Name: "remote-http", Transport: "http", URL: "https://mcp.example.com/v1"},
		{Name: "remote-sse", Transport: "sse", URL: "https://sse.example.com/stream"},
		{Name: "no-url", Transport: "http"},                 // fail closed: missing url is not local
		{Name: "local", Transport: "stdio", Command: "npx"}, // local: must NOT appear
	}}
	err := CheckMCPServers(cfg)
	if err == nil {
		t.Fatal("CheckMCPServers() = nil, want error")
	}
	msg := err.Error()
	for _, name := range []string{"remote-http", "remote-sse", "no-url"} {
		if !strings.Contains(msg, name) {
			t.Errorf("error %q does not mention rejected server %q", msg, name)
		}
	}
	if strings.Contains(msg, "\"local\"") {
		t.Errorf("error %q wrongly mentions local stdio server", msg)
	}
}

func TestCheckMCPServersIgnoresDisabled(t *testing.T) {
	cfg := &config.Config{MCP: []config.MCPServer{
		{Name: "remote", Transport: "http", URL: "https://mcp.example.com", Disabled: true},
		{Name: "local", Transport: "stdio", Command: "npx"},
	}}
	if err := CheckMCPServers(cfg); err != nil {
		t.Fatalf("CheckMCPServers() with disabled remote = %v, want nil", err)
	}
}

func TestCheckMCPServersNilConfig(t *testing.T) {
	if err := CheckMCPServers(nil); err != nil {
		t.Fatalf("CheckMCPServers(nil) = %v, want nil", err)
	}
}
