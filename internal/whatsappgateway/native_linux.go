package whatsappgateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/humanloop"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

// Native uses an explicitly selected, already paired session directory. It never
// pairs automatically or opens PolyClaw's live default session. Run is opt-in.
// A migration must copy a quiesced SQLite backup to a separate directory first.
type Native struct {
	SessionDir string
	StatusPath string
	Logger     *slog.Logger
	mu         sync.RWMutex
	mediaMu    sync.Mutex
	client     *whatsmeow.Client
	running    bool
}

func (n *Native) Send(ctx context.Context, msg Outbound) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.client == nil || !n.client.IsConnected() || !n.client.IsLoggedIn() {
		return fmt.Errorf("whatsapp: sessao desconectada: %w", humanloop.ErrRemoteUnavailable)
	}
	to, err := types.ParseJID(msg.ChatID)
	if err != nil || to.IsEmpty() {
		return errors.New("whatsapp: JID invalido")
	}
	m := &waE2E.Message{Conversation: proto.String(msg.Text)}
	if msg.ReplyTo != "" {
		m = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(msg.Text), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String(msg.ReplyTo), Participant: proto.String(msg.Participant), QuotedMessage: &waE2E.Message{Conversation: proto.String(msg.QuotedText)}}}}
	}
	_, err = n.client.SendMessage(ctx, to, m, whatsmeow.SendRequestExtra{ID: msg.ID})
	var netErr net.Error
	if errors.Is(err, whatsmeow.ErrNotConnected) || errors.Is(err, whatsmeow.ErrNotLoggedIn) ||
		errors.Is(err, socket.ErrSocketClosed) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.As(err, &netErr) {
		return fmt.Errorf("whatsapp: sessao desconectada: %w", humanloop.ErrRemoteUnavailable)
	}
	return err
}

