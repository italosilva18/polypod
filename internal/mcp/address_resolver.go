package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/costa/polypod/internal/peeraddress"
)

// ServerEndpoint describes a parsed MCP server address.
type ServerEndpoint struct {
	Raw     string           // original address string
	Scheme  string           // "stdio", "http", "https", "sse", "tcp"
	Addr    peeraddress.Addr // parsed host/port/IP
	Path    string           // URL path if scheme is http/https/sse
	Command string           // command if scheme is stdio
	Args    []string         // args if scheme is stdio
}

// ParseMCPAddress parses an MCP server endpoint string.
// Supports:
//   - stdio:command arg1 arg2
//   - http://host:port/path
//   - https://host:port/path
//   - sse://host:port/path
//   - tcp://host:port
//   - bare host:port (treated as tcp)
func ParseMCPAddress(raw string) (*ServerEndpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty address")
	}

	ep := &ServerEndpoint{Raw: raw}

	// stdio: special handling
	if strings.HasPrefix(raw, "stdio:") {
		ep.Scheme = "stdio"
		cmd := strings.TrimPrefix(raw, "stdio:")
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			return nil, fmt.Errorf("stdio: requires a command")
		}
		ep.Command = parts[0]
		if len(parts) > 1 {
			ep.Args = parts[1:]
		}
		return ep, nil
	}

	// URL-style addresses
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid url: %w", err)
		}
		ep.Scheme = u.Scheme
		ep.Path = u.Path

		addr, err := peeraddress.Parse(u.Host)
		if err != nil {
			return nil, fmt.Errorf("parse host: %w", err)
		}
		ep.Addr = addr
		return ep, nil
	}

	// Bare host:port (default tcp)
	ep.Scheme = "tcp"
	addr, err := peeraddress.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse address: %w", err)
	}
	ep.Addr = addr
	return ep, nil
}

// Resolve performs DNS resolution and returns all resolved addresses.
// Only applies to network-based schemes (http/https/sse/tcp), not stdio.
func (ep *ServerEndpoint) Resolve(ctx context.Context) ([]peeraddress.Addr, error) {
	if ep.Scheme == "stdio" {
		return nil, fmt.Errorf("stdio endpoint has no network address")
	}

	if ep.Addr.IsResolved() {
		return []peeraddress.Addr{ep.Addr}, nil
	}

	return peeraddress.Resolve(ctx, ep.Addr.Host)
}

// ConnectString returns a string suitable for passing to Dial().
func (ep *ServerEndpoint) ConnectString() string {
	if ep.Scheme == "stdio" {
		cmd := ep.Command
		for _, arg := range ep.Args {
			cmd += " " + arg
		}
		return "stdio:" + cmd
	}

	host := ep.Addr.String()
	if ep.Path != "" {
		return ep.Scheme + "://" + host + ep.Path
	}
	return ep.Scheme + "://" + host
}

// IsNetwork returns true if the endpoint uses a network connection.
func (ep *ServerEndpoint) IsNetwork() bool {
	return ep.Scheme != "stdio"
}

// IsSecure returns true if the endpoint uses an encrypted protocol.
func (ep *ServerEndpoint) IsSecure() bool {
	return ep.Scheme == "https"
}

// UsesHTTP returns true if the endpoint uses HTTP/HTTPS.
func (ep *ServerEndpoint) UsesHTTP() bool {
	return ep.Scheme == "http" || ep.Scheme == "https"
}

// UsesStdio returns true if the endpoint uses stdio transport.
func (ep *ServerEndpoint) UsesStdio() bool {
	return ep.Scheme == "stdio"
}

// DisplayName returns a user-friendly label for the endpoint.
func (ep *ServerEndpoint) DisplayName() string {
	switch ep.Scheme {
	case "stdio":
		return "stdio:" + ep.Command
	case "http", "https", "sse":
		return ep.Scheme + "://" + ep.Addr.Host + ep.Path
	case "tcp":
		return ep.Addr.String()
	}
	return ep.Raw
}
