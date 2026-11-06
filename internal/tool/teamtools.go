package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// Team represents a team definition with members and permissions.
type Team struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Members     []string        `json:"members"`
	CreatedAt   time.Time       `json:"created_at"`
	Permissions map[string]bool `json:"permissions"`
}

// TeamStore holds team definitions with concurrent-safe access.
type TeamStore struct {
	mu      sync.Mutex
	teams   []Team
	counter int
}

// NewTeamStore creates an empty TeamStore.
func NewTeamStore() *TeamStore {
	return &TeamStore{}
}

// globalTeamStore is the package-level singleton store.
var globalTeamStore = NewTeamStore()

// GetTeamStore returns the package-level singleton store.
func GetTeamStore() *TeamStore {
	return globalTeamStore
}

// Create adds a new team to the store and returns it.
func (s *TeamStore) Create(name string, members []string) Team {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	team := Team{
		ID:          fmt.Sprintf("team_%d", s.counter),
		Name:        name,
		Members:     members,
		CreatedAt:   time.Now(),
		Permissions: make(map[string]bool),
	}
	s.teams = append(s.teams, team)
	return team
}

// Delete removes the team identified by id. Returns true if found and removed.
func (s *TeamStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.teams {
		if s.teams[i].ID == id {
			s.teams = append(s.teams[:i], s.teams[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns the team identified by id and whether it was found.
func (s *TeamStore) Get(id string) (Team, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.teams {
		if t.ID == id {
			return t, true
		}
	}
	return Team{}, false
}

// List returns all teams in the store.
func (s *TeamStore) List() []Team {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]Team, len(s.teams))
	copy(result, s.teams)
	return result
}

// AddMember adds a member to the team identified by teamID.
func (s *TeamStore) AddMember(teamID, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.teams {
		if s.teams[i].ID == teamID {
			// Check for duplicate.
			for _, m := range s.teams[i].Members {
				if m == member {
					return fmt.Errorf("membro %s ja existe no time %s", member, teamID)
				}
			}
			s.teams[i].Members = append(s.teams[i].Members, member)
			return nil
		}
	}
	return fmt.Errorf("time %s nao encontrado", teamID)
}

// RemoveMember removes a member from the team identified by teamID.
func (s *TeamStore) RemoveMember(teamID, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.teams {
		if s.teams[i].ID == teamID {
			for j, m := range s.teams[i].Members {
				if m == member {
					s.teams[i].Members = append(s.teams[i].Members[:j], s.teams[i].Members[j+1:]...)
					return nil
				}
			}
			return fmt.Errorf("membro %s nao encontrado no time %s", member, teamID)
		}
	}
	return fmt.Errorf("time %s nao encontrado", teamID)
}

// ---------------------------------------------------------------------------
// Tool constructors
// ---------------------------------------------------------------------------

// TeamCreateTool creates a new team with optional members and returns JSON
// of the newly created Team.
var TeamCreateTool = types.BuildTool(types.ToolDef{
	NameFn:        func() string { return "team_create" },
	DescriptionFn: func() string { return "Cria um novo time com nome e membros opcionais" },
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Nome do time",
				},
				"members": map[string]any{
					"type":        "string",
					"description": "Lista de membros separados por virgula (opcional)",
				},
			},
			"required": []string{"name"},
		}
	},
	IsDestructiveFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		name, _ := args["name"].(string)
		if name == "" {
			return types.ToolResult{Content: "name e obrigatorio", IsError: true}, nil
		}

		var members []string
		if raw, ok := args["members"].(string); ok && raw != "" {
			for _, m := range strings.Split(raw, ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					members = append(members, m)
				}
			}
		}

		team := globalTeamStore.Create(name, members)

		data, err := json.Marshal(team)
		if err != nil {
			return types.ToolResult{Content: fmt.Sprintf("erro ao serializar: %v", err), IsError: true}, nil
		}
		return types.ToolResult{Content: string(data)}, nil
	},
})

// TeamDeleteTool removes an existing team by its ID.
var TeamDeleteTool = types.BuildTool(types.ToolDef{
	NameFn:        func() string { return "team_delete" },
	DescriptionFn: func() string { return "Remove um time existente pelo ID" },
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"team_id": map[string]any{
					"type":        "string",
					"description": "ID do time a ser removido",
				},
			},
			"required": []string{"team_id"},
		}
	},
	IsDestructiveFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		teamID, _ := args["team_id"].(string)
		if teamID == "" {
			return types.ToolResult{Content: "team_id e obrigatorio", IsError: true}, nil
		}

		if !globalTeamStore.Delete(teamID) {
			return types.ToolResult{Content: fmt.Sprintf("time %s nao encontrado", teamID), IsError: true}, nil
		}
		return types.ToolResult{Content: "removido"}, nil
	},
})

// TeamListTool returns a JSON array of all teams.
var TeamListTool = types.BuildTool(types.ToolDef{
	NameFn:        func() string { return "team_list" },
	DescriptionFn: func() string { return "Lista todos os times cadastrados" },
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	},
	IsReadOnlyFn:    func(map[string]any) bool { return true },
	IsConcurrencyFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		teams := globalTeamStore.List()

		data, err := json.Marshal(teams)
		if err != nil {
			return types.ToolResult{Content: fmt.Sprintf("erro ao serializar: %v", err), IsError: true}, nil
		}
		return types.ToolResult{Content: string(data)}, nil
	},
})

// NewTeamTools returns all three team tool instances.
func NewTeamTools() []types.Tool {
	return []types.Tool{
		TeamCreateTool,
		TeamDeleteTool,
		TeamListTool,
	}
}
