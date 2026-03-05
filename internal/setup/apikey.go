package setup

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/costa/polypod/internal/config"
)

// CheckAPIKey verifies that the config has an API key set. If not, it
// interactively asks the user to pick a provider and enter a key.
// When configPath is provided and the user opts to save, the updated
// config is written to disk.
func CheckAPIKey(cfg *config.Config, configPath string) error {
	if cfg.AI.APIKey != "" {
		return nil
	}

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println(cB + cW + "Nenhuma API key configurada." + cR)
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
