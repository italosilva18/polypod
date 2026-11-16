// Package crush_agent — Agent System from CRUSH
package crush_agent

type Agent struct {
	Name    string
	Role    string
	Tools   []string
	Context map[string]string
}

type AgentManager struct {
	agents []Agent
}

func NewAgentManager() *AgentManager {
	return &AgentManager{agents: make([]Agent, 0)}
}

func (m *AgentManager) CreateAgent(name, role string, tools []string) *Agent {
	agent := Agent{Name: name, Role: role, Tools: tools, Context: make(map[string]string)}
	m.agents = append(m.agents, agent)
	return &agent
}

func (m *AgentManager) ListAgents() []Agent {
	return m.agents
}
