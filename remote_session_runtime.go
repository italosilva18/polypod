package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/remote"
)

const remoteSessionChannel = "remote_session"
const remoteMessageContentLimit = 256 << 10

// remoteSessionRuntime owns the local HTTP listener. Only the configured host
// process starts it; other Polypod processes can still process durable inbox
// entries and defer outbound delivery until the host is available.
type remoteSessionRuntime struct {
	server *remote.Server
	cancel context.CancelFunc
	done   chan error
	logger *slog.Logger
}

func startRemoteSessionRuntime(parent context.Context, dataDir string, store *humanloop.Store,
	wake func(), logger *slog.Logger) (*remoteSessionRuntime, error) {
	if os.Getenv("POLYPOD_REMOTE_SESSION_HOST") != "1" {
		return nil, nil
	}
	token := os.Getenv("POLYPOD_REMOTE_SESSION_TOKEN")
	if len(token) < 32 || len(token) > 512 {
		return nil, fmt.Errorf("POLYPOD_REMOTE_SESSION_TOKEN precisa ter entre 32 e 512 caracteres")
	}
	listen := strings.TrimSpace(os.Getenv("POLYPOD_REMOTE_SESSION_LISTEN"))
	if listen == "" {
		listen = "127.0.0.1:8789"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fmt.Errorf("POLYPOD_REMOTE_SESSION_LISTEN precisa usar IP de loopback e porta")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("POLYPOD_REMOTE_SESSION_LISTEN tem porta inválida")
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("abrindo sessões remotas: %w", err)
	}
	server, err := remote.NewServer(remote.ServerConfig{
		DatabasePath: filepath.Join(dataDir, "remote-sessions", "sessions.db"),
		AuthToken:    token,
		Logger:       logger,
		OnUserMessage: func(ctx context.Context, session remote.RemoteSession, msg remote.Message) error {
			userID := session.ID + ":" + msg.Sender
			externalID := session.ID + ":" + msg.ID
			if _, err := store.EnqueueInboundOnce(remoteSessionChannel, userID, msg.Content, externalID); err != nil {
				return err
			}
			if wake != nil {
				wake()
			}
			return nil
		},
	})
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &remoteSessionRuntime{server: server, cancel: cancel, done: make(chan error, 1), logger: logger}
	go func() {
		err := server.Serve(ctx, listener)
		if err != nil && ctx.Err() == nil {
			logger.Error("servidor de sessões remotas parou", "error", err)
		}
		runtime.done <- err
	}()
	logger.Info("sessões remotas disponíveis", "listen", listen)
	return runtime, nil
}

func (r *remoteSessionRuntime) Deliver(ctx context.Context, msg humanloop.Message) error {
	if r == nil {
		return humanloop.ErrRemoteUnavailable
	}
	separator := strings.IndexByte(msg.UserID, ':')
	if separator < 1 || separator == len(msg.UserID)-1 {
		return fmt.Errorf("destinatário da sessão remota inválido")
	}
	sessionID := msg.UserID[:separator]
	parts := splitRemoteResponse(msg.Text)
	digest := sha256.Sum256([]byte(msg.ID))
	for index, part := range parts {
		id := fmt.Sprintf("out_%x_%06d", digest, index)
		if _, err := r.server.Publish(ctx, sessionID, remote.Message{
			ID: id, Type: "assistant", Content: part,
		}); err != nil {
			return fmt.Errorf("entregando parte %d/%d da resposta remota: %w", index+1, len(parts), err)
		}
	}
	return nil
}

// splitRemoteResponse preserves UTF-8 boundaries so each durable HTTP message
// stays below the server's per-message limit. Stable IDs make partial retries
// safe when an outbox response spans multiple messages.
func splitRemoteResponse(response string) []string {
	response = strings.ToValidUTF8(response, "\ufffd")
	if response == "" {
		return []string{""}
	}
	parts := make([]string, 0, 1+len(response)/remoteMessageContentLimit)
	for start := 0; start < len(response); {
		end := start + remoteMessageContentLimit
		if end >= len(response) {
			end = len(response)
		} else {
			for end > start && !utf8.RuneStart(response[end]) {
				end--
			}
		}
		parts = append(parts, response[start:end])
		start = end
	}
	return parts
}

func (r *remoteSessionRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			r.logger.Warn("sessões remotas encerradas com erro", "error", err)
		}
		return errors.Join(err, r.server.Close())
	case <-time.After(7 * time.Second):
		return fmt.Errorf("prazo excedido ao encerrar sessões remotas")
	}
}
