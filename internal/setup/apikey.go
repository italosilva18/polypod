package setup

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/config"
)

// validateKey sends a minimal chat completion request to verify the key.
// Returns true if the key is accepted (no 401/403).
func validateKey(baseURL, apiKey, model string) bool {
	if baseURL == "" || apiKey == "" {
		return false
	}
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	if model == "" {
		model = "deepseek-chat"
	}
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden
}

// needsAPIKey returns true when the interactive prompt should be shown:
// key is empty, or key fails validation against the configured API.
func needsAPIKey(cfg *config.Config) bool {
	if cfg.AI.APIKey == "" {
		return true
	}
	if cfg.AI.BaseURL == "" {
		return false // can't validate without a URL, assume it's fine
	}
	fmt.Printf("%sValidando API key...%s ", cW, cR)
	if validateKey(cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.Model) {
		fmt.Println(cW + "OK" + cR)
		return false
	}
	fmt.Println(cW + "falhou (401)" + cR)
	return true
}

// CheckAPIKey verifies that the config has a working API key. If the key
// is missing or returns 401, it interactively asks the user to pick a
// provider and enter a valid key.
func CheckAPIKey(cfg *config.Config, configPath string) error {
	if !needsAPIKey(cfg) {
		return nil
	}

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	if cfg.AI.APIKey == "" {
		fmt.Println(cB + cW + "Nenhuma API key configurada." + cR)
	} else {
		fmt.Println(cB + cW + "API key atual e invalida ou expirada." + cR)
	}
	fmt.Println()

	// Provider selection
	choices := []string{
		"DeepSeek (recomendado)",
		"Kimi (Moonshot)",
		"OpenAI (GPT)",
		"Claude (via OpenRouter)",
		"Outro (compativel OpenAI)",
	}
	provIdx := askChoice(scanner, "Qual provedor de IA?", choices, 1)

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

	key := askSecret(scanner, "Cole sua API key:")
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("API key nao pode ser vazia")
	}
	cfg.AI.APIKey = key

	// Validate the new key
	fmt.Printf("%sValidando...%s ", cW, cR)
	if !validateKey(cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.Model) {
		fmt.Println(cW + "falhou" + cR)
		fmt.Println(cW + "Continuando mesmo assim (verifique sua key)." + cR)
	} else {
		fmt.Println(cW + "OK" + cR)
	}

	fmt.Println()
	if askBool(scanner, "Quer que eu salve no config?", true) {
		data, err := cfg.Marshal()
		if err != nil {
			return fmt.Errorf("erro ao serializar config: %w", err)
		}
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return fmt.Errorf("erro ao gravar %s: %w", configPath, err)
		}
		fmt.Printf("%sConfig atualizada em %s%s\n", cW, configPath, cR)
	} else {
		fmt.Println(cW + "OK, usando apenas nesta sessao." + cR)
	}

	fmt.Println()
	return nil
}
