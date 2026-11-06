// Package schemas provides JSON Schema definitions for polypod config
// validation, JSON Schema export (for editor integration), and YAML example
// generation. It is a pure-data package with no runtime side-effects.
package schemas

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Schema definition
// ---------------------------------------------------------------------------

// Schema represents a JSON Schema node. Only the subset of keywords used by
// polypod's config is modelled.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Description string             `json:"description,omitempty"`
	Default     any                `json:"default,omitempty"`
	Enum        []any              `json:"enum,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	MinLength   int                `json:"minLength,omitempty"`
	MaxLength   int                `json:"maxLength,omitempty"`
	Pattern     string             `json:"pattern,omitempty"`
}

// ---------------------------------------------------------------------------
// Validation types
// ---------------------------------------------------------------------------

// ValidationError describes a single schema violation.
type ValidationError struct {
	Path     string // dot-separated path to the offending field
	Message  string // human-readable description of the problem
	Expected string // what the schema requires
	Got      string // what was actually found
}

func (e ValidationError) String() string {
	s := fmt.Sprintf("%s: %s", e.Path, e.Message)
	if e.Expected != "" {
		s += fmt.Sprintf(" (expected %s", e.Expected)
		if e.Got != "" {
			s += fmt.Sprintf(", got %s", e.Got)
		}
		s += ")"
	}
	return s
}

// ---------------------------------------------------------------------------
// ConfigSchema — the full JSON Schema for polypod's config.yaml
// ---------------------------------------------------------------------------

// ConfigSchema returns the root JSON Schema covering every section of
// polypod's config.yaml.
func ConfigSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Polypod configuration file (config.yaml)",
		Properties: map[string]*Schema{
			"server":    serverSchema(),
			"database":  databaseSchema(),
			"ai":        aiSchema(),
			"data":      dataSchema(),
			"embedding": embeddingSchema(),
			"cli":       cliSchema(),
			"rest":      restSchema(),
			"telegram":  telegramSchema(),
			"whatsapp":  whatsappSchema(),
			"webui":     webuiSchema(),
			"log":       logSchema(),
			"auth":      authSchema(),
			"rate":      rateSchema(),
			"notify":    notifySchema(),
			"plugins":   pluginsSchema(),
			"providers": providersSchema(),
			"mcp":       mcpSchema(),
			"sandbox":   sandboxSchema(),
			"scheduler": schedulerSchema(),
			"templates": templatesSchema(),
			"hooks":     hooksSchema(),
			// Extended config sections
			"permissions": permissionsSchema(),
			"mcpServers":  mcpServersExtSchema(),
			"outputStyle": outputStyleSchema(),
			"env":         envSchema(),
		},
	}
}

// ── section builders ──────────────────────────────────────────────────────

func serverSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "HTTP server settings",
		Properties: map[string]*Schema{
			"host": {Type: "string", Description: "Bind address", Default: "0.0.0.0"},
			"port": {Type: "integer", Description: "Listen port", Default: 8080},
		},
	}
}

func databaseSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Database configuration (SQLite or Postgres)",
		Properties: map[string]*Schema{
			"enabled":   {Type: "boolean", Description: "Enable persistent database", Default: false},
			"driver":    {Type: "string", Description: "Database driver", Enum: []any{"sqlite", "postgres"}, Default: "sqlite"},
			"path":      {Type: "string", Description: "SQLite file path", Default: "data/polypod.db"},
			"host":      {Type: "string", Description: "Postgres host"},
			"port":      {Type: "integer", Description: "Postgres port", Default: 5432},
			"user":      {Type: "string", Description: "Postgres user"},
			"password":  {Type: "string", Description: "Postgres password"},
			"name":      {Type: "string", Description: "Postgres database name"},
			"sslmode":   {Type: "string", Description: "Postgres SSL mode", Enum: []any{"disable", "require", "verify-ca", "verify-full"}, Default: "disable"},
			"max_conns": {Type: "integer", Description: "Maximum connections", Default: 10},
		},
	}
}

func aiSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Primary AI provider configuration",
		Required:    []string{"provider", "model"},
		Properties: map[string]*Schema{
			"provider":    {Type: "string", Description: "Provider name", Enum: []any{"deepseek", "openai", "anthropic", "google", "ollama", "groq", "together", "mistral", "cohere", "fireworks", "perplexity", "xai"}, Default: "deepseek"},
			"base_url":    {Type: "string", Description: "API base URL", MinLength: 1},
			"api_key":     {Type: "string", Description: "API key (supports ${ENV_VAR:default} syntax)"},
			"model":       {Type: "string", Description: "Model identifier", Default: "deepseek-chat", MinLength: 1},
			"max_tokens":  {Type: "integer", Description: "Maximum tokens per response", Default: 2048},
			"temperature": {Type: "number", Description: "Sampling temperature (0.0-2.0)", Default: 0.3},
			"tools":       {Type: "boolean", Description: "Enable tool/function calling", Default: true},
		},
	}
}

func dataSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Data directories",
		Properties: map[string]*Schema{
			"dir":        {Type: "string", Description: "Conversation data directory", Default: "data/conversations"},
			"agents_dir": {Type: "string", Description: "Agents directory", Default: "agents"},
		},
	}
}

func embeddingSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Embedding API for RAG",
		Properties: map[string]*Schema{
			"enabled":  {Type: "boolean", Description: "Enable embeddings", Default: false},
			"api_key":  {Type: "string", Description: "Embedding API key"},
			"base_url": {Type: "string", Description: "Embedding API base URL"},
		},
	}
}

func cliSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "CLI channel",
		Properties: map[string]*Schema{
			"enabled": {Type: "boolean", Description: "Enable CLI interface", Default: true},
		},
	}
}

func restSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "REST API channel",
		Properties: map[string]*Schema{
			"enabled":  {Type: "boolean", Description: "Enable REST API", Default: false},
			"api_keys": {Type: "array", Description: "Allowed API keys", Items: &Schema{Type: "string"}},
		},
	}
}

func telegramSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Telegram bot channel",
		Properties: map[string]*Schema{
			"enabled": {Type: "boolean", Description: "Enable Telegram bot", Default: false},
			"token":   {Type: "string", Description: "Bot token from @BotFather"},
		},
	}
}

func whatsappSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "WhatsApp channel (Green API)",
		Properties: map[string]*Schema{
			"enabled":     {Type: "boolean", Description: "Enable WhatsApp", Default: false},
			"api_url":     {Type: "string", Description: "Green API URL"},
			"media_url":   {Type: "string", Description: "Green API media URL"},
			"id_instance": {Type: "string", Description: "Instance ID"},
			"api_token":   {Type: "string", Description: "API token"},
		},
	}
}

func webuiSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Web UI channel",
		Properties: map[string]*Schema{
			"enabled": {Type: "boolean", Description: "Enable web interface", Default: false},
			"host":    {Type: "string", Description: "Bind address", Default: "127.0.0.1"},
			"port":    {Type: "integer", Description: "Listen port", Default: 8090},
		},
	}
}

func logSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Logging configuration",
		Properties: map[string]*Schema{
			"level":  {Type: "string", Description: "Log level", Enum: []any{"debug", "info", "warn", "error"}, Default: "info"},
			"format": {Type: "string", Description: "Log format", Enum: []any{"text", "json"}, Default: "text"},
		},
	}
}

func authSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Channel authentication",
		Properties: map[string]*Schema{
			"telegram_allowed_ids":     {Type: "array", Description: "Allowed Telegram user IDs", Items: &Schema{Type: "integer"}},
			"whatsapp_allowed_numbers": {Type: "array", Description: "Allowed WhatsApp numbers", Items: &Schema{Type: "string"}},
		},
	}
}

func rateSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Rate limiting",
		Properties: map[string]*Schema{
			"requests_per_minute": {Type: "integer", Description: "Max requests per minute", Default: 20},
			"burst_size":          {Type: "integer", Description: "Burst size", Default: 5},
		},
	}
}

func notifySchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Notification settings",
		Properties: map[string]*Schema{
			"webhook": {Type: "string", Description: "Webhook URL for notifications"},
		},
	}
}

func pluginsSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Plugin system",
		Properties: map[string]*Schema{
			"dir": {Type: "string", Description: "Plugins directory", Default: "data/plugins"},
		},
	}
}

func providersSchema() *Schema {
	return &Schema{
		Type:        "array",
		Description: "Additional AI providers",
		Items: &Schema{
			Type:     "object",
			Required: []string{"name"},
			Properties: map[string]*Schema{
				"name":     {Type: "string", Description: "Provider name", Enum: []any{"openai", "ollama", "anthropic", "google", "groq", "together", "mistral"}},
				"base_url": {Type: "string", Description: "API base URL"},
				"api_key":  {Type: "string", Description: "API key"},
				"default":  {Type: "boolean", Description: "Use as default provider", Default: false},
			},
		},
	}
}

func mcpSchema() *Schema {
	return &Schema{
		Type:        "array",
		Description: "MCP (Model Context Protocol) server connections",
		Items: &Schema{
			Type:     "object",
			Required: []string{"name", "transport"},
			Properties: map[string]*Schema{
				"name":       {Type: "string", Description: "Server name", MinLength: 1},
				"transport":  {Type: "string", Description: "Transport type", Enum: []any{"stdio", "sse", "http"}},
				"command":    {Type: "string", Description: "Command to run (stdio transport)"},
				"args":       {Type: "array", Description: "Command arguments", Items: &Schema{Type: "string"}},
				"url":        {Type: "string", Description: "Server URL (sse/http transport)"},
				"env":        {Type: "object", Description: "Environment variables for the server process"},
				"auto_start": {Type: "boolean", Description: "Auto-start on launch", Default: true},
			},
		},
	}
}

func sandboxSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Docker sandbox for code execution",
		Properties: map[string]*Schema{
			"enabled":      {Type: "boolean", Description: "Enable sandbox", Default: false},
			"image":        {Type: "string", Description: "Docker image", Default: "ubuntu:22.04"},
			"memory_limit": {Type: "string", Description: "Memory limit", Default: "256m"},
			"cpu_limit":    {Type: "string", Description: "CPU limit", Default: "1"},
			"timeout":      {Type: "integer", Description: "Execution timeout (seconds)", Default: 30},
			"network":      {Type: "boolean", Description: "Allow network access", Default: false},
		},
	}
}

func schedulerSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Task scheduler",
		Properties: map[string]*Schema{
			"enabled":   {Type: "boolean", Description: "Enable scheduler", Default: false},
			"data_file": {Type: "string", Description: "Scheduler state file", Default: "data/scheduler.json"},
		},
	}
}

func templatesSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Prompt templates",
		Properties: map[string]*Schema{
			"dir": {Type: "string", Description: "Templates directory", Default: "templates"},
		},
	}
}

func hooksSchema() *Schema {
	return &Schema{
		Type:        "array",
		Description: "Lifecycle hooks (shell or HTTP)",
		Items: &Schema{
			Type:     "object",
			Required: []string{"name", "event", "type"},
			Properties: map[string]*Schema{
				"name":    {Type: "string", Description: "Hook name", MinLength: 1},
				"event":   {Type: "string", Description: "Trigger event", Enum: []any{"SessionStart", "SessionEnd", "PreToolUse", "PostToolUse", "PreCompact", "PostCompact", "UserPrompt", "AssistantResponse", "Error", "Stop"}},
				"type":    {Type: "string", Description: "Hook type", Enum: []any{"shell", "http"}},
				"command": {Type: "string", Description: "Shell command (type=shell)"},
				"url":     {Type: "string", Description: "Webhook URL (type=http)"},
				"matcher": {Type: "string", Description: "Pattern to match (e.g. tool name)"},
				"timeout": {Type: "integer", Description: "Timeout in seconds", Default: 10},
				"enabled": {Type: "boolean", Description: "Enable this hook", Default: true},
			},
		},
	}
}

func permissionsSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Permission rules and mode",
		Properties: map[string]*Schema{
			"mode":         {Type: "string", Description: "Permission mode", Enum: []any{"default", "bypass", "plan", "dontask"}, Default: "default"},
			"default_mode": {Type: "string", Description: "Fallback mode if mode is empty"},
			"allow":        {Type: "array", Description: "Allowed permission rules", Items: &Schema{Type: "string"}},
			"deny":         {Type: "array", Description: "Denied permission rules", Items: &Schema{Type: "string"}},
		},
	}
}

func mcpServersExtSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Extended MCP server definitions (TS-compatible key-value format)",
	}
}

func outputStyleSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Output formatting preferences",
		Properties: map[string]*Schema{
			"theme":   {Type: "string", Description: "Color theme"},
			"verbose": {Type: "boolean", Description: "Verbose output", Default: false},
		},
	}
}

func envSchema() *Schema {
	return &Schema{
		Type:        "object",
		Description: "Environment variable overrides",
		Properties: map[string]*Schema{
			"env":              {Type: "object", Description: "Key-value env vars exported to subprocesses"},
			"apiKeyHelper":     {Type: "string", Description: "Command to obtain API key dynamically"},
			"disableHooks":     {Type: "boolean", Description: "Disable all hooks", Default: false},
			"onlyManagedHooks": {Type: "boolean", Description: "Only run managed hooks", Default: false},
		},
	}
}

// ---------------------------------------------------------------------------
// ValidateConfig — validates a parsed config map against the schema
// ---------------------------------------------------------------------------

// ValidateConfig walks the given map (typically from yaml.Unmarshal) against
// ConfigSchema() and returns all violations found.
func ValidateConfig(data map[string]any) []ValidationError {
	schema := ConfigSchema()
	var errs []ValidationError
	validateObject(schema, data, "", &errs)
	return errs
}

// ── recursive validation helpers ──────────────────────────────────────────

func validateObject(schema *Schema, data map[string]any, path string, errs *[]ValidationError) {
	if schema.Properties == nil {
		return
	}

	// Check required fields
	for _, req := range schema.Required {
		if _, ok := data[req]; !ok {
			*errs = append(*errs, ValidationError{
				Path:     joinPath(path, req),
				Message:  "required field is missing",
				Expected: "present",
				Got:      "absent",
			})
		}
	}

	// Validate each provided field
	for key, val := range data {
		propSchema, known := schema.Properties[key]
		if !known {
			// Unknown top-level field is a warning, not an error, so skip.
			continue
		}
		validateValue(propSchema, val, joinPath(path, key), errs)
	}
}

func validateValue(schema *Schema, val any, path string, errs *[]ValidationError) {
	if val == nil {
		return
	}

	switch schema.Type {
	case "string":
		s, ok := val.(string)
		if !ok {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  "wrong type",
				Expected: "string",
				Got:      fmt.Sprintf("%T", val),
			})
			return
		}
		if schema.MinLength > 0 && len(s) < schema.MinLength {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  fmt.Sprintf("string too short (min %d)", schema.MinLength),
				Expected: fmt.Sprintf("len >= %d", schema.MinLength),
				Got:      fmt.Sprintf("len %d", len(s)),
			})
		}
		if schema.MaxLength > 0 && len(s) > schema.MaxLength {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  fmt.Sprintf("string too long (max %d)", schema.MaxLength),
				Expected: fmt.Sprintf("len <= %d", schema.MaxLength),
				Got:      fmt.Sprintf("len %d", len(s)),
			})
		}
		if schema.Pattern != "" {
			if re, err := regexp.Compile(schema.Pattern); err == nil && !re.MatchString(s) {
				*errs = append(*errs, ValidationError{
					Path:     path,
					Message:  "does not match pattern",
					Expected: schema.Pattern,
					Got:      s,
				})
			}
		}
		if len(schema.Enum) > 0 {
			if !enumContains(schema.Enum, s) {
				*errs = append(*errs, ValidationError{
					Path:     path,
					Message:  "value not in allowed set",
					Expected: fmt.Sprintf("one of %v", schema.Enum),
					Got:      s,
				})
			}
		}

	case "integer":
		if !isNumeric(val) {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  "wrong type",
				Expected: "integer",
				Got:      fmt.Sprintf("%T", val),
			})
		}

	case "number":
		if !isNumeric(val) {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  "wrong type",
				Expected: "number",
				Got:      fmt.Sprintf("%T", val),
			})
		}

	case "boolean":
		if _, ok := val.(bool); !ok {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  "wrong type",
				Expected: "boolean",
				Got:      fmt.Sprintf("%T", val),
			})
		}

	case "object":
		m, ok := val.(map[string]any)
		if !ok {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  "wrong type",
				Expected: "object",
				Got:      fmt.Sprintf("%T", val),
			})
			return
		}
		validateObject(schema, m, path, errs)

	case "array":
		arr, ok := val.([]any)
		if !ok {
			*errs = append(*errs, ValidationError{
				Path:     path,
				Message:  "wrong type",
				Expected: "array",
				Got:      fmt.Sprintf("%T", val),
			})
			return
		}
		if schema.Items != nil {
			for i, elem := range arr {
				elemPath := fmt.Sprintf("%s[%d]", path, i)
				validateValue(schema.Items, elem, elemPath, errs)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// GenerateJSONSchema — serialises the schema as JSON
// ---------------------------------------------------------------------------

// GenerateJSONSchema returns the config schema as indented JSON bytes suitable
// for editor integration (e.g. VS Code yaml-language-server).
func GenerateJSONSchema() ([]byte, error) {
	root := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://github.com/costa/polypod/config.schema.json",
		"title":       "Polypod Configuration",
		"description": "Schema for polypod config.yaml",
		"type":        "object",
		"properties":  schemaToMap(ConfigSchema()),
	}
	return json.MarshalIndent(root, "", "  ")
}

// schemaToMap converts a Schema tree into a plain map[string]any that
// json.Marshal will serialise as a valid JSON Schema document.
func schemaToMap(s *Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := make(map[string]any)
	if s.Type != "" {
		m["type"] = s.Type
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if s.Default != nil {
		m["default"] = s.Default
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.MinLength > 0 {
		m["minLength"] = s.MinLength
	}
	if s.MaxLength > 0 {
		m["maxLength"] = s.MaxLength
	}
	if s.Pattern != "" {
		m["pattern"] = s.Pattern
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Items != nil {
		m["items"] = schemaToMap(s.Items)
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToMap(v)
		}
		m["properties"] = props
	}
	return m
}

// ---------------------------------------------------------------------------
// GenerateYAMLExample — generates an example config.yaml with comments
// ---------------------------------------------------------------------------

// GenerateYAMLExample returns a fully-commented example config.yaml string
// covering every section recognised by polypod.
func GenerateYAMLExample() string {
	var b strings.Builder
	b.WriteString(`# Polypod configuration — example generated from schema
