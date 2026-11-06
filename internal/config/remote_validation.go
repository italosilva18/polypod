package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Validate checks remote authorization without making network calls.
func (c RemoteConfig) Validate() error {
	if strings.TrimSpace(c.BotToken) == "" {
		return fmt.Errorf("remote.bot_token nao configurado")
	}
	chat := strings.TrimSpace(c.ChatID)
	id, err := strconv.ParseInt(chat, 10, 64)
	if !strings.HasPrefix(chat, "@") && (err != nil || id == 0) {
		return fmt.Errorf("remote.chat_id invalido")
	}
	if (id < 0 || strings.HasPrefix(chat, "@")) && len(c.AllowedUserIDs) == 0 {
		return fmt.Errorf("remote.allowed_user_ids obrigatorio para grupos")
	}
	for _, userID := range c.AllowedUserIDs {
		if userID <= 0 {
			return fmt.Errorf("remote.allowed_user_ids deve conter IDs positivos")
		}
	}
	return nil
}
