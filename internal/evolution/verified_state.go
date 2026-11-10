package evolution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxVerificationJSON = 1 << 20
	maxExpectedJSON     = 64 << 10
	verificationTimeout = 5 * time.Second
)

var errVerificationDestination = errors.New("destino HTTP não permitido para verificação")

func verifierName(kind string) string { return kind + "_v1" }

func verifierKind(name string) string {
	for _, kind := range []string{FileSHA256Check, HTTPStatusCheck, HTTPJSONCheck, SQLiteScalarCheck} {
		if name == verifierName(kind) {
			return kind
		}
	}
	return ""
}

func internalVerifier(name string) bool { return verifierKind(strings.TrimSpace(name)) != "" }

func verificationState(ctx context.Context, plan VerificationPlanInput) (string, error) {
	switch plan.Kind {
	case FileSHA256Check:
		return fileStateSHA256(ctx, plan.Path)
	case HTTPStatusCheck, HTTPJSONCheck:
		return httpVerificationState(ctx, plan)
	case SQLiteScalarCheck:
		return sqliteVerificationState(ctx, plan)
	default:
		return "", errors.New("tipo de verificação desconhecido")
	}
}

// A digest contains only the selected state, with kind and status as domain
// separators. Response bodies and SQL values never enter the evidence table.
func stateDigest(kind string, status int, value []byte) string {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(status)))
	h.Write([]byte{0})
	h.Write(value)
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalExpectedJSON(raw string) ([]byte, error) {
	if raw == "" || len(raw) > maxExpectedJSON {
		return nil, errors.New("expected_json deve conter JSON válido de até 64 KiB")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("expected_json inválido: %w", err)
	}
	var more any
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected_json deve conter um único valor")
	}
	return json.Marshal(value)
}

func isJSONScalar(value []byte) bool {
	return len(value) > 0 && value[0] != '[' && value[0] != '{'
}

func validateJSONPointer(pointer string) error {
	if len(pointer) > 256 || (pointer != "" && !strings.HasPrefix(pointer, "/")) {
		return errors.New("selector deve ser ponteiro JSON RFC 6901 de até 256 caracteres")
	}
	if strings.Count(pointer, "/") > 32 {
		return errors.New("selector JSON contém segmentos demais")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		for i := 0; i < len(segment); i++ {
			if segment[i] != '~' {
				continue
			}
			if i+1 >= len(segment) || (segment[i+1] != '0' && segment[i+1] != '1') {
				return errors.New("escape inválido no selector JSON")
			}
			i++
		}
	}
	return nil
}

func resolveJSONPointer(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	for _, encoded := range strings.Split(pointer[1:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			var found bool
			value, found = node[key]
			if !found {
				return nil, false
			}
		case []any:
			if key == "" || (len(key) > 1 && key[0] == '0') {
				return nil, false
			}
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			value = node[index]
		default:
			return nil, false
		}
	}
	return value, true
}

func validateVerificationURL(raw string) (string, error) {
	if len(raw) > 2048 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("URL de verificação inválida ou longa demais")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", errors.New("URL exige HTTP(S), sem credenciais, consulta ou fragmento")
	}
	if u.Scheme == "http" {
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("HTTP sem TLS é permitido apenas em localhost")
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("porta da URL inválida")
		}
	}
	return u.String(), nil
}

func publicVerificationIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(block).Contains(addr) {
			return false
		}
	}
	return true
}

func httpVerificationState(ctx context.Context, plan VerificationPlanInput) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, verificationTimeout)
	defer cancel()
	u, err := url.Parse(plan.Path)
	if err != nil {
		return "", err
	}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 2 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, u.Hostname()) {
				return nil, errVerificationDestination
			}
			var addresses []net.IPAddr
			if literal := net.ParseIP(host); literal != nil {
				addresses = []net.IPAddr{{IP: literal}}
			} else {
				addresses, err = net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
			}
			if len(addresses) == 0 {
				return nil, errors.New("destino HTTP sem endereço")
			}
			for _, candidate := range addresses {
				allowed := candidate.IP.IsLoopback()
				if u.Scheme == "https" {
					allowed = publicVerificationIP(candidate.IP)
				}
				if !allowed {
					return nil, errVerificationDestination
				}
			}
			dialer := net.Dialer{Timeout: 2 * time.Second}
			var dialErr error
			for _, candidate := range addresses {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				dialErr = err
			}
			return nil, dialErr
		},
	}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.Path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errVerificationDestination) || ctx.Err() != nil {
			return "", err
		}
		return stateDigest(plan.Kind, 0, []byte("unreachable")), nil
	}
	defer resp.Body.Close()
	if plan.Kind == HTTPStatusCheck {
		return stateDigest(plan.Kind, resp.StatusCode, nil), nil
	}
	if resp.StatusCode != plan.ExpectedStatus {
		return stateDigest(plan.Kind, resp.StatusCode, []byte("unexpected-status")), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVerificationJSON+1))
	if err != nil || len(body) > maxVerificationJSON {
		return "", errors.New("resposta HTTP excede 1 MiB ou falhou na leitura")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return stateDigest(plan.Kind, resp.StatusCode, []byte("invalid-json")), nil
	}
	var more any
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return stateDigest(plan.Kind, resp.StatusCode, []byte("invalid-json")), nil
	}
	selected, found := resolveJSONPointer(value, plan.Selector)
	if !found {
		return stateDigest(plan.Kind, resp.StatusCode, []byte("missing-pointer")), nil
	}
	canonical, err := json.Marshal(selected)
	if err != nil {
		return "", err
	}
	return stateDigest(plan.Kind, resp.StatusCode, canonical), nil
}

func validateScalarQuery(query string) error {
	upper := strings.ToUpper(query)
	if len(query) > 4096 || !strings.HasPrefix(upper, "SELECT ") ||
		strings.ContainsAny(query, ";\x00") || strings.Contains(query, "--") ||
		strings.Contains(query, "/*") || strings.Contains(query, "*/") {
		return errors.New("selector SQL deve ser um único SELECT sem comentários, de até 4096 caracteres")
	}
	return nil
}

func sqliteVerificationState(ctx context.Context, plan VerificationPlanInput) (string, error) {
	info, err := os.Lstat(plan.Path)
	if errors.Is(err, os.ErrNotExist) {
		return missingVerifiedFile, nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("alvo SQLite não é arquivo regular")
	}
	ctx, cancel := context.WithTimeout(ctx, verificationTimeout)
	defer cancel()
	dsn := (&url.URL{Scheme: "file", Path: plan.Path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return "", err
	}
	rows, err := conn.QueryContext(ctx, plan.Selector)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if len(columns) != 1 {
		return "", errors.New("consulta de verificação exige exatamente uma coluna")
	}
	value := []byte("missing-row")
	if rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return "", err
		}
		if _, blob := raw.([]byte); blob {
			return "", errors.New("resultado SQLite BLOB não é permitido")
		}
		value, err = json.Marshal(raw)
		if err != nil {
			return "", err
		}
		if rows.Next() {
			value = []byte("multiple-rows")
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	latest, err := os.Lstat(plan.Path)
	if err != nil || !latest.Mode().IsRegular() || !os.SameFile(info, latest) ||
		latest.Size() != info.Size() || !latest.ModTime().Equal(info.ModTime()) {
		return "", ErrVerificationUnstable
	}
	return stateDigest(plan.Kind, 0, value), nil
}
