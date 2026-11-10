package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// loadRemoteSessionTLS reads the optional certificate pair for the session
// host. A nil config keeps the listener cleartext, which is allowed only on
// a loopback address. Both paths must be set together. The pair is read once
// at process start from non-symlink files; replacing the files takes effect
// on the next start. Cleartext is never used as a fallback when TLS was
// requested.
func loadRemoteSessionTLS() (*tls.Config, error) {
	certFile := strings.TrimSpace(os.Getenv("POLYPOD_REMOTE_SESSION_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("POLYPOD_REMOTE_SESSION_TLS_KEY"))
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("POLYPOD_REMOTE_SESSION_TLS_CERT e POLYPOD_REMOTE_SESSION_TLS_KEY precisam ser definidos juntos")
	}
	certPEM, err := readHostTLSFile(certFile, false)
	if err != nil {
		return nil, err
	}
	keyPEM, err := readHostTLSFile(keyFile, true)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("carregando certificado TLS das sessões remotas: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}, nil
}

// validateRemoteSessionListen allows a non-loopback IP only when TLS is
// configured. The host must be an IP, including 0.0.0.0 or :: when TLS is
// active. An empty host, a name, or port 0 is rejected so the process never
// chooses an interface or port by itself.
func validateRemoteSessionListen(listen string, tlsEnabled bool) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return fmt.Errorf("POLYPOD_REMOTE_SESSION_LISTEN precisa ser IP:porta")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("POLYPOD_REMOTE_SESSION_LISTEN precisa usar um endereço IP e uma porta")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("POLYPOD_REMOTE_SESSION_LISTEN tem porta inválida")
	}
	if !ip.IsLoopback() && !tlsEnabled {
		return fmt.Errorf("POLYPOD_REMOTE_SESSION_LISTEN fora de loopback exige POLYPOD_REMOTE_SESSION_TLS_CERT e POLYPOD_REMOTE_SESSION_TLS_KEY")
	}
	return nil
}

func listenRemoteSession(address string, tlsConfig *tls.Config) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	if tlsConfig == nil {
		return listener, nil
	}
	return tls.NewListener(listener, tlsConfig), nil
}
