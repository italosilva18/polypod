package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// SessionIndex stores metadata about a conversation session.
type SessionIndex struct {
	ID        string    `json:"id"`
	Channel   string    `json:"channel"`
	UserID    string    `json:"user_id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  int       `json:"messages"`
	Tokens    int       `json:"tokens"`
}

// Manager handles session persistence and resume.
type Manager struct {
	dataDir string
	logger  *slog.Logger
}

// NewManager creates a session manager.
func NewManager(dataDir string, logger *slog.Logger) *Manager {
	os.MkdirAll(filepath.Join(dataDir, "sessions"), 0755)
	return &Manager{dataDir: dataDir, logger: logger}
}

func (m *Manager) indexPath() string {
	return filepath.Join(m.dataDir, "sessions", "index.json")
}

// ListSessions returns all sessions sorted by last update (newest first).
func (m *Manager) ListSessions() ([]SessionIndex, error) {
	data, err := os.ReadFile(m.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []SessionIndex
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// GetLastSession returns the most recent session for a channel.
func (m *Manager) GetLastSession(channel string) (*SessionIndex, error) {
	sessions, err := m.ListSessions()
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		if s.Channel == channel {
			return &s, nil
		}
	}
	return nil, nil
}

// SaveSessionIndex updates the index with a new or modified session entry.
func (m *Manager) SaveSessionIndex(idx SessionIndex) error {
	sessions, _ := m.ListSessions()

	found := false
	for i, s := range sessions {
		if s.ID == idx.ID {
			sessions[i] = idx
			found = true
			break
		}
	}
	if !found {
		sessions = append(sessions, idx)
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.indexPath(), data, 0644)
}

// GenerateSessionID creates a unique session identifier.
func GenerateSessionID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "ses_" + hex.EncodeToString(b)
}

// GenerateTitle creates a title from the first user message.
func GenerateTitle(firstMessage string) string {
	title := strings.TrimSpace(firstMessage)
	if len(title) > 60 {
		title = title[:57] + "..."
	}
	return title
}

// RegisterSkills registers session management skills.
func RegisterSkills(reg *skill.Registry, mgr *Manager) {
	reg.Register(&skill.Skill{
		Name:        "list_sessions",
		Description: "Listar sessoes de conversa recentes",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"count": {Type: jsonschema.String, Description: "Numero de sessoes (default: 10)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			sessions, err := mgr.ListSessions()
			if err != nil {
				return "", err
			}
			if len(sessions) == 0 {
				return "Nenhuma sessao encontrada.", nil
			}

			count := 10
			if c := args["count"]; c != "" {
				fmt.Sscanf(c, "%d", &count)
			}
			if count > len(sessions) {
				count = len(sessions)
			}

			var sb strings.Builder
			for _, s := range sessions[:count] {
				fmt.Fprintf(&sb, "- **%s** [%s] %s (%d msgs, %s)\n",
					s.ID, s.Channel, s.Title, s.Messages,
					s.UpdatedAt.Format("2006-01-02 15:04"))
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "session_info",
		Description: "Mostrar detalhes de uma sessao",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id": {Type: jsonschema.String, Description: "ID da sessao"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			sessions, err := mgr.ListSessions()
			if err != nil {
				return "", err
			}
			for _, s := range sessions {
				if s.ID == args["id"] {
					data, _ := json.MarshalIndent(s, "", "  ")
					return string(data), nil
				}
			}
			return "", fmt.Errorf("sessao %s nao encontrada", args["id"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "export_session",
		Description: "Exportar uma sessao para arquivo markdown",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id":   {Type: jsonschema.String, Description: "ID da sessao"},
				"path": {Type: jsonschema.String, Description: "Caminho do arquivo (default: session_<id>.md)"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			if path == "" {
				path = fmt.Sprintf("session_%s.md", args["id"])
			}
			// Read the session data file
			sessionFile := filepath.Join(mgr.dataDir, "sessions", args["id"]+".json")
			data, err := os.ReadFile(sessionFile)
			if err != nil {
				return "", fmt.Errorf("sessao nao encontrada: %w", err)
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Sessao exportada para %s", path), nil
		},
	})
}
