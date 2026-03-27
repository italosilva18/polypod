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
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	AI        AIConfig        `yaml:"ai"`
	Data      DataConfig      `yaml:"data"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	CLI       CLIConfig       `yaml:"cli"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	WhatsApp  WhatsAppConfig  `yaml:"whatsapp"`
	REST      RESTConfig      `yaml:"rest"`
	Auth      AuthConfig      `yaml:"auth"`
	Rate      RateConfig      `yaml:"rate"`
	Log       LogConfig       `yaml:"log"`
	// New modules
	Providers []ProviderConfig `yaml:"providers"`
	MCP       []MCPServerConf  `yaml:"mcp"`
	Sandbox   SandboxConfig    `yaml:"sandbox"`
	Scheduler SchedulerConfig  `yaml:"scheduler"`
	Notify    NotifyConfig     `yaml:"notify"`
	Plugins   PluginsConfig    `yaml:"plugins"`
	Templates TemplatesConfig  `yaml:"templates"`
	Hooks     []HookConfig     `yaml:"hooks"`
	WebUI     WebUIConfig      `yaml:"webui"`
}

type HookConfig struct {
	Name    string `yaml:"name"`
	Event   string `yaml:"event"`
	Type    string `yaml:"type"`    // "shell" or "http"
	Command string `yaml:"command"`
	URL     string `yaml:"url"`
	Matcher string `yaml:"matcher"`
	Timeout int    `yaml:"timeout"`
	Enabled bool   `yaml:"enabled"`
}

type WebUIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
}

type ProviderConfig struct {
	Name    string `yaml:"name"`    // "openai", "ollama", "anthropic", "google"
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

type NotifyConfig struct {
	Webhook string `yaml:"webhook"` // webhook URL for notifications
}

type PluginsConfig struct {
	Dir string `yaml:"dir"` // plugins directory
}

type TemplatesConfig struct {
	Dir string `yaml:"dir"` // templates directory
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Driver   string `yaml:"driver"`   // "sqlite" | "postgres" (default: "postgres" for backwards compat)
	Path     string `yaml:"path"`     // SQLite file path (only used when driver=sqlite)
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"`
	MaxConns int    `yaml:"max_conns"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.Name, d.SSLMode)
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

type TelegramConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`
}

type WhatsAppConfig struct {
	Enabled    bool   `yaml:"enabled"`
	APIURL     string `yaml:"api_url"`
	MediaURL   string `yaml:"media_url"`
	IDInstance string `yaml:"id_instance"`
	APIToken   string `yaml:"api_token"`
}

type DataConfig struct {
	Dir       string `yaml:"dir"`
	AgentsDir string `yaml:"agents_dir"`
}

type CLIConfig struct {
	Enabled bool `yaml:"enabled"`
}

type RESTConfig struct {
	Enabled bool     `yaml:"enabled"`
	APIKeys []string `yaml:"api_keys"`
}

type AuthConfig struct {
	TelegramAllowedIDs []int64  `yaml:"telegram_allowed_ids"`
	WhatsAppAllowedNos []string `yaml:"whatsapp_allowed_numbers"`
}

type RateConfig struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
	BurstSize         int `yaml:"burst_size"`
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
	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Database.Enabled && cfg.Database.Driver == "" {
		cfg.Database.Driver = "postgres"
	}
	if cfg.Database.Driver == "sqlite" && cfg.Database.Path == "" {
		cfg.Database.Path = "data/polypod.db"
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Database.MaxConns == 0 {
		cfg.Database.MaxConns = 10
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
	if cfg.Rate.RequestsPerMinute == 0 {
		cfg.Rate.RequestsPerMinute = 20
	}
	if cfg.Rate.BurstSize == 0 {
		cfg.Rate.BurstSize = 5
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
	if cfg.Plugins.Dir == "" {
		cfg.Plugins.Dir = "data/plugins"
	}
	if cfg.Templates.Dir == "" {
		cfg.Templates.Dir = "templates"
	}
	if cfg.WebUI.Host == "" {
		cfg.WebUI.Host = "127.0.0.1"
	}
	if cfg.WebUI.Port == 0 {
		cfg.WebUI.Port = 8090
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