# See https://github.com/costa/polypod for documentation

# --- Server ---
server:
  host: "0.0.0.0"        # Bind address
  port: 8080              # Listen port

# --- Database (optional — without it, persists to JSON files) ---
database:
  enabled: false
  # driver: sqlite
  # path: data/polypod.db
  # --- or Postgres ---
  # driver: postgres
  # host: "localhost"
  # port: 5432
  # user: "polypod"
  # password: "CHANGE_ME"
  # name: "polypod"
  # sslmode: "disable"
  # max_conns: 10

# --- Primary AI provider ---
ai:
  provider: "deepseek"                        # deepseek, openai, anthropic, google, ollama, groq, ...
  base_url: "https://api.deepseek.com/v1"
  api_key: "${DEEPSEEK_API_KEY:sk-your-key}"  # env var substitution supported
  model: "deepseek-chat"
  max_tokens: 4096
  temperature: 0.3
  tools: true                                 # enable tool/function calling

# --- Additional providers (optional) ---
# providers:
#   - name: ollama
#     base_url: "http://localhost:11434"
#   - name: anthropic
#     api_key: "${ANTHROPIC_API_KEY}"
#   - name: google
#     api_key: "${GOOGLE_API_KEY}"

# --- Embeddings for RAG (optional) ---
embedding:
  enabled: false
  # api_key: ""
  # base_url: ""

