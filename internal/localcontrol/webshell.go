package localcontrol

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

// ChatTurnExecutor processes one prompt submitted from the Web Shell and streams deltas.
type ChatTurnExecutor func(ctx context.Context, prompt string, onDelta func(string)) (string, error)

// TranscriptEntry represents one chat message displayed in the Web Shell.
type TranscriptEntry struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// Config configures the `polypod serve` / `--local-control` Web Shell daemon.
type Config struct {
	ListenAddr   string // e.g. "127.0.0.1:8789" or "<lan-ip>:8789"
	LocalControl bool   // when true, binds private LAN IPv4, renders QR code, and optionally inhibits sleep
	InhibitSleep bool
	Workspace    string
	ModelLabel   string
	Executor     ChatTurnExecutor
	Questions    *humanloop.MultiChildQuestionQueue
}

// Server hosts the Polypod Web Shell + SSE stream with revocable pairing tokens.
type Server struct {
	cfg        Config
	mu         sync.RWMutex
	token      string
	seq        int
	busy       bool
	transcript []TranscriptEntry
	subs       map[chan string]struct{}
	mux        *http.ServeMux
}

// NewServer creates a new Web Shell server with a fresh 256-bit pairing token.
func NewServer(cfg Config) (*Server, error) {
	tok, err := generateToken()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:   cfg,
		token: tok,
		subs:  make(map[chan string]struct{}),
		mux:   http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /api/state", s.requireAuth(s.handleState))
	s.mux.HandleFunc("POST /api/chat", s.requireAuth(s.handleChat))
	s.mux.HandleFunc("GET /api/events", s.requireAuth(s.handleEvents))
	s.mux.HandleFunc("POST /api/answer", s.requireAuth(s.handleAnswerQuestion))
	s.mux.HandleFunc("POST /api/revoke", s.requireAuth(s.handleRevoke))
	return s, nil
}

func generateToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gerando token de pareamento: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Token returns the current active pairing token.
func (s *Server) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// RotateToken revokes the existing pairing token immediately and issues a new one.
func (s *Server) RotateToken() (string, error) {
	next, err := generateToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.token = next
	s.mu.Unlock()
	s.broadcastJSON(map[string]any{"type": "revoked"})
	return next, nil
}

// PairingURL builds the fragment-protected Web Shell URL (`http://host:port/#token=...`)
// so the secret never travels in HTTP request paths, query strings, or Referer headers.
func (s *Server) PairingURL(boundAddr string) string {
	return fmt.Sprintf("http://%s/#token=%s", boundAddr, s.Token())
}

// Handler returns the HTTP handler for unit testing or custom listeners.
func (s *Server) Handler() http.Handler { return s.mux }

// DetectPrivateLANIPv4 finds the machine's preferred RFC1918 private IPv4 address
// (192.168.x.x, 10.x.x.x, or 172.16-31.x.x) for `--local-control` LAN pairing.
func DetectPrivateLANIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 != nil && ip4.IsPrivate() {
				return ip4.String(), nil
			}
		}
	}
	return "", fmt.Errorf("nenhuma interface LAN privada RFC1918 ativa detectada")
}

