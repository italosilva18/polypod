package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database  DatabaseConfig  `yaml:"database"`
	AI        AIConfig        `yaml:"ai"`
	Data      DataConfig      `yaml:"data"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	CLI       CLIConfig       `yaml:"cli"`
	Log       LogConfig       `yaml:"log"`
	// New modules
	Providers   []ProviderConfig   `yaml:"providers"`
	MCP         []MCPServerConf    `yaml:"mcp"`
	Sandbox     SandboxConfig      `yaml:"sandbox"`
	Scheduler   SchedulerConfig    `yaml:"scheduler"`
	Templates   TemplatesConfig    `yaml:"templates"`
	Hooks       []HookConfig       `yaml:"hooks"`
	Permissions *PermissionsConfig `yaml:"permissions,omitempty"`
}

type HookConfig struct {
	Name    string `yaml:"name"`
	Event   string `yaml:"event"`
	Type    string `yaml:"type"` // "shell" or "http"
	Command string `yaml:"command"`
	URL     string `yaml:"url"`
	Matcher string `yaml:"matcher"`
	Timeout int    `yaml:"timeout"`
	Enabled bool   `yaml:"enabled"`
}

type ProviderConfig struct {
	Name    string `yaml:"name"` // "openai", "ollama", "anthropic", "google"
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Default bool   `yaml:"default"` // use as default provider
}

type MCPServerConf struct {
	Name      string            `yaml:"name"`
	Transport string            `yaml:"transport"` // "stdio" or "sse"
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	URL       string            `yaml:"url"`
	Env       map[string]string `yaml:"env"`
	AutoStart bool              `yaml:"auto_start"`
}

type SandboxConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Image       string `yaml:"image"`
	MemoryLimit string `yaml:"memory_limit"`
	CPULimit    string `yaml:"cpu_limit"`
	Timeout     int    `yaml:"timeout"`
	Network     bool   `yaml:"network"`
}

type SchedulerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	DataFile string `yaml:"data_file"`
}

type TemplatesConfig struct {
	Dir string `yaml:"dir"` // templates directory
}

type DatabaseConfig struct {
	Enabled bool   `yaml:"enabled"`
	Driver  string `yaml:"driver"` // "sqlite" (only supported driver in CLI build)
	Path    string `yaml:"path"`   // SQLite file path
}

type AIConfig struct {
	Provider string  `yaml:"provider"`
	BaseURL  string  `yaml:"base_url"`
	APIKey   string  `yaml:"api_key"`
	Model    string  `yaml:"model"`
	MaxToks  int     `yaml:"max_tokens"`
	Temp     float32 `yaml:"temperature"`
	Tools    bool    `yaml:"tools"`
}

type EmbeddingConfig struct {
	Enabled bool   `yaml:"enabled"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

type DataConfig struct {
	Dir       string `yaml:"dir"`
	AgentsDir string `yaml:"agents_dir"`
}

type CLIConfig struct {
	Enabled bool `yaml:"enabled"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

var envRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	expanded := envRegex.ReplaceAllStringFunc(string(data), func(match string) string {
		parts := strings.SplitN(match[2:len(match)-1], ":", 2)
		key := parts[0]
		val := os.Getenv(key)
		if val == "" && len(parts) == 2 {
			val = parts[1]
		}
		return val
	})

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	setDefaults(cfg)
	if err := cfg.Permissions.Validate(); err != nil {
		return nil, fmt.Errorf("validating permissions: %w", err)
	}
	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Permissions == nil {
		cfg.Permissions = &PermissionsConfig{
			Mode:        "bypassPermissions",
			DefaultMode: "bypassPermissions",
		}
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "data/polypod.db"
	}
	if cfg.AI.Model == "" {
		cfg.AI.Model = "deepseek-chat"
	}
	if cfg.AI.MaxToks == 0 {
		cfg.AI.MaxToks = 2048
	}
	if cfg.AI.Temp == 0 {
		cfg.AI.Temp = 0.3
	}
	if cfg.Data.Dir == "" {
		cfg.Data.Dir = "data/conversations"
	}
	if cfg.Data.AgentsDir == "" {
		cfg.Data.AgentsDir = "agents"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	// New module defaults
	if cfg.Sandbox.Image == "" {
		cfg.Sandbox.Image = "ubuntu:22.04"
	}
	if cfg.Sandbox.MemoryLimit == "" {
		cfg.Sandbox.MemoryLimit = "256m"
	}
	if cfg.Sandbox.CPULimit == "" {
		cfg.Sandbox.CPULimit = "1"
	}
	if cfg.Sandbox.Timeout == 0 {
		cfg.Sandbox.Timeout = 30
	}
	if cfg.Scheduler.DataFile == "" {
		cfg.Scheduler.DataFile = "data/scheduler.json"
	}
	if cfg.Templates.Dir == "" {
		cfg.Templates.Dir = "templates"
	}
	for i := range cfg.Hooks {
		if cfg.Hooks[i].Timeout == 0 {
			cfg.Hooks[i].Timeout = 10
		}
	}
}

// DefaultConfig returns a Config with sensible defaults, CLI enabled and tools enabled.
func DefaultConfig() *Config {
	cfg := &Config{}
	cfg.CLI.Enabled = true
	cfg.AI.Tools = true
	cfg.AI.Provider = "deepseek"
	cfg.AI.BaseURL = "https://api.deepseek.com/v1"
	setDefaults(cfg)
	return cfg
}

// Marshal serializes the Config to YAML bytes.
func (c *Config) Marshal() ([]byte, error) {
	return yaml.Marshal(c)
}

func MustParseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