# --- Channels ---

cli:
  enabled: true           # CLI is the primary channel

rest:
  enabled: false
  api_keys:
    - "${API_KEY:your-api-key}"

telegram:
  enabled: false
  token: "${TELEGRAM_BOT_TOKEN}"

whatsapp:
  enabled: false
  api_url: "https://api.green-api.com"
  media_url: "https://media.green-api.com"
  id_instance: "${WA_INSTANCE}"
  api_token: "${WA_TOKEN}"

webui:
  enabled: false
  host: "127.0.0.1"
  port: 8090

# --- MCP Servers ---
# mcp:
#   - name: filesystem
#     transport: stdio
#     command: npx
#     args: ["-y", "@modelcontextprotocol/server-filesystem", "/home"]
#     auto_start: true

# --- Hooks ---
# hooks:
#   - name: log-tools
#     event: PostToolUse
#     type: shell
#     command: "cat >> /tmp/polypod-tools.log"
#     enabled: true

# --- Docker Sandbox ---
sandbox:
  enabled: false
  image: "ubuntu:22.04"
  memory_limit: "256m"
  cpu_limit: "1"
  timeout: 30             # seconds
  network: false

# --- Scheduler ---
scheduler:
  enabled: false
  data_file: "data/scheduler.json"

# --- Notifications ---
notify:
  webhook: ""

# --- Plugins ---
plugins:
  dir: "data/plugins"

# --- Templates ---
templates:
  dir: "templates"

# --- Auth & Rate Limiting ---
auth:
  telegram_allowed_ids: []
  whatsapp_allowed_numbers: []

rate:
  requests_per_minute: 30
  burst_size: 10

# --- Logging ---
log:
  level: "info"           # debug, info, warn, error
  format: "text"          # text, json

# --- Data ---
data:
  dir: "data/conversations"
  agents_dir: "agents"

# --- Permissions (extended) ---
# permissions:
#   mode: "default"       # default, bypass, plan, dontask
#   allow: []
#   deny: []

# --- Environment overrides (extended) ---
# env:
#   MY_VAR: "value"
# apiKeyHelper: ""
# disableHooks: false
# onlyManagedHooks: false
`)
	return b.String()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func joinPath(base, field string) string {
	if base == "" {
		return field
	}
	return base + "." + field
}

func enumContains(haystack []any, needle string) bool {
	for _, v := range haystack {
		if fmt.Sprintf("%v", v) == needle {
			return true
		}
	}
	return false
}

func isNumeric(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	return false
}
