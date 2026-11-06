package config

import (
	"fmt"
	"strings"
	"unicode"

	"go.mau.fi/whatsmeow/types"
)

// Validate rejects malformed allowlists before an enabled gateway starts.
// Matching is exact against the JIDs emitted by whatsmeow.
func (c WhatsAppConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.SessionDir) == "" {
		return fmt.Errorf("whatsapp.session_dir obrigatorio")
	}
	if len(c.AllowedChats) == 0 || len(c.AllowedSenders) == 0 {
		return fmt.Errorf("whatsapp.allowed_chats e allowed_senders obrigatorios")
	}
	for i, value := range c.AllowedChats {
		if !validWhatsAppJID(value, true) {
			return fmt.Errorf("whatsapp.allowed_chats[%d] deve ser um JID exato de usuario ou grupo", i)
		}
	}
	for i, value := range c.AllowedSenders {
		if !validWhatsAppJID(value, false) {
			return fmt.Errorf("whatsapp.allowed_senders[%d] deve ser um JID exato de usuario", i)
		}
	}
	if c.OwnerChat != "" {
		if !validWhatsAppJID(c.OwnerChat, false) {
			return fmt.Errorf("whatsapp.owner_chat deve ser um JID exato de usuario")
		}
		allowed := false
		for _, chat := range c.AllowedChats {
			if chat == c.OwnerChat {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("whatsapp.owner_chat deve constar em whatsapp.allowed_chats")
		}
	}
	return nil
}

func validWhatsAppJID(value string, allowGroup bool) bool {
	if value == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.Count(value, "@") != 1 {
		return false
	}
	jid, err := types.ParseJID(value)
	if err != nil || jid.IsEmpty() || jid.String() != value {
		return false
	}
	if jid.Server == types.GroupServer && allowGroup {
		if jid.RawAgent != 0 || jid.Device != 0 || jid.Integrator != 0 {
			return false
		}
		parts := strings.Split(jid.User, "-")
		return len(parts) == 1 && decimalDigits(parts[0]) || len(parts) == 2 && decimalDigits(parts[0]) && decimalDigits(parts[1])
	}
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.LegacyUserServer, types.HostedServer, types.HostedLIDServer:
		return decimalDigits(jid.User)
	default:
		return false
	}
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