// Run blocks until cancellation. It serializes session ownership, waits for event
// callbacks on shutdown, and leaves reconnect/backoff to whatsmeow. Only a
// successful durable Receive permits WhatsApp's acknowledgement path to proceed.
func (n *Native) Run(ctx context.Context, g *Gateway) (runErr error) {
	defer func() {
		state := "retrying"
		if ctx.Err() != nil {
			state = "stopped"
		}
		n.recordStatus(state, false, false)
	}()
	if g == nil {
		return errors.New("whatsapp: gateway obrigatorio")
	}
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return errors.New("whatsapp: ja iniciado")
	}
	n.running = true
	n.mu.Unlock()
	defer func() { n.mu.Lock(); n.running = false; n.mu.Unlock() }()
	if err := n.Preflight(); err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(n.SessionDir)
	if err != nil {
		return fmt.Errorf("whatsapp: diretorio de sessao: %w", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	live := filepath.Join(home, ".picoclaw", "workspace", "whatsapp")
	if resolved, e := filepath.EvalSymlinks(live); e == nil {
		live = resolved
	}
	if dir == live {
		return errors.New("whatsapp: sessao ativa do PolyClaw protegida; use backup isolado apos o corte")
	}
	dbPath := filepath.Join(dir, "store.db")
	info, err := os.Lstat(dbPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("whatsapp: store.db deve ser arquivo regular")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dbPath, 0600); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "polypod.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, lock.Close()) }()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("whatsapp: sessao em uso por outro Polypod")
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	container := sqlstore.NewWithDB(db, "sqlite", nil)
	defer func() { runErr = errors.Join(runErr, container.Close()) }()
	if _, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	if err = container.Upgrade(ctx); err != nil {
		return err
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return err
	}
	if device == nil || device.ID == nil {
		return errors.New("whatsapp: sessao nao pareada; pareamento requer fluxo explicito")
	}
	logger := n.Logger
	if logger == nil {
		logger = slog.Default()
	}
	client := whatsmeow.NewClient(device, &waDiagnosticLogger{logger: logger})
	client.ManualHistorySyncDownload = true
	client.SynchronousAck = true
	client.EnableDecryptedEventBuffer = true
	handler := client.AddEventHandlerWithSuccessStatus(func(raw any) bool {
		switch evt := raw.(type) {
		case *events.Connected:
			logger.Info("WhatsApp authenticated")
			for _, channel := range []string{Channel, "notification", "scheduler"} {
				if err := g.store.ExpediteDeferredOutbounds(channel, time.Now()); err != nil {
					logger.Warn("WhatsApp deferred delivery wake failed", "channel", channel, "error", err)
				}
			}
			if g.wake != nil {
				g.wake()
			}
		case *events.Disconnected:
			logger.Warn("WhatsApp websocket disconnected")
		case *events.LoggedOut:
			logger.Error("WhatsApp session logged out", "reason", evt.Reason.String(), "on_connect", evt.OnConnect)
		case *events.ConnectFailure:
			logger.Warn("WhatsApp connection rejected", "reason", evt.Reason.String())
		case *events.StreamReplaced:
			logger.Error("WhatsApp session replaced by another client")
		case *events.ClientOutdated:
			logger.Error("WhatsApp client version rejected")
		case *events.CATRefreshError:
			logger.Error("WhatsApp authentication token refresh failed")
		case *events.TemporaryBan:
			logger.Error("WhatsApp connection temporarily restricted")
		case *events.KeepAliveTimeout:
			logger.Warn("WhatsApp keepalive timed out", "errors", evt.ErrorCount)
		case *events.UndecryptableMessage:
			logger.Warn("WhatsApp inbound could not be decrypted", "from_me", evt.Info.IsFromMe, "unavailable", evt.IsUnavailable)
		}
		evt, ok := raw.(*events.Message)
		if !ok {
			return true
		}
		if evt.Message == nil {
			logIgnoredCandidate(logger, "missing_payload", evt.Info.IsFromMe)
			return true
		}
		chat := evt.Info.Chat.String()
		e := Event{
			ID: evt.Info.ID, ChatID: chat,
			SenderID: senderForEvent(evt.Info.MessageSource), FromMe: evt.Info.IsFromMe,
		}
		if reason := g.ignoreReason(e); reason != "" {
			logIgnoredCandidate(logger, reason, evt.Info.IsFromMe)
			return true
		}
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		e.Text = evt.Message.GetConversation()
		if ext := evt.Message.ExtendedTextMessage; ext != nil {
			e.Text = ext.GetText()
			e.ReplyTo = ext.GetContextInfo().GetStanzaID()
		}
		mime, size, caption, ci := mediaInfo(evt.Message)
		if mime != "" {
			if size > 16<<20 {
				e.Text += "\n[anexo excede limite de 16 MiB]"
			} else {
				data, downloadErr := client.DownloadAny(callCtx, evt.Message)
				if downloadErr != nil {
					logger.Warn("WhatsApp media download failed", "error", downloadErr)
					return false
				}
				if len(data) > 16<<20 {
					e.Text += "\n[anexo excede limite de 16 MiB]"
				} else {
					// Keep the write and durable enqueue together with retention scans.
					n.mediaMu.Lock()
					defer n.mediaMu.Unlock()
					mediaDir := filepath.Join(dir, "media")
					if err := os.MkdirAll(mediaDir, 0700); err != nil {
						return false
					}
					sum := sha256.Sum256([]byte(chat + ":" + e.ID))
					path := filepath.Join(mediaDir, hex.EncodeToString(sum[:])+".bin")
					if err := os.WriteFile(path, data, 0600); err != nil {
						return false
					}
					e.MediaPath, e.MIME = path, mime
					e.Text += caption
				}
			}
			if ci != nil {
				e.ReplyTo = ci.GetStanzaID()
			}
		}
		if e.Text == "" && e.MediaPath == "" {
			logIgnoredCandidate(logger, "empty_content", evt.Info.IsFromMe)
			return true
		}
		if err := g.Receive(callCtx, e); err != nil {
			logger.Warn("WhatsApp inbox persistence failed", "error", err)
			return false
		}
		logger.Info("WhatsApp inbound processed", "from_me", evt.Info.IsFromMe,
			"chat_domain", jidDomain(evt.Info.Chat), "sender_domain", jidDomain(evt.Info.Sender))
		return true
	})
	n.mu.Lock()
	n.client = client
	n.mu.Unlock()
	defer func() {
		client.Disconnect()
		client.RemoveEventHandler(handler)
		n.mu.Lock()
		n.client = nil
		n.mu.Unlock()
	}()
	if err = client.ConnectContext(ctx); err != nil {
		return err
	}
	cleanup := func() {
		removed, cleanupErr := n.CleanupMedia(ctx, g, time.Now())
		if cleanupErr != nil {
			logger.Warn("WhatsApp media cleanup failed", "error", cleanupErr)
		} else if removed > 0 {
			logger.Info("WhatsApp media cleanup complete", "removed", removed)
		}
	}
	cleanup()
	lastCleanup := time.Now()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		state := "retrying"
		socketConnected, authenticated := client.IsConnected(), client.IsLoggedIn()
		if socketConnected && authenticated {
			state = "connected"
		}
		n.recordStatus(state, socketConnected, authenticated)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Since(lastCleanup) >= 24*time.Hour {
				cleanup()
				lastCleanup = time.Now()
			}
		}
	}
}

