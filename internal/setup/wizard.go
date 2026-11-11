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
	"time"

	"golang.org/x/term"

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
	{Name: "NVIDIA NIM", BaseURL: "https://integrate.api.nvidia.com/v1", Model: "nvidia/nemotron-3.5-super-vl-120b-a12b"},
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
	choices := []string{"DeepSeek", "Kimi (Moonshot)", "OpenAI (GPT)", "Claude (via OpenRouter)", "Outro (compativel OpenAI)", "ChatGPT (OAuth; sem API key, recomendado)", "NVIDIA NIM (API hospedada)"}
	provIdx := askChoice(scanner, "Qual provedor?", choices, 6)

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
	case 6:
		model := askString(scanner, "Modelo ChatGPT (consulte polypod auth chatgpt models):", "")
		if err := configureChatGPT(cfg, model); err != nil {
			return err
		}
	case 7:
		cfg.AI.Provider = "nvidia"
		cfg.AI.BaseURL = providers[4].BaseURL
		cfg.AI.Model = askString(scanner, "Modelo NVIDIA NIM (ID exibido no catalogo):", providers[4].Model)
	}

	if cfg.AI.Provider != "chatgpt" {
		defaultVariable := "POLYPOD_AI_API_KEY"
		if cfg.AI.Provider == "nvidia" {
			defaultVariable = "NVIDIA_API_KEY"
		}
		variable := askString(scanner, "Nome da variavel de ambiente da API key (nao cole a chave):", defaultVariable)
		reference, err := environmentReference(variable)
		if err != nil {
			return err
		}
		cfg.AI.APIKey = reference
		fmt.Println(cW + "Defina essa variavel no ambiente ou no arquivo .env privado (0600)." + cR)
	}

	// --- Tools ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Ferramentas ---" + cR)
	cfg.AI.Tools = askBool(scanner, "Habilitar tools (ler arquivos, executar comandos)?", true)

	// --- Optional authenticated gateway ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Canais ---" + cR)
	fmt.Println(cW + "CLI e o nucleo; Telegram opcional utiliza inbox e respostas duraveis." + cR)
	cfg.Remote.Enabled = askBool(scanner, "Habilitar gateway Telegram?", false)
	if cfg.Remote.Enabled {
		cfg.Remote.BotToken = "${POLYPOD_TELEGRAM_BOT_TOKEN:}"
		cfg.Remote.ChatID = askString(scanner, "Chat ID numerico ou @canal:", "")
		users := askString(scanner, "IDs de usuarios permitidos separados por virgula (obrigatorio para grupos):", "")
		allowed, err := parseAllowedUsers(users)
		if err != nil {
			return err
		}
		cfg.Remote.AllowedUserIDs = allowed
		if err := cfg.Remote.Validate(); err != nil {
			return err
		}
		fmt.Println(cW + "Defina POLYPOD_TELEGRAM_BOT_TOKEN no ambiente ou .env privado; inicie uma conversa com o bot." + cR)
	}
	cfg.CLI.Enabled = true

	// --- Periodic review ---
	cfg.Scheduler.HeartbeatEnabled = askBool(scanner, "Habilitar heartbeat de revisao periodica?", true)
	if cfg.Scheduler.HeartbeatEnabled {
		interval := askString(scanner, "Intervalo do heartbeat:", "1h")
		duration, err := time.ParseDuration(interval)
		if err != nil || duration <= 0 {
			return fmt.Errorf("intervalo de heartbeat deve ser uma duracao positiva")
		}
		cfg.Scheduler.Enabled, cfg.Scheduler.HeartbeatInterval = true, interval
	}

	// --- Database ---
	fmt.Println()
	fmt.Println(cB + cW + "--- Armazenamento ---" + cR)
	storageChoices := []string{"SQLite local (recomendado)", "Nenhum (JSON local)"}
	storageIdx := askChoice(scanner, "Onde guardar conversas e dados?", storageChoices, 1)

	switch storageIdx {
	case 1: // SQLite
		cfg.Database.Enabled = true
		cfg.Database.Driver = "sqlite"
		cfg.Database.Path = askString(scanner, "Caminho do banco SQLite:", "data/polypod.db")
	case 2: // None
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

	if cfg.Remote.Enabled {
		fmt.Printf("%s  Canais: CLI e gateway Telegram autenticado%s\n", cW, cR)
	} else {
		fmt.Printf("%s  Canais: CLI%s\n", cW, cR)
	}
	if cfg.Scheduler.HeartbeatEnabled {
		fmt.Printf("  Heartbeat: %s\n", cfg.Scheduler.HeartbeatInterval)
	}

	if cfg.Database.Enabled {
		fmt.Printf("%s  Banco: SQLite (%s)%s\n", cW, cfg.Database.Path, cR)
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

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("erro ao gravar %s: %w", configPath, err)
	}
	if err := os.Chmod(configPath, 0600); err != nil {
		return fmt.Errorf("protegendo config: %w", err)
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

	fmt.Println(cW + "Proximos passos:" + cR)
	if cfg.AI.Provider == "chatgpt" {
		fmt.Println("  polypod auth chatgpt login\n  polypod auth chatgpt models")
	} else if cfg.AI.Provider == "nvidia" {
		fmt.Println("  Crie a chave em https://build.nvidia.com/explore/discover e grave NVIDIA_API_KEY no .env privado (0600).")
		fmt.Println("  Confira docs/integrations/nvidia-nim.md para selecionar o modelo da sua conta.")
	}
	fmt.Println("  polypod doctor --live (OAuth, Telegram, captura, entrada, heartbeat e boot)")
	fmt.Println("  Para boot, use a mesma sessao desktop e diretorio de dados; confira systemctl is-enabled e is-active.")
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

// askSecret disables terminal echo; piped input is consumed without printing.
func askSecret(scanner *bufio.Scanner, prompt string) string {
	fmt.Printf("%s%s %s", cW, prompt, cR)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
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

func configureChatGPT(cfg *config.Config, model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("informe o modelo disponivel em polypod auth chatgpt models")
	}
	cfg.AI.Provider, cfg.AI.BaseURL, cfg.AI.Model, cfg.AI.APIKey = "chatgpt", "https://api.openai.com/v1", strings.TrimSpace(model), ""
	return nil
}

func environmentReference(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nome da variavel obrigatorio")
	}
	for index, ch := range name {
		if !(ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || index > 0 && ch >= '0' && ch <= '9') {
			return "", fmt.Errorf("informe apenas o nome da variavel, sem o valor secreto")
		}
	}
	return "${" + name + ":}", nil
}

func parseAllowedUsers(value string) ([]int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var ids []int64
	for _, part := range strings.Split(value, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("IDs permitidos devem ser numeros positivos separados por virgula")
		}
		ids = append(ids, id)
	}
	return ids, nil
}
