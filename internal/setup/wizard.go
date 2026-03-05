package setup

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/costa/polypod/internal/config"
)

// ANSI color codes for terminal output (white on dark backgrounds).
const (
	cW = "\033[97m" // bright white
	cB = "\033[1m"  // bold
	cR = "\033[0m"  // reset
)

type provider struct {
	Name    string
	BaseURL string
	Model   string
}

var providers = []provider{
	{Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"},
	{Name: "Kimi", BaseURL: "https://api.moonshot.cn/v1", Model: "moonshot-v1-8k"},
	{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"},
	{Name: "Claude", BaseURL: "https://openrouter.ai/api/v1", Model: "anthropic/claude-sonnet-4-20250514"},
}

// Run executes the interactive setup wizard and writes config to configPath.
func Run(configPath string) error {
	scanner := bufio.NewScanner(os.Stdin)
	cfg := config.DefaultConfig()

	fmt.Println(cB + cW + "==========================================================" + cR)
	fmt.Println(cB + cW + "  Polypod — Setup do seu agente pessoal" + cR)
	fmt.Println(cB + cW + "==========================================================" + cR)
	fmt.Println()
	fmt.Println(cW + "Vamos configurar em poucos passos." + cR)

	// --- Provider ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Provedor de IA ---" + cR)
	choices := []string{"DeepSeek (recomendado)", "Kimi (Moonshot)", "OpenAI (GPT)", "Claude (via OpenRouter)", "Outro (compativel OpenAI)"}
	provIdx := askChoice(scanner, "Qual provedor?", choices, 1)

	switch provIdx {
	case 1:
		cfg.AI.Provider = "deepseek"
		cfg.AI.BaseURL = providers[0].BaseURL
		cfg.AI.Model = providers[0].Model
	case 2:
		cfg.AI.Provider = "kimi"
		cfg.AI.BaseURL = providers[1].BaseURL
		cfg.AI.Model = providers[1].Model
	case 3:
		cfg.AI.Provider = "openai"
		cfg.AI.BaseURL = providers[2].BaseURL
		cfg.AI.Model = providers[2].Model
	case 4:
		cfg.AI.Provider = "claude"
		cfg.AI.BaseURL = providers[3].BaseURL
		cfg.AI.Model = providers[3].Model
	case 5:
		cfg.AI.Provider = "custom"
		cfg.AI.BaseURL = askString(scanner, "URL base da API:", "")
		cfg.AI.Model = askString(scanner, "Nome do modelo:", "")
	}

	cfg.AI.APIKey = askSecret(scanner, "Sua API key:")

	// --- Tools ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Ferramentas ---" + cR)
	cfg.AI.Tools = askBool(scanner, "Habilitar tools (ler arquivos, executar comandos)?", true)

	// --- Channels ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Canais ---" + cR)
	fmt.Println(cW + "CLI ja vem habilitado." + cR)
	fmt.Println()

	// Telegram
	if askBool(scanner, "Habilitar Telegram?", false) {
		cfg.Telegram.Enabled = true
		cfg.Telegram.Token = askSecret(scanner, "Token do bot:")
	}

	// WhatsApp
	if askBool(scanner, "Habilitar WhatsApp (GREEN-API)?", false) {
		cfg.WhatsApp.Enabled = true
		cfg.WhatsApp.IDInstance = askString(scanner, "ID da instancia:", "")
		cfg.WhatsApp.APIToken = askSecret(scanner, "Token da API:")
	}

	// REST
	if askBool(scanner, "Habilitar API REST?", false) {
		cfg.REST.Enabled = true
		key := askString(scanner, "API key (Enter = gerar):", "")
		if key == "" {
			key = generateAPIKey()
			fmt.Printf("%s  Chave gerada: %s%s\n", cW, key, cR)
		}
		cfg.REST.APIKeys = []string{key}
	}

	// --- Database ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Armazenamento ---" + cR)
	storageChoices := []string{"SQLite local (recomendado)", "PostgreSQL", "Nenhum (JSON local)"}
	storageIdx := askChoice(scanner, "Onde guardar conversas e dados?", storageChoices, 1)

	switch storageIdx {
	case 1: // SQLite
		cfg.Database.Enabled = true
		cfg.Database.Driver = "sqlite"
		cfg.Database.Path = askString(scanner, "Caminho do banco SQLite:", "data/polypod.db")
	case 2: // PostgreSQL
		cfg.Database.Enabled = true
		cfg.Database.Driver = "postgres"
		cfg.Database.Host = askString(scanner, "Host:", "localhost")
		portStr := askString(scanner, "Porta:", "5432")
		cfg.Database.Port, _ = strconv.Atoi(portStr)
		if cfg.Database.Port == 0 {
			cfg.Database.Port = 5432
		}
		cfg.Database.User = askString(scanner, "Usuario:", "polypod")
		cfg.Database.Password = askSecret(scanner, "Senha:")
		cfg.Database.Name = askString(scanner, "Nome do banco:", "polypod")
	case 3: // None
		cfg.Database.Enabled = false
	}

	// --- Summary ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Resumo ---" + cR)
	fmt.Printf("%s  Provedor: %s (%s)%s\n", cW, cfg.AI.Provider, cfg.AI.Model, cR)
	if cfg.AI.Tools {
		fmt.Println(cW + "  Tools: habilitadas" + cR)
	} else {
		fmt.Println(cW + "  Tools: desabilitadas" + cR)
	}

	var canais []string
	canais = append(canais, "CLI")
	if cfg.Telegram.Enabled {
		canais = append(canais, "Telegram")
	}
	if cfg.WhatsApp.Enabled {
		canais = append(canais, "WhatsApp")
	}
	if cfg.REST.Enabled {
		canais = append(canais, "REST")
	}
	fmt.Printf("%s  Canais: %s%s\n", cW, strings.Join(canais, ", "), cR)

	if cfg.Database.Enabled && cfg.Database.Driver == "sqlite" {
		fmt.Printf("%s  Banco: SQLite (%s)%s\n", cW, cfg.Database.Path, cR)
	} else if cfg.Database.Enabled {
		fmt.Printf("%s  Banco: PostgreSQL %s@%s:%d/%s%s\n", cW, cfg.Database.User, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cR)
	} else {
		fmt.Println(cW + "  Banco: desabilitado (JSON local)" + cR)
	}
	fmt.Println()

	if !askBool(scanner, "Salvar e iniciar?", true) {
		fmt.Println(cW + "Setup cancelado." + cR)
		return fmt.Errorf("setup cancelado pelo usuario")
	}

	// Write config
	data, err := cfg.Marshal()
	if err != nil {
		return fmt.Errorf("erro ao serializar config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("erro ao gravar %s: %w", configPath, err)
	}
	fmt.Printf("\n%sConfig salva em %s%s\n", cW, configPath, cR)

	// Create directories
	for _, dir := range []string{cfg.Data.Dir, cfg.Data.AgentsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("erro ao criar diretorio %s: %w", dir, err)
		}
	}

	// Copy default agent if not present
	agentPath := filepath.Join(cfg.Data.AgentsDir, "default.yaml")
	if _, err := os.Stat(agentPath); os.IsNotExist(err) {
		defaultAgent := `name: default
description: Assistente padrao com acesso total ao sistema
persona: |
  Voce e um assistente inteligente com acesso total ao sistema local. Siga estas regras:

  1. Voce tem ferramentas para acessar o sistema: ler arquivos, listar diretorios, executar comandos e buscar arquivos.
  2. Quando o usuario pedir para ler um arquivo, listar pastas, executar comandos ou buscar arquivos, USE as ferramentas disponiveis.
  3. Seja conciso e direto nas respostas.
  4. Responda no mesmo idioma da pergunta do usuario.
  5. Se tiver contexto da base de conhecimento, use-o tambem.
  6. Ao mostrar conteudo de arquivos, formate de forma legivel.
skills:
  - read_file
  - list_directory
  - run_command
  - search_files
`
		if err := os.WriteFile(agentPath, []byte(defaultAgent), 0644); err != nil {
			return fmt.Errorf("erro ao criar agente padrao: %w", err)
		}
	}

	fmt.Println(cW + "Iniciando Polypod..." + cR)
	return nil
}

// askString prompts for a string input with an optional default.
func askString(scanner *bufio.Scanner, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s%s [%s]: %s", cW, prompt, defaultVal, cR)
	} else {
		fmt.Printf("%s%s %s", cW, prompt, cR)
	}
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			return text
		}
	}
	return defaultVal
}

