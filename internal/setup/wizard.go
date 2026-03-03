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

type provider struct {
	Name    string
	BaseURL string
	Model   string
}

var providers = []provider{
	{Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"},
	{Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"},
}

// Run executes the interactive setup wizard and writes config to configPath.
func Run(configPath string) error {
	scanner := bufio.NewScanner(os.Stdin)
	cfg := config.DefaultConfig()

	fmt.Println("==========================================================")
	fmt.Println("  Polypod — Setup do seu agente pessoal")
	fmt.Println("==========================================================")
	fmt.Println()
	fmt.Println("Vamos configurar em poucos passos.")

	// --- Provider ---
	fmt.Println()
	fmt.Println("--- Provedor de IA ---")
	choices := []string{"DeepSeek (recomendado)", "OpenAI", "Outro (compativel OpenAI)"}
	provIdx := askChoice(scanner, "Qual provedor?", choices, 1)

	switch provIdx {
	case 1:
		cfg.AI.Provider = "deepseek"
		cfg.AI.BaseURL = providers[0].BaseURL
		cfg.AI.Model = providers[0].Model
	case 2:
		cfg.AI.Provider = "openai"
		cfg.AI.BaseURL = providers[1].BaseURL
		cfg.AI.Model = providers[1].Model
	case 3:
		cfg.AI.Provider = "custom"
		cfg.AI.BaseURL = askString(scanner, "URL base da API:", "")
		cfg.AI.Model = askString(scanner, "Nome do modelo:", "")
	}

	cfg.AI.APIKey = askSecret(scanner, "Sua API key:")

	// --- Tools ---
	fmt.Println()
	fmt.Println("--- Ferramentas ---")
	cfg.AI.Tools = askBool(scanner, "Habilitar tools (ler arquivos, executar comandos)?", true)

	// --- Channels ---
	fmt.Println()
	fmt.Println("--- Canais ---")
	fmt.Println("CLI ja vem habilitado.")
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
			fmt.Printf("  Chave gerada: %s\n", key)
		}
		cfg.REST.APIKeys = []string{key}
	}

	// --- Database ---
	fmt.Println()
	fmt.Println("--- Armazenamento ---")
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
	fmt.Println("--- Resumo ---")
	fmt.Printf("  Provedor: %s (%s)\n", cfg.AI.Provider, cfg.AI.Model)
	if cfg.AI.Tools {
		fmt.Println("  Tools: habilitadas")
	} else {
		fmt.Println("  Tools: desabilitadas")
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
	fmt.Printf("  Canais: %s\n", strings.Join(canais, ", "))

	if cfg.Database.Enabled && cfg.Database.Driver == "sqlite" {
		fmt.Printf("  Banco: SQLite (%s)\n", cfg.Database.Path)
	} else if cfg.Database.Enabled {
		fmt.Printf("  Banco: PostgreSQL %s@%s:%d/%s\n", cfg.Database.User, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
	} else {
		fmt.Println("  Banco: desabilitado (JSON local)")
	}
	fmt.Println()

	if !askBool(scanner, "Salvar e iniciar?", true) {
		fmt.Println("Setup cancelado.")
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
	fmt.Printf("\nConfig salva em %s\n", configPath)

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

	fmt.Println("Iniciando Polypod...")
	return nil
}

// askString prompts for a string input with an optional default.
func askString(scanner *bufio.Scanner, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("%s ", prompt)
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
	fmt.Printf("%s ", prompt)
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
	fmt.Printf("%s (%s): ", prompt, def)
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
	fmt.Println(prompt)
	for i, opt := range options {
		fmt.Printf("  [%d] %s\n", i+1, opt)
	}
	fmt.Printf("Escolha [%d]: ", defaultIdx)
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
