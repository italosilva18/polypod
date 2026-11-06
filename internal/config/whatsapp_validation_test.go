package config

import (
	"strings"
	"testing"
)

func validWhatsAppConfig() WhatsAppConfig {
	return WhatsAppConfig{
		Enabled:        true,
		SessionDir:     "data/whatsapp",
		AllowedChats:   []string{"15551234567@s.whatsapp.net", "120363000000000000@g.us"},
		AllowedSenders: []string{"15551234567@s.whatsapp.net", "123456789@lid", "15551234567:25@s.whatsapp.net"},
	}
}

func TestWhatsAppConfigValidateJIDs(t *testing.T) {
	if err := validWhatsAppConfig().Validate(); err != nil {
		t.Fatalf("valid JIDs rejected: %v", err)
	}
	for _, tc := range []struct {
		name    string
		chats   []string
		senders []string
		field   string
	}{
		{name: "leading whitespace", chats: []string{" 15551234567@s.whatsapp.net"}, field: "allowed_chats[0]"},
		{name: "trailing whitespace", senders: []string{"15551234567@s.whatsapp.net "}, field: "allowed_senders[0]"},
		{name: "embedded whitespace", chats: []string{"1555 1234567@s.whatsapp.net"}, field: "allowed_chats[0]"},
		{name: "bare number", chats: []string{"15551234567"}, field: "allowed_chats[0]"},
		{name: "extra at sign", chats: []string{"15551234567@s.whatsapp.net@other"}, field: "allowed_chats[0]"},
		{name: "unknown server", chats: []string{"15551234567@example.com"}, field: "allowed_chats[0]"},
		{name: "empty user", chats: []string{"@g.us"}, field: "allowed_chats[0]"},
		{name: "nonnumeric user", chats: []string{"user@s.whatsapp.net"}, field: "allowed_chats[0]"},
		{name: "malformed group", chats: []string{"123--456@g.us"}, field: "allowed_chats[0]"},
		{name: "group sender", senders: []string{"120363000000000000@g.us"}, field: "allowed_senders[0]"},
		{name: "nonnumeric device", senders: []string{"15551234567:device@s.whatsapp.net"}, field: "allowed_senders[0]"},
		{name: "noncanonical device zero", senders: []string{"15551234567:0@s.whatsapp.net"}, field: "allowed_senders[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validWhatsAppConfig()
			if tc.chats != nil {
				cfg.AllowedChats = tc.chats
			}
			if tc.senders != nil {
				cfg.AllowedSenders = tc.senders
			}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %v; want %s error", err, tc.field)
			}
		})
	}
}

func TestWhatsAppConfigValidateRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*WhatsAppConfig)
	}{
		{name: "session directory", apply: func(c *WhatsAppConfig) { c.SessionDir = " \t" }},
		{name: "chat allowlist", apply: func(c *WhatsAppConfig) { c.AllowedChats = nil }},
		{name: "sender allowlist", apply: func(c *WhatsAppConfig) { c.AllowedSenders = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validWhatsAppConfig()
			tc.apply(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("enabled WhatsApp accepted incomplete configuration")
			}
		})
	}
	cfg := validWhatsAppConfig()
	cfg.Enabled = false
	cfg.AllowedChats = nil
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled WhatsApp should not require allowlists: %v", err)
	}
}

func TestLoadRejectsInvalidEnabledWhatsAppJID(t *testing.T) {
	path := writeConfig(t, "whatsapp:\n  enabled: true\n  session_dir: data/whatsapp\n  allowed_chats: [' 15551234567@s.whatsapp.net']\n  allowed_senders: ['15551234567@s.whatsapp.net']\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "whatsapp.allowed_chats[0]") {
		t.Fatalf("Load() = %v; want invalid WhatsApp JID error", err)
	}
}

func TestWhatsAppConfigValidateOwnerChat(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ownerChat string
		valid     bool
	}{
		{name: "optional", valid: true},
		{name: "allowlisted user", ownerChat: "15551234567@s.whatsapp.net", valid: true},
		{name: "missing from allowlist", ownerChat: "15559999999@s.whatsapp.net"},
		{name: "group", ownerChat: "120363000000000000@g.us"},
		{name: "leading whitespace", ownerChat: " 15551234567@s.whatsapp.net"},
		{name: "bare number", ownerChat: "15551234567"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validWhatsAppConfig()
			cfg.OwnerChat = tc.ownerChat
			err := cfg.Validate()
			if tc.valid && err != nil {
				t.Fatalf("valid owner chat rejected: %v", err)
			}
			if !tc.valid && (err == nil || !strings.Contains(err.Error(), "whatsapp.owner_chat")) {
				t.Fatalf("Validate() = %v; want owner chat error", err)
			}
		})
	}
}

func TestLoadParsesWhatsAppOwnerChat(t *testing.T) {
	path := writeConfig(t, "whatsapp:\n  enabled: true\n  session_dir: data/whatsapp\n  owner_chat: 15551234567@s.whatsapp.net\n  allowed_chats: ['15551234567@s.whatsapp.net']\n  allowed_senders: ['15551234567@s.whatsapp.net']\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WhatsApp.OwnerChat != "15551234567@s.whatsapp.net" {
		t.Fatalf("owner chat = %q", cfg.WhatsApp.OwnerChat)
	}
}