// askSecret prompts for a secret value (shown as typed — no masking in terminal).
func askSecret(scanner *bufio.Scanner, prompt string) string {
	fmt.Printf("%s%s %s", cW, prompt, cR)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

// askBool prompts for a yes/no answer with a default.
func askBool(scanner *bufio.Scanner, prompt string, defaultVal bool) bool {
	def := "s/n"
	if defaultVal {
		def = "S/n"
	} else {
		def = "s/N"
	}
	fmt.Printf("%s%s (%s): %s", cW, prompt, def, cR)
	if scanner.Scan() {
		text := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if text == "" {
			return defaultVal
		}
		return text == "s" || text == "sim" || text == "y" || text == "yes"
	}
	return defaultVal
}

// askChoice presents numbered options and returns the 1-based selection.
func askChoice(scanner *bufio.Scanner, prompt string, options []string, defaultIdx int) int {
	fmt.Printf("%s%s%s\n", cB+cW, prompt, cR)
	for i, opt := range options {
		fmt.Printf("%s  [%d] %s%s\n", cW, i+1, opt, cR)
	}
	fmt.Printf("%sEscolha [%d]: %s", cW, defaultIdx, cR)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			return defaultIdx
		}
		if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= len(options) {
			return n
		}
	}
	return defaultIdx
}

func generateAPIKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "pk-change-me"
	}
	return "pk-" + hex.EncodeToString(b)
}
