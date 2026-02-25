package auth

import (
	"strconv"

	"github.com/costa/polypod/internal/config"
)

// Authorizer checks if a user is allowed on a given channel.
type Authorizer struct {
	telegramIDs    map[int64]bool
	whatsappNumbers map[string]bool
	restAPIKeys    map[string]bool
}

// New creates an authorizer from config.
func New(cfg *config.Config) *Authorizer {
	a := &Authorizer{
		telegramIDs:    make(map[int64]bool),
		whatsappNumbers: make(map[string]bool),
		restAPIKeys:    make(map[string]bool),
	}

	for _, id := range cfg.Auth.TelegramAllowedIDs {
		a.telegramIDs[id] = true
	}
	for _, n := range cfg.Auth.WhatsAppAllowedNos {
		a.whatsappNumbers[n] = true
	}
	for _, k := range cfg.REST.APIKeys {
		if k != "" {
			a.restAPIKeys[k] = true
		}
	}

	return a
}

// IsAllowed checks if a user is authorized for the given channel.
// Empty allowlists mean all users are allowed (open access).
func (a *Authorizer) IsAllowed(channel, userID string) bool {
	switch channel {
	case "telegram":
		if len(a.telegramIDs) == 0 {
			return true
		}
		id, err := strconv.ParseInt(userID, 10, 64)
		if err != nil {
			return false
		}
		return a.telegramIDs[id]
	case "whatsapp":
		if len(a.whatsappNumbers) == 0 {
			return true
		}
		return a.whatsappNumbers[userID]
	case "rest":
		// REST auth is handled by API key middleware
		return true
	case "cli":
		// CLI is always allowed (local access)
		return true
	default:
		return false
	}
}

// ValidAPIKey checks if the given API key is valid.
func (a *Authorizer) ValidAPIKey(key string) bool {
	if len(a.restAPIKeys) == 0 {
		return true
	}
	return a.restAPIKeys[key]
}