func logIgnoredCandidate(logger *slog.Logger, reason string, fromMe bool) {
	logger.Debug("WhatsApp candidate ignored", "reason", reason, "from_me", fromMe)
}

func senderForEvent(source types.MessageSource) string {
	sender := source.Sender
	if source.IsFromMe {
		// A phone-origin message can carry a device suffix. Strip only the
		// device, then require the exact configured sender JID.
		sender = sender.ToNonAD()
	}
	return sender.String()
}

func jidDomain(jid types.JID) string {
	switch jid.Server {
	case types.DefaultUserServer:
		return "phone"
	case types.HiddenUserServer, types.HostedLIDServer:
		return "lid"
	case types.GroupServer:
		return "group"
	default:
		return "other"
	}
}

// whatsmeow errors can contain message IDs and JIDs. Persist only a bounded
// category, which is enough to distinguish reconnect, network, auth and
// decryption failures without storing transport identifiers in app logs.
type waDiagnosticLogger struct{ logger *slog.Logger }

var _ waLog.Logger = (*waDiagnosticLogger)(nil)

func (l *waDiagnosticLogger) Errorf(format string, args ...any) {
	l.logger.Error("WhatsApp library error", "category", waDiagnosticCategory(format, args...))
}
func (l *waDiagnosticLogger) Warnf(format string, args ...any) {
	l.logger.Warn("WhatsApp library warning", "category", waDiagnosticCategory(format, args...))
}
func (l *waDiagnosticLogger) Infof(string, ...any)    {}
func (l *waDiagnosticLogger) Debugf(string, ...any)   {}
func (l *waDiagnosticLogger) Sub(string) waLog.Logger { return l }

func waDiagnosticCategory(format string, args ...any) string {
	message := strings.ToLower(fmt.Sprintf(format, args...))
	switch {
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline"), strings.Contains(message, "keepalive"):
		return "timeout"
	case strings.Contains(message, "handshake"), strings.Contains(message, "noise"):
		return "handshake"
	case strings.Contains(message, "websocket"), strings.Contains(message, "network"), strings.Contains(message, "dial"), strings.Contains(message, "lookup"):
		return "network"
	case strings.Contains(message, "unauthorized"), strings.Contains(message, "logged out"), strings.Contains(message, "401"):
		return "authentication"
	case strings.Contains(message, "decrypt"), strings.Contains(message, "signal"):
		return "decryption"
	case strings.Contains(message, "sqlite"), strings.Contains(message, "database"):
		return "storage"
	case strings.Contains(message, "reconnect"):
		return "reconnect"
	default:
		return "protocol"
	}
}

func (n *Native) recordStatus(state string, socketConnected, authenticated bool) {
	if err := WriteStatusHealth(n.StatusPath, state, socketConnected, authenticated); err != nil {
		logger := n.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("WhatsApp status persistence failed", "error", err)
	}
}

func mediaInfo(m *waE2E.Message) (string, uint64, string, *waE2E.ContextInfo) {
	switch {
	case m.ImageMessage != nil:
		v := m.ImageMessage
		return v.GetMimetype(), v.GetFileLength(), v.GetCaption(), v.GetContextInfo()
	case m.AudioMessage != nil:
		v := m.AudioMessage
		return v.GetMimetype(), v.GetFileLength(), "", v.GetContextInfo()
	case m.VideoMessage != nil:
		v := m.VideoMessage
		return v.GetMimetype(), v.GetFileLength(), v.GetCaption(), v.GetContextInfo()
	case m.DocumentMessage != nil:
		v := m.DocumentMessage
		return v.GetMimetype(), v.GetFileLength(), v.GetCaption(), v.GetContextInfo()
	case m.StickerMessage != nil:
		v := m.StickerMessage
		return v.GetMimetype(), v.GetFileLength(), "", v.GetContextInfo()
	}
	return "", 0, "", nil
}

// Preflight validates the isolated directory without opening the session database.
// Pairing/schema readiness is checked by Run, under the session lock.
func (n *Native) Preflight() error {
	if strings.TrimSpace(n.SessionDir) == "" {
		return errors.New("whatsapp: diretorio de sessao obrigatorio")
	}
	dir, err := filepath.EvalSymlinks(n.SessionDir)
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if strings.HasSuffix(filepath.ToSlash(dir), "/.picoclaw/workspace/whatsapp") || strings.HasSuffix(filepath.ToSlash(dir), "/.polyclaw/workspace/whatsapp") {
		return errors.New("whatsapp: sessao ativa do PolyClaw protegida; use backup isolado apos o corte")
	}
	info, err := os.Lstat(filepath.Join(dir, "store.db"))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("whatsapp: store.db deve ser arquivo regular")
	}
	return nil
}
