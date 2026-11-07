package config

import (
	"errors"
	"net"
	"strconv"
)

// WebhookConfig enables an authenticated, loopback-only HTTP ingress. A TLS
// reverse proxy may expose it remotely after applying its own access controls.
type WebhookConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Token   string `yaml:"token"`
}

// Validate rejects accidental public or unauthenticated listeners. Disabled
// configurations do not need a token, so the example can be committed safely.
func (c WebhookConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.Token) < 32 || len(c.Token) > 512 {
		return errors.New("webhook.token deve ter entre 32 e 512 caracteres ASCII visiveis")
	}
	for _, ch := range c.Token {
		if ch < 33 || ch > 126 {
			return errors.New("webhook.token deve ter entre 32 e 512 caracteres ASCII visiveis")
		}
	}
	address := c.Listen
	if address == "" {
		address = "127.0.0.1:8788"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("webhook.listen deve ser host:porta de loopback")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("webhook.listen deve usar um endereco IP de loopback")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("webhook.listen deve informar uma porta de 1 a 65535")
	}
	return nil
}
