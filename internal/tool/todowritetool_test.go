package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// Helper: build a ToolContext sufficient for Execute calls.
func newTodoToolContext() types.ToolContext {
	return types.ToolContext{Context: context.Background()}
}

// Helper: build an args map the way the JSON decoder would produce it.
func todosArg(items ...map[string]any) map[string]any {
	raw := make([]any, 0, len(items))
	for _, it := range items {
		raw = append(raw, it)
	}
	return map[string]any{"todos": raw}
}

func TestTodoStore_SetAndGet(t *testing.T) {
	store := NewTodoStore()

	if got := store.Get(); got != nil {
		t.Fatalf("expected empty store, got %+v", got)
	}
	if got := store.Len(); got != 0 {
		t.Fatalf("expected Len 0, got %d", got)
	}

	initial := []Todo{
		{Content: "Escrever testes", Status: TodoStatusInProgress, ActiveForm: "Escrevendo testes"},
		{Content: "Documentar API", Status: TodoStatusPending, ActiveForm: "Documentando API"},
		{Content: "Revisar PR", Status: TodoStatusCompleted, ActiveForm: "Revisando PR"},
	}
	store.Set(initial)

	got := store.Get()
	if len(got) != 3 {
		t.Fatalf("expected 3 todos, got %d", len(got))
	}
	for i := range got {
		if got[i] != initial[i] {
			t.Errorf("todo[%d] mismatch: got %+v want %+v", i, got[i], initial[i])
		}
	}

	// Mutating the returned slice must not affect the store.
	got[0].Content = "clobbered"
	again := store.Get()
	if again[0].Content != "Escrever testes" {
		t.Errorf("Get returned a live reference: store now has %q", again[0].Content)
	}

	// Mutating the input slice after Set must not affect the store either.
	initial[1].Content = "clobbered"
	again = store.Get()
	if again[1].Content != "Documentar API" {
		t.Errorf("Set kept a live reference: store now has %q", again[1].Content)
	}

	// Replacing the list fully overwrites.
	store.Set([]Todo{
		{Content: "Nova tarefa", Status: TodoStatusPending, ActiveForm: "Executando nova tarefa"},
	})
	if store.Len() != 1 {
		t.Errorf("expected 1 todo after replace, got %d", store.Len())
	}

	// Setting nil clears.
	store.Set(nil)
	if store.Len() != 0 {
		t.Errorf("expected empty store after Set(nil), got %d", store.Len())
	}
}

