package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/costa/polypod/internal/types"
	"golang.org/x/net/proxy"
)

const (
	defaultTorSOCKSAddress = "127.0.0.1:9050"
	maxTorResponseBytes    = 512 * 1024
	defaultTorPageChars    = 12_000
	maxTorPageChars        = 30_000
	torFetchTimeout        = 90 * time.Second
)

// newTorFetchTool reads onion pages through a local Tor SOCKS5 endpoint.
// The target hostname is passed to SOCKS5 unchanged; no local DNS lookup or
// direct HTTP fallback is used for the destination.
func newTorFetchTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "tor_fetch" },
		DescriptionFn: func() string {
			return "Ler uma pagina HTTP(S) .onion pelo proxy SOCKS5 local do Tor. " +
				"Exige Tor ativo; nunca usa conexao direta como alternativa."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"url":       map[string]any{"type": "string", "description": "URL http:// ou https:// terminada em .onion"},
				"start":     map[string]any{"type": "integer", "minimum": 0, "description": "Deslocamento em caracteres na resposta"},
				"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": maxTorPageChars, "description": "Caracteres a retornar, ate 30000"},
			}, "required": []string{"url"}}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if _, err := parseOnionURL(argString(args, "url")); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			if _, err := torPageRange(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			target, err := parseOnionURL(argString(args, "url"))
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			page, err := torPageRange(args)
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			proxyAddress, err := torSOCKSAddress()
			if err != nil {
				return types.ToolResult{Content: err.Error(), IsError: true}, nil
			}
			socksDialer, err := proxy.SOCKS5("tcp", proxyAddress, nil, &net.Dialer{Timeout: 15 * time.Second})
			if err != nil {
				return types.ToolResult{Content: "Nao foi possivel configurar o proxy Tor: " + err.Error(), IsError: true}, nil
			}
			contextDialer, ok := socksDialer.(proxy.ContextDialer)
			if !ok {
				return types.ToolResult{Content: "O proxy Tor nao aceita cancelamento de conexoes.", IsError: true}, nil
			}
			transport := &http.Transport{
				DialContext:           contextDialer.DialContext,
				DisableKeepAlives:     true,
				TLSHandshakeTimeout:   20 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
			}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: torFetchTimeout,
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					if len(via) >= 5 {
						return errors.New("limite de redirecionamentos Tor atingido")
					}
					_, err := parseOnionURL(req.URL.String())
					return err
				},
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
			if err != nil {
				return types.ToolResult{Content: "URL .onion invalida: " + err.Error(), IsError: true}, nil
			}
			resp, err := client.Do(req)
			if err != nil {
				var urlErr *url.Error
				if errors.As(err, &urlErr) {
					err = urlErr.Err // Avoid echoing URL query parameters in the result.
				}
				return types.ToolResult{Content: "Falha no acesso via Tor; verifique o servico SOCKS local: " + err.Error(), IsError: true}, nil
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxTorResponseBytes+1))
			if err != nil {
				return types.ToolResult{Content: "Falha ao ler pagina via Tor: " + err.Error(), IsError: true}, nil
			}
			bodyTruncated := len(body) > maxTorResponseBytes
			if bodyTruncated {
				body = body[:maxTorResponseBytes]
			}
			mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if mediaType == "" {
				mediaType = http.DetectContentType(body)
			}
			if !torTextContentType(mediaType) {
				return types.ToolResult{Content: "Conteudo binario nao suportado por tor_fetch (" + mediaType + ").", IsError: true}, nil
			}
			runes := []rune(string(body))
			if page.start > len(runes) {
				return types.ToolResult{Content: fmt.Sprintf("Deslocamento %d alem do conteudo recebido (%d caracteres).", page.start, len(runes)), IsError: true}, nil
			}
			end := page.start + page.maxChars
			if end > len(runes) {
				end = len(runes)
			}
			content := fmt.Sprintf("HTTP %d; tipo: %s; caracteres %d-%d de %d", resp.StatusCode, mediaType,
				page.start, end, len(runes))
			if end < len(runes) {
				content += fmt.Sprintf("; proximo start: %d", end)
			}
			if bodyTruncated {
				content += "; corpo limitado a 512 KiB"
			}
			return types.ToolResult{Content: content + "\n" + string(runes[page.start:end]), IsError: resp.StatusCode >= 400}, nil
		},
	}
}

type torPage struct {
	start    int
	maxChars int
}

func torPageRange(args map[string]any) (torPage, error) {
	start, err := integerArg(args, "start", 0, maxTorResponseBytes)
	if err != nil {
		return torPage{}, err
	}
	maxChars, err := integerArg(args, "max_chars", defaultTorPageChars, maxTorPageChars)
	if err != nil {
		return torPage{}, err
	}
	return torPage{start: start, maxChars: maxChars}, nil
}

func parseOnionURL(raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target == nil || target.Opaque != "" || target.User != nil {
		return nil, errors.New("informe uma URL .onion HTTP(S) valida, sem usuario ou senha embutidos")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, errors.New("tor_fetch aceita apenas URLs http:// ou https://")
	}
	host := strings.ToLower(target.Hostname())
	if len(host) <= len(".onion") || !strings.HasSuffix(host, ".onion") {
		return nil, errors.New("tor_fetch aceita apenas dominios .onion")
	}
	if strings.ContainsAny(host, " /\\\t\r\n") || target.Host == "" {
		return nil, errors.New("host .onion invalido")
	}
	return target, nil
}

func torSOCKSAddress() (string, error) {
	address := strings.TrimSpace(os.Getenv("POLYPOD_TOR_SOCKS_ADDR"))
	if address == "" {
		address = defaultTorSOCKSAddress
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("POLYPOD_TOR_SOCKS_ADDR deve ser um endereco local host:porta")
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(portText)
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return "", errors.New("POLYPOD_TOR_SOCKS_ADDR deve usar IP de loopback e porta valida")
	}
	return address, nil
}

func torTextContentType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" ||
		mediaType == "application/xml" || mediaType == "application/xhtml+xml" || mediaType == "application/javascript"
}
