package wiring

import (
	"log/slog"
	"os"
	"testing"
)

// TestConfigStructFields verifies the Config struct can be constructed with
// zero values and that all documented fields are assignable.
func TestConfigStructFields(t *testing.T) {
	cfg := Config{}
	_ = cfg

	// Ensure each field is addressable/assignable at compile time.
	cfg.Logger = slog.Default()
	cfg.WorkingDir = "/tmp"
	cfg.Model = "deepseek-chat"
	cfg.DataDir = "/tmp/data"
	cfg.MemoryStore = nil

	if cfg.WorkingDir != "/tmp" {
		t.Errorf("WorkingDir = %q, want /tmp", cfg.WorkingDir)
	}
	if cfg.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want deepseek-chat", cfg.Model)
	}
	if cfg.DataDir != "/tmp/data" {
		t.Errorf("DataDir = %q, want /tmp/data", cfg.DataDir)
	}
	if cfg.Logger == nil {
		t.Error("Logger should be assignable")
	}
}

// TestSystemsStructFields verifies the Systems struct can be constructed
// as a zero value without panicking.
func TestSystemsStructFields(t *testing.T) {
	s := &Systems{}
	if s == nil {
		t.Fatal("Systems{} should not be nil")
	}

	// All fields should default to nil pointers.
	if s.ToolRegistry != nil {
		t.Error("ToolRegistry should be nil in zero-value Systems")
	}
	if s.Orchestrator != nil {
		t.Error("Orchestrator should be nil in zero-value Systems")
	}
	if s.Memory != nil {
		t.Error("Memory should be nil in zero-value Systems")
	}
}

// TestWireAllWithEmptyConfig runs WireAll with a completely empty Config.
// The documented behaviour is that missing values fall back to defaults
// (logger -> slog.Default, working dir -> ".", model -> "deepseek-chat").
// WireAll should return a non-nil Systems with every core subsystem populated.
func TestWireAllWithEmptyConfig(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("WireAll panicked with empty config: %v", r)
		}
	}()

	sys := WireAll(Config{})
	if sys == nil {
		t.Fatal("WireAll returned nil")
	}

	// Core subsystems that must always be wired regardless of config.
	if sys.ToolRegistry == nil {
		t.Error("ToolRegistry should be wired")
	}
	if sys.SkillRegistry == nil {
		t.Error("SkillRegistry should be wired")
	}
	if sys.Orchestrator == nil {
		t.Error("Orchestrator should be wired")
	}
	if sys.ToolAdapter == nil {
		t.Error("ToolAdapter should be wired")
	}
	if sys.Permissions == nil {
		t.Error("Permissions should be wired")
	}
	if sys.Hooks == nil {
		t.Error("Hooks should be wired")
	}
	if sys.Modes == nil {
		t.Error("Modes should be wired")
	}
	if sys.Commands == nil {
		t.Error("Commands should be wired")
	}
	if sys.SlashCommands == nil {
		t.Error("SlashCommands should be wired")
	}
	if sys.Agents == nil {
		t.Error("Agents should be wired")
	}
	if sys.AgentRunner == nil {
		t.Error("AgentRunner should be wired")
	}
	if sys.TaskList == nil {
		t.Error("TaskList should be wired")
	}
	if sys.TaskRegistry == nil {
		t.Error("TaskRegistry should be wired")
	}
	if sys.Tracker == nil {
		t.Error("Tracker should be wired")
	}
	if sys.Swarm == nil {
		t.Error("Swarm should be wired")
	}
	if sys.ContextWindow == nil {
		t.Error("ContextWindow should be wired")
	}
	if sys.UserPromptHandler == nil {
		t.Error("UserPromptHandler should be wired")
	}
	if sys.AskQuestionChannel == nil {
		t.Error("AskQuestionChannel should be wired")
	}
}

// TestWireAllWithDataDir verifies that supplying a DataDir causes the
// file-based memory store to be populated (when none is explicitly provided).
func TestWireAllWithDataDir(t *testing.T) {
	tmp, err := os.MkdirTemp("", "wiring-test-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(tmp)

	cfg := Config{
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		WorkingDir: tmp,
		Model:      "test-model",
		DataDir:    tmp,
	}

	sys := WireAll(cfg)
	if sys == nil {
		t.Fatal("WireAll returned nil")
	}
	if sys.Memory == nil {
		t.Error("Memory store should be auto-created when DataDir is set")
	}
}

// TestWireAllPopulatesToolRegistry sanity-checks that the tool registry is
// not empty after wiring. The exact count is an implementation detail, but
// there should be at least a handful of builtin tools registered.
func TestWireAllPopulatesToolRegistry(t *testing.T) {
	sys := WireAll(Config{})
	if sys.ToolRegistry == nil {
		t.Fatal("ToolRegistry nil")
	}
	count := sys.ToolRegistry.Count()
	if count == 0 {
		t.Error("ToolRegistry should contain builtin tools after WireAll")
	}
	t.Logf("wired tool count: %d", count)
}

// TestWireAllAgentRunPlaceholder verifies that before WireAIClient is called,
// the agent_run tool is registered (as a placeholder). This encodes the
// contract in wiring.go: agent_run is always present, but only functional
// after WireAIClient.
func TestWireAllAgentRunPlaceholder(t *testing.T) {
	sys := WireAll(Config{})
	if sys.ToolRegistry == nil {
		t.Fatal("ToolRegistry nil")
	}
	if _, ok := sys.ToolRegistry.Get("agent_run"); !ok {
		t.Error("agent_run tool should be registered as placeholder by WireAll")
	}
}

// TestWireAllDefaultAgent verifies the default agent is registered so the
// orchestrator can resolve AgentName "default".
func TestWireAllDefaultAgent(t *testing.T) {
	sys := WireAll(Config{})
	if sys.Agents == nil {
		t.Fatal("Agents registry nil")
	}
	if a := sys.Agents.Get("default"); a == nil {
		t.Error("default agent should be registered")
	}
}
