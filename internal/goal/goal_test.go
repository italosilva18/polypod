package goal

import (
	"testing"
)

func TestNewGoal(t *testing.T) {
	goal := NewGoal("Test objective", []string{"done when 1", "done when 2"})
	
	if goal.Objective != "Test objective" {
		t.Errorf("Expected 'Test objective', got %s", goal.Objective)
	}
	if len(goal.DoneWhen) != 2 {
		t.Errorf("Expected 2 done-when criteria, got %d", len(goal.DoneWhen))
	}
	if goal.Status != "active" {
		t.Errorf("Expected 'active', got %s", goal.Status)
	}
}

func TestVerifier(t *testing.T) {
	v := NewVerifier([]string{"criteria 1", "criteria 2"})
	results := v.Verify()
	
	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
	if v.IsComplete() {
		t.Error("Expected not complete")
	}
}

func TestGoalComplete(t *testing.T) {
	goal := NewGoal("Test", []string{"done"})
	goal.Complete()
	
	if goal.Status != "completed" {
		t.Errorf("Expected 'completed', got %s", goal.Status)
	}
	if goal.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set")
	}
}