func TestTodoWriteTool_ExecuteWritesToStore(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoWriteTool(store)

	if tool.Name() != constants.ToolTodoWrite {
		t.Errorf("expected name %q, got %q", constants.ToolTodoWrite, tool.Name())
	}
	if tool.IsReadOnly(nil) {
		t.Error("todo_write must not be read-only")
	}
	if tool.IsDestructive(nil) {
		t.Error("todo_write must not be destructive")
	}
	if tool.IsConcurrencySafe(nil) {
		t.Error("todo_write must not be concurrency-safe")
	}
	if !tool.IsEnabled() {
		t.Error("todo_write should be enabled when store is non-nil")
	}

	// Schema should declare the three required fields on each item.
	schema := tool.InputSchema()
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["todos"]; !ok {
		t.Error("schema missing 'todos' property")
	}

	args := todosArg(
		map[string]any{"content": "Implementar feature", "status": TodoStatusInProgress, "activeForm": "Implementando feature"},
		map[string]any{"content": "Escrever testes", "status": TodoStatusPending, "activeForm": "Escrevendo testes"},
		map[string]any{"content": "Fazer deploy", "status": TodoStatusPending, "activeForm": "Fazendo deploy"},
	)

	if vr := tool.ValidateInput(args); !vr.Valid {
		t.Fatalf("ValidateInput failed: %s", vr.Message)
	}

	res, err := tool.Execute(newTodoToolContext(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "3") {
		t.Errorf("expected summary to contain count 3, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "in_progress") || !strings.Contains(res.Content, "pending") {
		t.Errorf("expected summary breakdown, got %q", res.Content)
	}

	// Store should now reflect the executed call.
	got := store.Get()
	if len(got) != 3 {
		t.Fatalf("expected 3 todos in store, got %d", len(got))
	}
	if got[0].Content != "Implementar feature" || got[0].Status != TodoStatusInProgress {
		t.Errorf("unexpected first todo: %+v", got[0])
	}
	if got[0].ActiveForm != "Implementando feature" {
		t.Errorf("expected activeForm to round-trip, got %q", got[0].ActiveForm)
	}
}

func TestTodoWriteTool_RejectsMultipleInProgress(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoWriteTool(store)

	args := todosArg(
		map[string]any{"content": "Tarefa A", "status": TodoStatusInProgress, "activeForm": "Executando A"},
		map[string]any{"content": "Tarefa B", "status": TodoStatusInProgress, "activeForm": "Executando B"},
	)

	vr := tool.ValidateInput(args)
	if vr.Valid {
		t.Fatal("expected validation to fail for two in_progress todos")
	}
	if !strings.Contains(vr.Message, "in_progress") {
		t.Errorf("expected validation message to mention in_progress, got %q", vr.Message)
	}

	// Execute should also refuse and leave the store untouched.
	res, err := tool.Execute(newTodoToolContext(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true, got result %q", res.Content)
	}
	if store.Len() != 0 {
		t.Errorf("store should remain empty on rejected write, got %d entries", store.Len())
	}
}

func TestTodoWriteTool_RejectsInvalidStatus(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoWriteTool(store)

	args := todosArg(
		map[string]any{"content": "Tarefa", "status": "bogus", "activeForm": "Fazendo bogus"},
	)

	vr := tool.ValidateInput(args)
	if vr.Valid {
		t.Fatal("expected validation to reject unknown status")
	}
	if !strings.Contains(vr.Message, "bogus") {
		t.Errorf("expected message to mention bad status, got %q", vr.Message)
	}
}

func TestTodoWriteTool_RejectsMissingFields(t *testing.T) {
	store := NewTodoStore()
	tool := NewTodoWriteTool(store)

	// Missing activeForm
	args := todosArg(
		map[string]any{"content": "Tarefa", "status": TodoStatusPending},
	)
	if vr := tool.ValidateInput(args); vr.Valid {
		t.Error("expected validation to fail when activeForm is missing")
	}

	// Missing content
	args = todosArg(
		map[string]any{"status": TodoStatusPending, "activeForm": "Fazendo algo"},
	)
	if vr := tool.ValidateInput(args); vr.Valid {
		t.Error("expected validation to fail when content is missing")
	}

	// Missing todos entirely
	if vr := tool.ValidateInput(map[string]any{}); vr.Valid {
		t.Error("expected validation to fail when todos is absent")
	}
}

func TestTodoStore_Clear(t *testing.T) {
	store := NewTodoStore()
	store.Set([]Todo{
		{Content: "A", Status: TodoStatusPending, ActiveForm: "Fazendo A"},
		{Content: "B", Status: TodoStatusInProgress, ActiveForm: "Fazendo B"},
	})
	if store.Len() != 2 {
		t.Fatalf("expected 2 todos before Clear, got %d", store.Len())
	}

	store.Clear()
	if store.Len() != 0 {
		t.Errorf("expected 0 todos after Clear, got %d", store.Len())
	}
	if got := store.Get(); got != nil {
		t.Errorf("expected nil slice from Get after Clear, got %+v", got)
	}

	// Clear on an already-empty store is a no-op.
	store.Clear()
	if store.Len() != 0 {
		t.Errorf("Clear on empty store should stay empty, got %d", store.Len())
	}
}

func TestRegisterTodoWriteTool(t *testing.T) {
	reg := NewRegistry()
	store := NewTodoStore()

	RegisterTodoWriteTool(reg, store)

	tool, ok := reg.Get(constants.ToolTodoWrite)
	if !ok {
		t.Fatalf("tool %q was not registered", constants.ToolTodoWrite)
	}
	if tool.Name() != constants.ToolTodoWrite {
		t.Errorf("expected name %q, got %q", constants.ToolTodoWrite, tool.Name())
	}

	// End-to-end through the registry pipeline.
	args := todosArg(
		map[string]any{"content": "Only one", "status": TodoStatusCompleted, "activeForm": "Completing"},
	)
	res, err := reg.Execute(constants.ToolTodoWrite, args, newTodoToolContext())
	if err != nil {
		t.Fatalf("registry Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("registry Execute returned error: %s", res.Content)
	}
	if store.Len() != 1 {
		t.Errorf("expected 1 todo in store after registry execute, got %d", store.Len())
	}

	// Nil-safe registration must not panic.
	RegisterTodoWriteTool(nil, store)
}
