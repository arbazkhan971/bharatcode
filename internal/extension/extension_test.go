package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/tools"
)

// fakeTool is a minimal tools.Tool used to exercise RegisterTool.
type fakeTool struct {
	name string
}

func (f fakeTool) Name() string            { return f.name }
func (f fakeTool) Description() string     { return "fake " + f.name }
func (f fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeTool) Run(context.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: "ok"}, nil
}

func TestHostRegisterToolAndCommand(t *testing.T) {
	h := NewHost(nil)

	if err := h.RegisterTool(fakeTool{name: "alpha"}); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if err := h.RegisterTool(fakeTool{name: "alpha"}); err == nil {
		t.Fatalf("RegisterTool: expected duplicate error")
	}
	if err := h.RegisterTool(nil); err == nil {
		t.Fatalf("RegisterTool(nil): expected error")
	}
	if got := h.Tools(); len(got) != 1 || got[0].Name() != "alpha" {
		t.Fatalf("Tools: got %+v want one tool alpha", got)
	}

	if err := h.RegisterCommand(Command{Name: "greet", Prompt: "hi"}); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}
	if err := h.RegisterCommand(Command{Name: "greet"}); err == nil {
		t.Fatalf("RegisterCommand: expected duplicate error")
	}
	cmds := h.GetCommands()
	if len(cmds) != 1 || cmds[0].Name != "greet" || cmds[0].Prompt != "hi" {
		t.Fatalf("GetCommands: got %+v want one command greet", cmds)
	}
}

// TestDispatchObservesAndOrders confirms handlers fire in registration order and
// every registered handler for an event sees the payload.
func TestDispatchObservesAndOrders(t *testing.T) {
	h := NewHost(nil)
	var order []string
	h.On(SessionStart, func(_ context.Context, p HookPayload) (HookResult, error) {
		if p.Event != SessionStart {
			t.Errorf("payload event = %q want %q", p.Event, SessionStart)
		}
		order = append(order, "first")
		return HookResult{}, nil
	})
	h.On(SessionStart, func(_ context.Context, _ HookPayload) (HookResult, error) {
		order = append(order, "second")
		return HookResult{}, nil
	})

	res, err := h.Dispatch(context.Background(), SessionStart, HookPayload{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Block {
		t.Fatalf("Dispatch: SessionStart must not block")
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("handler order: got %v want [first second]", order)
	}
}

// TestDispatchVetoShortCircuits confirms the first blocking handler wins and the
// later handler does not run.
func TestDispatchVetoShortCircuits(t *testing.T) {
	h := NewHost(nil)
	ran := false
	h.On(BeforeToolCall, func(_ context.Context, p HookPayload) (HookResult, error) {
		if p.ToolName != "bash" {
			t.Errorf("ToolName = %q want bash", p.ToolName)
		}
		return HookResult{Block: true, Reason: "denied"}, nil
	})
	h.On(BeforeToolCall, func(_ context.Context, _ HookPayload) (HookResult, error) {
		ran = true
		return HookResult{}, nil
	})

	res, err := h.Dispatch(context.Background(), BeforeToolCall, HookPayload{ToolName: "bash"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !res.Block || res.Reason != "denied" {
		t.Fatalf("Dispatch result: got %+v want blocked with reason denied", res)
	}
	if ran {
		t.Fatalf("second handler ran despite an earlier veto")
	}
}

// TestDispatchErrorIsPassThrough confirms a handler error never blocks and the
// remaining handlers still run.
func TestDispatchErrorIsPassThrough(t *testing.T) {
	h := NewHost(nil)
	reached := false
	h.On(BeforeProviderRequest, func(_ context.Context, _ HookPayload) (HookResult, error) {
		return HookResult{}, errors.New("boom")
	})
	h.On(BeforeProviderRequest, func(_ context.Context, _ HookPayload) (HookResult, error) {
		reached = true
		return HookResult{}, nil
	})

	res, err := h.Dispatch(context.Background(), BeforeProviderRequest, HookPayload{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Block {
		t.Fatalf("Dispatch: a handler error must not block")
	}
	if !reached {
		t.Fatalf("handler after an erroring one did not run")
	}
}

// TestNilHostDispatch confirms a nil host is a safe no-op pass-through.
func TestNilHostDispatch(t *testing.T) {
	var h *Host
	res, err := h.Dispatch(context.Background(), SessionStart, HookPayload{})
	if err != nil || res.Block {
		t.Fatalf("nil Dispatch: got res=%+v err=%v want pass-through", res, err)
	}
	if got := h.Tools(); got != nil {
		t.Fatalf("nil Tools: got %+v want nil", got)
	}
	if got := h.GetCommands(); got != nil {
		t.Fatalf("nil GetCommands: got %+v want nil", got)
	}
}

// stubExtension is a compiled extension used to exercise the Register path.
type stubExtension struct {
	name  string
	setup func(API) error
}

func (s stubExtension) Name() string      { return s.name }
func (s stubExtension) Setup(a API) error { return s.setup(a) }

// TestCompiledExtensionLoaded confirms Register + Load runs Setup and folds the
// extension's contributions into the host.
func TestCompiledExtensionLoaded(t *testing.T) {
	Register(stubExtension{
		name: "stub-ext-test",
		setup: func(a API) error {
			if err := a.RegisterCommand(Command{Name: "stubcmd", Prompt: "p"}); err != nil {
				return err
			}
			a.On(SessionStart, func(context.Context, HookPayload) (HookResult, error) {
				return HookResult{}, nil
			})
			return nil
		},
	})

	host, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	found := false
	for _, name := range host.Names() {
		if name == "stub-ext-test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Names: stub-ext-test not loaded; got %v", host.Names())
	}
	var hasCmd bool
	for _, c := range host.GetCommands() {
		if c.Name == "stubcmd" {
			hasCmd = true
		}
	}
	if !hasCmd {
		t.Fatalf("stub extension command not registered; got %v", host.GetCommands())
	}
}

func TestExecEnvAccessor(t *testing.T) {
	env := NewOSEnv("/tmp/project")
	h := NewHost(env)
	if h.Env().WorkDir() != "/tmp/project" {
		t.Fatalf("Env().WorkDir() = %q want /tmp/project", h.Env().WorkDir())
	}
	t.Setenv("BHARATCODE_EXT_ENV_PROBE", "yes")
	if h.Env().Getenv("BHARATCODE_EXT_ENV_PROBE") != "yes" {
		t.Fatalf("Env().Getenv probe failed")
	}
}
