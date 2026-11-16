// Package arena — Agent Arena (Multiple Agents Competing)
package arena

import (
	"fmt"
	"strings"
	"time"
)

// Arena manages competing agents
type Arena struct {
	id        string
	task      string
	agents    []ArenaAgent
	startTime time.Time
	endTime   time.Time
	winner    string
}

// ArenaAgent represents an agent in the arena
type ArenaAgent struct {
	ID       string
	Name     string
	Solution string
	Score    float64
	Rank     int
}

// NewArena creates a new arena
func NewArena(task string, numAgents int) *Arena {
	arena := &Arena{
		id:        fmt.Sprintf("arena_%d", time.Now().UnixNano()),
		task:      task,
		startTime: time.Now(),
		agents:    make([]ArenaAgent, numAgents),
	}
	
	for i := range arena.agents {
		arena.agents[i] = ArenaAgent{
			ID:   fmt.Sprintf("agent_%d", i),
			Name: fmt.Sprintf("Agent %d", i+1),
		}
	}
	
	return arena
}

// SubmitSolution submits a solution from an agent
func (a *Arena) SubmitSolution(agentID, solution string) {
	for i := range a.agents {
		if a.agents[i].ID == agentID {
			a.agents[i].Solution = solution
			break
		}
	}
}

// Score scores all solutions
func (a *Arena) Score() {
	for i := range a.agents {
		// In real implementation, evaluate solution quality
		a.agents[i].Score = 0.0
	}
	
	// Rank agents by score
	a.rankAgents()
}

// rankAgents ranks agents by score
func (a *Arena) rankAgents() {
	// Simple ranking (in real implementation, sort by score)
	for i := range a.agents {
		a.agents[i].Rank = i + 1
	}
	if len(a.agents) > 0 {
		a.winner = a.agents[0].ID
	}
}

// GetWinner returns the winner
func (a *Arena) GetWinner() *ArenaAgent {
	for _, agent := range a.agents {
		if agent.ID == a.winner {
			return &agent
		}
	}
	return nil
}

// FormatArena formats arena for display
func FormatArena(arena *Arena) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚔️ **Agent Arena:** %s\n\n", arena.task))
	sb.WriteString("**Competitors**:\n")
	
	for _, agent := range arena.agents {
		sb.WriteString(fmt.Sprintf("%d. **%s** - Score: %.2f\n", 
			agent.Rank, agent.Name, agent.Score))
	}
	
	if arena.winner != "" {
		winner := arena.GetWinner()
		sb.WriteString(fmt.Sprintf("\n🏆 **Winner:** %s\n", winner.Name))
	}
	
	return sb.String()
}
