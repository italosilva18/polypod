package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// ExtensionBridge presents a paired browser extension as a local CDP endpoint.
// The extension owns the user's browser session; Polypod never copies its
// profile, cookies or passwords. The random path authorizes both transports.
type ExtensionBridge struct {
	mu       sync.RWMutex
	listener net.Listener
	server   *http.Server
	token    string
	peer     *extensionPeer
}

type extensionRequest struct {
	ID     uint64          `json:"id"`
	Op     string          `json:"op"`
	TabID  string          `json:"tab_id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type extensionReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type extensionPeer struct {
	conn    *websocket.Conn
	sendMu  sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan extensionReply
	closed  bool
}

// NewExtensionBridge listens on a random loopback port. PairURL is entered in
// the unpacked extension's popup; it is never a public network endpoint.
func NewExtensionBridge() (*ExtensionBridge, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("criando chave da extensão: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("abrindo ponte local da extensão: %w", err)
	}
	b := &ExtensionBridge{listener: listener, token: hex.EncodeToString(secret)}
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = b.server.Serve(listener) }()
	return b, nil
}

// Endpoint is consumed by the existing CDP client. PairURL is private pairing
// material for the extension popup. A new bridge gets a new random URL.
func (b *ExtensionBridge) Endpoint() string {
	return "http://" + b.listener.Addr().String() + "/" + b.token
}

func (b *ExtensionBridge) PairURL() string {
	return "ws://" + b.listener.Addr().String() + "/" + b.token + "/extension"
}

func (b *ExtensionBridge) Connected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.peer != nil
}

func (b *ExtensionBridge) Close() error {
	b.mu.Lock()
	peer := b.peer
	b.peer = nil
	b.mu.Unlock()
	if peer != nil {
		peer.fail(errors.New("ponte da extensão encerrada"))
		_ = peer.conn.Close()
	}
	return b.server.Close()
}

func (b *ExtensionBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != b.listener.Addr().String() {
		http.NotFound(w, r)
		return
	}
	prefix := "/" + b.token + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if path == "extension" {
		b.serveExtension(w, r)
		return
	}
	if strings.HasPrefix(path, "devtools/page/") {
		b.serveCDP(w, r, strings.TrimPrefix(path, "devtools/page/"))
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "origem não permitida", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case path == "json/list" && r.Method == http.MethodGet:
		var tabs []Tab
		if err := b.call(ctx, "tabs", "", "", nil, &tabs); err != nil {
			bridgeHTTPError(w, err)
			return
		}
		for i := range tabs {
			tabs[i].WebSocketURL = "ws://" + b.listener.Addr().String() + "/" + b.token + "/devtools/page/" + url.PathEscape(tabs[i].ID)
		}
		_ = json.NewEncoder(w).Encode(tabs)
	case path == "json/new" && r.Method == http.MethodPut:
		var tab Tab
		if err := b.call(ctx, "new", "", "", nil, &tab); err != nil {
			bridgeHTTPError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(tab)
	case strings.HasPrefix(path, "json/activate/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "json/activate/")
		if err := b.call(ctx, "activate", id, "", nil, nil); err != nil {
			bridgeHTTPError(w, err)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	case strings.HasPrefix(path, "json/close/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "json/close/")
		if err := b.call(ctx, "close", id, "", nil, nil); err != nil {
			bridgeHTTPError(w, err)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	default:
		http.NotFound(w, r)
	}
}

func bridgeHTTPError(w http.ResponseWriter, err error) {
	// Errors from the browser are returned only to this loopback client.
	http.Error(w, err.Error(), http.StatusServiceUnavailable)
}

func (b *ExtensionBridge) serveExtension(w http.ResponseWriter, r *http.Request) {
	server := websocket.Server{
		Handshake: func(_ *websocket.Config, req *http.Request) error {
			if !strings.HasPrefix(req.Header.Get("Origin"), "chrome-extension://") {
				return errors.New("origem da extensão inválida")
			}
			return nil
		},
		Handler: func(conn *websocket.Conn) {
			conn.MaxPayloadBytes = 16 << 20
			peer := &extensionPeer{conn: conn, pending: make(map[uint64]chan extensionReply)}
			b.mu.Lock()
			previous := b.peer
			b.peer = peer
			b.mu.Unlock()
			if previous != nil {
				previous.fail(errors.New("extensão reconectada"))
				_ = previous.conn.Close()
			}
			defer func() {
				peer.fail(errors.New("extensão desconectada"))
				b.mu.Lock()
				if b.peer == peer {
					b.peer = nil
				}
				b.mu.Unlock()
			}()
			for {
				var reply extensionReply
				if err := websocket.JSON.Receive(conn, &reply); err != nil {
					return
				}
				peer.dispatch(reply)
			}
		},
	}
	server.ServeHTTP(w, r)
}

func (b *ExtensionBridge) serveCDP(w http.ResponseWriter, r *http.Request, tabID string) {
	if tabID == "" || strings.Contains(tabID, "/") {
		http.NotFound(w, r)
		return
	}
	server := websocket.Server{
		Handshake: func(_ *websocket.Config, req *http.Request) error {
			if req.Header.Get("Origin") != "http://localhost" {
				return errors.New("origem CDP inválida")
			}
			return nil
		},
		Handler: func(conn *websocket.Conn) {
			conn.MaxPayloadBytes = 1 << 20
			for {
				var request struct {
					ID     int             `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := websocket.JSON.Receive(conn, &request); err != nil {
					return
				}
				if request.ID <= 0 || (request.Method != "Runtime.evaluate" && request.Method != "Page.captureScreenshot" && request.Method != "Page.navigate") {
					_ = websocket.JSON.Send(conn, map[string]any{"id": request.ID, "error": map[string]string{"message": "método CDP indisponível"}})
					continue
				}
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				var result json.RawMessage
				err := b.call(ctx, "cdp", tabID, request.Method, request.Params, &result)
				cancel()
				if err != nil {
					if sendErr := websocket.JSON.Send(conn, map[string]any{"id": request.ID, "error": map[string]string{"message": err.Error()}}); sendErr != nil {
						return
					}
					continue
				}
				if len(result) == 0 {
					result = json.RawMessage(`{}`)
				}
				if err := websocket.JSON.Send(conn, map[string]any{"id": request.ID, "result": result}); err != nil {
					return
				}
			}
		},
	}
	server.ServeHTTP(w, r)
}

