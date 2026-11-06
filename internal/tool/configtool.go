package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/types"

	"gopkg.in/yaml.v3"
)

// ConfigStore holds runtime config overrides with concurrent-safe access.
// It reads the underlying YAML config file on demand and allows ephemeral
// (in-memory only) overrides via SetValue.
type ConfigStore struct {
	mu         sync.Mutex
	configPath string
	overrides  map[string]string
}

// NewConfigStore creates a ConfigStore backed by the given config file path.
func NewConfigStore(configPath string) *ConfigStore {
	return &ConfigStore{
		configPath: configPath,
		overrides:  make(map[string]string),
	}
}

// globalConfigStore is the package-level singleton.
var globalConfigStore *ConfigStore
var globalConfigOnce sync.Once

// InitConfigStore initialises the global ConfigStore with a config path.
// Safe to call multiple times; only the first call takes effect.
func InitConfigStore(configPath string) {
	globalConfigOnce.Do(func() {
		globalConfigStore = NewConfigStore(configPath)
	})
}

// GetConfigStore returns the package-level singleton ConfigStore.
// If InitConfigStore was never called, a store with an empty path is created.
func GetConfigStore() *ConfigStore {
	globalConfigOnce.Do(func() {
		globalConfigStore = NewConfigStore("")
	})
	return globalConfigStore
}

// GetValue retrieves a config value using dot-notation (e.g. "ai.model").
// Runtime overrides take precedence over the on-disk config file.
func (cs *ConfigStore) GetValue(key string) (string, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Check overrides first.
	if v, ok := cs.overrides[key]; ok {
		return v, nil
	}

	// Fall back to the YAML file.
	if cs.configPath == "" {
		return "", fmt.Errorf("chave %q nao encontrada (sem arquivo de configuracao)", key)
	}

	data, err := os.ReadFile(cs.configPath)
	if err != nil {
		return "", fmt.Errorf("erro ao ler config %s: %w", cs.configPath, err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("erro ao parsear config: %w", err)
	}

	val, err := lookupDot(raw, key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", val), nil
}

// SetValue sets a runtime override for key. The override is kept in memory
// only and is NOT persisted to disk.
func (cs *ConfigStore) SetValue(key, value string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if key == "" {
		return fmt.Errorf("chave nao pode ser vazia")
	}
	cs.overrides[key] = value
	return nil
}

// ListOverrides returns a copy of all current runtime overrides.
func (cs *ConfigStore) ListOverrides() map[string]string {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	out := make(map[string]string, len(cs.overrides))
	for k, v := range cs.overrides {
		out[k] = v
	}
	return out
}

// ResetOverrides clears all runtime overrides.
func (cs *ConfigStore) ResetOverrides() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.overrides = make(map[string]string)
}

// lookupDot traverses a nested map using dot-separated keys.
func lookupDot(m map[string]any, key string) (any, error) {
	parts := strings.Split(key, ".")
	var current any = m
	for _, p := range parts {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("chave %q nao encontrada", key)
		}
		current, ok = cm[p]
		if !ok {
			return nil, fmt.Errorf("chave %q nao encontrada", key)
		}
	}
	return current, nil
}

// ---------------------------------------------------------------------------
// Tool constructors
// ---------------------------------------------------------------------------

// ConfigGetTool reads a config value by dot-notation key.
var ConfigGetTool = types.BuildTool(types.ToolDef{
	NameFn:        func() string { return "config_get" },
	DescriptionFn: func() string { return "Le um valor de configuracao pela chave (dot-notation, ex: ai.model)" },
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Chave de configuracao em dot-notation (ex: ai.model, server.port)",
				},
			},
			"required": []string{"key"},
		}
	},
	IsReadOnlyFn:    func(map[string]any) bool { return true },
	IsConcurrencyFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		key, _ := args["key"].(string)
		if key == "" {
			return types.ToolResult{Content: "key e obrigatorio", IsError: true}, nil
		}

		val, err := GetConfigStore().GetValue(key)
		if err != nil {
			return types.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return types.ToolResult{Content: val}, nil
	},
})

// ConfigSetTool sets a runtime config override (in-memory only).
var ConfigSetTool = types.BuildTool(types.ToolDef{
	NameFn: func() string { return "config_set" },
	DescriptionFn: func() string {
		return "Define um override de configuracao em tempo de execucao (somente em memoria, nao persiste em disco)"
	},
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Chave de configuracao em dot-notation (ex: ai.model)",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "Valor a ser definido",
				},
			},
			"required": []string{"key", "value"},
		}
	},
	IsDestructiveFn: func(map[string]any) bool { return false },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		if key == "" {
			return types.ToolResult{Content: "key e obrigatorio", IsError: true}, nil
		}
		if value == "" {
			return types.ToolResult{Content: "value e obrigatorio", IsError: true}, nil
		}

		if err := GetConfigStore().SetValue(key, value); err != nil {
			return types.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return types.ToolResult{Content: fmt.Sprintf("override definido: %s = %s", key, value)}, nil
	},
})

// ConfigListTool returns all current runtime overrides as JSON.
var ConfigListTool = types.BuildTool(types.ToolDef{
	NameFn:        func() string { return "config_list" },
	DescriptionFn: func() string { return "Lista todos os overrides de configuracao em tempo de execucao como JSON" },
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	},
	IsReadOnlyFn:    func(map[string]any) bool { return true },
	IsConcurrencyFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		overrides := GetConfigStore().ListOverrides()

		data, err := json.Marshal(overrides)
		if err != nil {
			return types.ToolResult{Content: fmt.Sprintf("erro ao serializar: %v", err), IsError: true}, nil
		}
		return types.ToolResult{Content: string(data)}, nil
	},
})

// NewConfigTools returns all three config tool instances.
func NewConfigTools() []types.Tool {
	return []types.Tool{
		ConfigGetTool,
		ConfigSetTool,
		ConfigListTool,
	}
}
