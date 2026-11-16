package arena

import (
	"testing"
)

func TestNewArena(t *testing.T) {
	arena := NewArena("Test task", 3)
	
	if arena.task != "Test task" {
		t.Errorf("Expected 'Test task', got %s", arena.task)
	}
	if len(arena.agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(arena.agents))
	}
}

func TestSubmitSolution(t *testing.T) {
	arena := NewArena("Test", 2)
	arena.SubmitSolution("agent_0", "Solution 1")
	
	if arena.agents[0].Solution != "Solution 1" {
		t.Errorf("Expected 'Solution 1', got %s", arena.agents[0].Solution)
	}
}

func TestScore(t *testing.T) {
	arena := NewArena("Test", 2)
	arena.Score()
	
	if arena.winner == "" {
		t.Error("Expected winner to be set")
	}
}
