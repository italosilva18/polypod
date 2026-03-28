package modes

import "testing"

func TestSetMode(t *testing.T) {
	s := NewState()
	if s.Current != ModeEdit {
		t.Fatalf("expected edit, got %s", s.Current)
	}

	tests := []struct {
		input   string
		want    Mode
		wantErr bool
	}{
		{"plan", ModePlan, false},
		{"ask", ModeAsk, false},
		{"edit", ModeEdit, false},
		{"auto", ModeAuto, false},
		{"PLAN", ModePlan, false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		_, err := s.SetMode(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("SetMode(%q) expected error", tt.input)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("SetMode(%q) unexpected error: %v", tt.input, err)
		}
		if !tt.wantErr && s.Current != tt.want {
			t.Errorf("SetMode(%q) = %s, want %s", tt.input, s.Current, tt.want)
		}
	}
}

func TestBlockedTools(t *testing.T) {
	s := NewState()

	// Edit mode: no blocked tools
	if tools := s.BlockedTools(); len(tools) != 0 {
		t.Errorf("edit mode should block 0 tools, got %d", len(tools))
	}

	// Ask mode: block all
	s.SetMode("ask")
	if tools := s.BlockedTools(); len(tools) != 1 || tools[0] != "*" {
		t.Errorf("ask mode should block *, got %v", tools)
	}

	// Plan mode: block write tools
	s.SetMode("plan")
	tools := s.BlockedTools()
	if len(tools) == 0 {
		t.Error("plan mode should block write tools")
	}
}

func TestSystemPromptPrefix(t *testing.T) {
	s := NewState()
	if p := s.SystemPromptPrefix(); p != "" {
		t.Error("edit mode should have empty prefix")
	}
	s.SetMode("plan")
	if p := s.SystemPromptPrefix(); p == "" {
		t.Error("plan mode should have non-empty prefix")
	}
}
