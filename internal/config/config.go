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
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Enabled  bool   `yaml:"enabled"`
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