func (b *ExtensionBridge) call(ctx context.Context, op, tabID, method string, params json.RawMessage, out any) error {
	b.mu.RLock()
	peer := b.peer
	b.mu.RUnlock()
	if peer == nil {
		return errors.New("extensão ainda não conectada; abra o popup e informe a URL de pareamento")
	}
	reply, err := peer.call(ctx, extensionRequest{Op: op, TabID: tabID, Method: method, Params: params})
	if err != nil {
		return err
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	if out == nil {
		return nil
	}
	if len(reply.Result) == 0 {
		return errors.New("extensão não devolveu resultado")
	}
	return json.Unmarshal(reply.Result, out)
}

func (p *extensionPeer) call(ctx context.Context, req extensionRequest) (extensionReply, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return extensionReply{}, errors.New("extensão desconectada")
	}
	p.nextID++
	req.ID = p.nextID
	response := make(chan extensionReply, 1)
	p.pending[req.ID] = response
	p.mu.Unlock()
	p.sendMu.Lock()
	err := websocket.JSON.Send(p.conn, req)
	p.sendMu.Unlock()
	if err != nil {
		p.mu.Lock()
		delete(p.pending, req.ID)
		p.mu.Unlock()
		return extensionReply{}, fmt.Errorf("enviando pedido à extensão: %w", err)
	}
	select {
	case reply := <-response:
		if reply.Error == "" && p.isClosed() {
			return extensionReply{}, errors.New("extensão desconectada")
		}
		return reply, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, req.ID)
		p.mu.Unlock()
		return extensionReply{}, ctx.Err()
	}
}

func (p *extensionPeer) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *extensionPeer) dispatch(reply extensionReply) {
	p.mu.Lock()
	ch := p.pending[reply.ID]
	delete(p.pending, reply.ID)
	p.mu.Unlock()
	if ch != nil {
		ch <- reply
	}
}

func (p *extensionPeer) fail(err error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	pending := p.pending
	p.pending = make(map[uint64]chan extensionReply)
	p.mu.Unlock()
	for _, ch := range pending {
		ch <- extensionReply{Error: err.Error()}
	}
}