// StartSleepInhibitor optionally launches `systemd-inhibit` on Linux while `--local-control`
// is active so mobile sessions do not drop due to laptop idle suspend.
func StartSleepInhibitor(ctx context.Context) func() {
	if runtime.GOOS != "linux" {
		return func() {}
	}
	bin, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return func() {}
	}
	inhCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(inhCtx, bin,
		"--what=idle:sleep",
		"--who=Polypod Local Control",
		"--why=Sessão móvel polypod serve --local-control ativa",
		"sleep", "infinity",
	)
	if err := cmd.Start(); err != nil {
		cancel()
		return func() {}
	}
	return func() {
		cancel()
		_ = cmd.Wait()
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
		if provided == "" {
			// Optional header fallback for EventSource browsers
			provided = strings.TrimSpace(r.Header.Get("X-Polypod-Token"))
		}
		s.mu.RLock()
		expected := s.token
		s.mu.RUnlock()
		if len(provided) == 0 || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"token de pareamento inválido ou revogado"}`))
			return
		}
		next(w, r)
	}
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	resp := map[string]any{
		"workspace":  s.cfg.Workspace,
		"model":      s.cfg.ModelLabel,
		"busy":       s.busy,
		"transcript": append([]TranscriptEntry(nil), s.transcript...),
	}
	s.mu.RUnlock()
	if s.cfg.Questions != nil {
		resp["questions"] = s.cfg.Questions.ListPending()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"JSON inválido"}`, http.StatusBadRequest)
		return
	}
	prompt := strings.TrimSpace(body.Prompt)
	if prompt == "" {
		http.Error(w, `{"error":"prompt vazio"}`, http.StatusBadRequest)
		return
	}
	if s.cfg.Executor == nil {
		http.Error(w, `{"error":"executor indisponível"}`, http.StatusServiceUnavailable)
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		http.Error(w, `{"error":"um turno já está em execução"}`, http.StatusConflict)
		return
	}
	s.busy = true
	s.seq++
	userMsg := TranscriptEntry{ID: s.seq, Role: "user", Content: prompt, Timestamp: time.Now()}
	s.transcript = append(s.transcript, userMsg)
	s.mu.Unlock()

	s.broadcastJSON(map[string]any{"type": "message", "entry": userMsg})

	go func() {
		turnCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		reply, err := s.cfg.Executor(turnCtx, prompt, func(delta string) {
			if delta != "" {
				s.broadcastJSON(map[string]any{"type": "delta", "text": delta})
			}
		})
		if err != nil && reply == "" {
			reply = "Erro: " + err.Error()
		}
		s.mu.Lock()
		s.seq++
		asstMsg := TranscriptEntry{ID: s.seq, Role: "assistant", Content: reply, Timestamp: time.Now()}
		s.transcript = append(s.transcript, asstMsg)
		s.busy = false
		s.mu.Unlock()
		s.broadcastJSON(map[string]any{"type": "done", "entry": asstMsg})
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"status":"accepted"}`))
}

func (s *Server) handleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Questions == nil {
		http.Error(w, `{"error":"fila de perguntas indisponível"}`, http.StatusNotFound)
		return
	}
	var body struct {
		ID     string `json:"id"`
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"JSON inválido"}`, http.StatusBadRequest)
		return
	}
	if err := s.cfg.Questions.Answer(body.ID, body.Answer); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"answered"}`))
}

func (s *Server) handleRevoke(w http.ResponseWriter, _ *http.Request) {
	_, err := s.RotateToken()
	if err != nil {
		http.Error(w, `{"error":"falha ao revogar token"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"revoked"}`))
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming indisponível", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 32)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

	_, _ = fmt.Fprint(w, "data: {\"type\":\"connected\"}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *Server) broadcastJSON(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	payload := string(raw)
	s.mu.RLock()
	for ch := range s.subs {
		select {
		case ch <- payload:
		default:
		}
	}
	s.mu.RUnlock()
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = io.WriteString(w, webShellHTML)
}

const webShellHTML = `<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1">
<title>Polypod Web Shell · Local Control</title>
<style>
  :root { color-scheme: dark; --bg:#0b0f17; --card:#131a26; --fg:#e6edf3; --muted:#8b949e; --accent:#58a6ff; --danger:#f85149; }
  * { box-sizing: border-box; }
  body { margin:0; font-family: system-ui,-apple-system,sans-serif; background:var(--bg); color:var(--fg); display:flex; flex-direction:column; height:100dvh; }
  header { padding:12px 16px; background:var(--card); border-bottom:1px solid #21262d; display:flex; justify-content:space-between; align-items:center; gap:8px; }
  .title { font-weight:700; font-size:15px; }
  .meta { font-size:12px; color:var(--muted); }
  main { flex:1; overflow-y:auto; padding:14px; display:flex; flex-direction:column; gap:10px; }
  .msg { padding:10px 12px; border-radius:10px; max-width:92%; white-space:pre-wrap; word-break:break-word; font-size:14px; line-height:1.45; }
  .user { align-self:flex-end; background:#1f6feb; color:#fff; }
  .assistant { align-self:flex-start; background:var(--card); border:1px solid #262c36; }
  footer { padding:10px 12px; background:var(--card); border-top:1px solid #21262d; display:flex; gap:8px; }
  input { flex:1; background:#0d1117; border:1px solid #30363d; color:var(--fg); border-radius:8px; padding:10px 12px; font-size:15px; }
  button { background:var(--accent); color:#05080d; font-weight:600; border:none; border-radius:8px; padding:10px 14px; cursor:pointer; }
  button.revoke { background:transparent; border:1px solid var(--danger); color:var(--danger); padding:6px 10px; font-size:12px; }
</style>
</head>
<body>
<header>
  <div>
    <div class="title">Polypod · Web Shell</div>
    <div class="meta" id="meta">Conectando...</div>
  </div>
  <button class="revoke" onclick="revokeSession()">Revogar Acesso</button>
</header>
<main id="chat"></main>
<footer>
  <input id="prompt" placeholder="Envie uma tarefa ou comando para a Poly..." onkeydown="if(event.key==='Enter')sendPrompt()">
  <button onclick="sendPrompt()">Enviar</button>
</footer>
<script>
(function(){
  const hash = new URLSearchParams(location.hash.slice(1));
  if (hash.get('token')) {
    sessionStorage.setItem('polypod_token', hash.get('token'));
    history.replaceState(null, '', location.pathname);
  }
})();
function tok() { return sessionStorage.getItem('polypod_token') || ''; }
function hdrs() { return {'Authorization':'Bearer '+tok(), 'Content-Type':'application/json'}; }
const chat = document.getElementById('chat');
let streamEl = null;

function addMsg(role, text) {
  const d = document.createElement('div');
  d.className = 'msg ' + role;
  d.textContent = text;
  chat.appendChild(d);
  chat.scrollTop = chat.scrollHeight;
  return d;
}

async function loadState() {
  const r = await fetch('/api/state', {headers: hdrs()});
  if (!r.ok) { document.getElementById('meta').textContent = 'Sessão não autorizada ou revogada'; return; }
  const s = await r.json();
  document.getElementById('meta').textContent = (s.model || 'Poly') + ' · ' + (s.workspace || '.');
  chat.innerHTML = '';
  (s.transcript || []).forEach(m => addMsg(m.role, m.content));
  pollEvents();
}

async function pollEvents() {
  try {
    const resp = await fetch('/api/events', {headers: hdrs()});
    if (!resp.ok || !resp.body) return;
    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    while (true) {
      const {value, done} = await reader.read();
      if (done) break;
      buf += dec.decode(value, {stream:true});
      let idx;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const chunk = buf.slice(0, idx); buf = buf.slice(idx + 2);
        if (!chunk.startsWith('data: ')) continue;
        const ev = JSON.parse(chunk.slice(6));
        if (ev.type === 'message' && ev.entry.role === 'user') { addMsg('user', ev.entry.content); streamEl = null; }
        else if (ev.type === 'delta') { if (!streamEl) streamEl = addMsg('assistant', ''); streamEl.textContent += ev.text; chat.scrollTop = chat.scrollHeight; }
        else if (ev.type === 'done') { if (streamEl) { streamEl.textContent = ev.entry.content; streamEl = null; } else addMsg('assistant', ev.entry.content); }
        else if (ev.type === 'revoked') { alert('Token revogado!'); location.reload(); }
      }
    }
  } catch (_) {}
}

window.sendPrompt = async function() {
  const inp = document.getElementById('prompt');
  const p = inp.value.trim();
  if (!p) return;
  inp.value = '';
  await fetch('/api/chat', {method:'POST', headers:hdrs(), body:JSON.stringify({prompt:p})});
};

window.revokeSession = async function() {
  await fetch('/api/revoke', {method:'POST', headers:hdrs()});
  sessionStorage.removeItem('polypod_token');
  document.getElementById('meta').textContent = 'Acesso revogado.';
};

loadState();
</script>
</body>
</html>`
