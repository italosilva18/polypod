package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/costa/polypod/internal/adapter"
)

//go:embed static
var staticFS embed.FS

// Server provides a browser-based chat UI.
type Server struct {
	host          string
	port          int
	streamHandler adapter.StreamHandler
	logger        *slog.Logger
	clients       map[chan string]bool
	mu            sync.Mutex
}

// New creates a web UI server.
func New(host string, port int, streamHandler adapter.StreamHandler, logger *slog.Logger) *Server {
	return &Server{
		host:          host,
		port:          port,
		streamHandler: streamHandler,
		logger:        logger,
		clients:       make(map[chan string]bool),
	}
}

// Start begins serving the web UI.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Serve static files (embedded)
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/health", s.handleHealth)

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	s.logger.Info("web UI disponivel", "url", fmt.Sprintf("http://%s", addr))
	return server.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "metodo nao permitido", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "corpo invalido", http.StatusBadRequest)
		return
	}

	// SSE response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming nao suportado", http.StatusInternalServerError)
		return
	}

	msg := adapter.InMessage{
		Channel: "webui",
		UserID:  r.RemoteAddr,
		Text:    req.Message,
	}

	chunks := make(chan adapter.StreamChunk, 100)
	go s.streamHandler(r.Context(), msg, chunks)

	for chunk := range chunks {
		if chunk.Error != nil {
			data, _ := json.Marshal(map[string]string{"type": "error", "error": chunk.Error.Error()})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			continue
		}
		if chunk.Delta != "" {
			data, _ := json.Marshal(map[string]string{"type": "delta", "content": chunk.Delta})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		if chunk.Done {
			fmt.Fprintf(w, "data: {\"type\":\"done\"}\n\n")
			flusher.Flush()
		}
	}
}

const indexHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Polypod</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;background:#0a0a0a;color:#e0e0e0;height:100vh;display:flex;flex-direction:column}
.header{padding:16px 24px;border-bottom:1px solid #222;display:flex;align-items:center;gap:12px}
.header h1{font-size:20px;font-weight:600;color:#fff}
.header span{font-size:13px;color:#666}
.messages{flex:1;overflow-y:auto;padding:24px;display:flex;flex-direction:column;gap:16px}
.msg{max-width:80%;padding:12px 16px;border-radius:12px;line-height:1.5;white-space:pre-wrap;font-size:14px}
.msg.user{align-self:flex-end;background:#1a3a5c;color:#e0e8f0;border-bottom-right-radius:4px}
.msg.assistant{align-self:flex-start;background:#1a1a1a;border:1px solid #333;border-bottom-left-radius:4px}
.msg code{background:#000;padding:2px 6px;border-radius:4px;font-family:'Fira Code',monospace;font-size:13px}
.input-area{padding:16px 24px;border-top:1px solid #222;display:flex;gap:12px}
.input-area textarea{flex:1;background:#111;border:1px solid #333;color:#e0e0e0;padding:12px;border-radius:8px;font-size:14px;font-family:inherit;resize:none;outline:none;min-height:48px;max-height:200px}
.input-area textarea:focus{border-color:#4a8fd4}
.input-area button{background:#4a8fd4;color:#fff;border:none;padding:12px 24px;border-radius:8px;cursor:pointer;font-size:14px;font-weight:500}
.input-area button:hover{background:#5a9fe4}
.input-area button:disabled{opacity:0.5;cursor:not-allowed}
.typing{color:#666;font-style:italic;padding:4px 16px}
</style>
</head>
<body>
<div class="header">
<h1>Polypod</h1>
<span>AI Gateway</span>
</div>
<div class="messages" id="messages"></div>
<div class="input-area">
<textarea id="input" placeholder="Digite sua mensagem..." rows="1" onkeydown="if(event.key==='Enter'&&!event.shiftKey){event.preventDefault();send()}"></textarea>
<button id="btn" onclick="send()">Enviar</button>
</div>
<script>
const msgs=document.getElementById('messages'),input=document.getElementById('input'),btn=document.getElementById('btn');
function addMsg(role,text){const d=document.createElement('div');d.className='msg '+role;d.textContent=text;msgs.appendChild(d);msgs.scrollTop=msgs.scrollHeight;return d}
async function send(){
const text=input.value.trim();if(!text)return;
input.value='';btn.disabled=true;
addMsg('user',text);
const el=addMsg('assistant','');
try{
const res=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({message:text})});
const reader=res.body.getReader();const dec=new TextDecoder();let buf='';
while(true){const{done,value}=await reader.read();if(done)break;
buf+=dec.decode(value);const lines=buf.split('\n');buf=lines.pop()||'';
for(const line of lines){if(!line.startsWith('data: '))continue;
try{const d=JSON.parse(line.slice(6));if(d.type==='delta')el.textContent+=d.content;if(d.type==='error')el.textContent+='Erro: '+d.error;}catch(e){}}}
}catch(e){el.textContent='Erro: '+e.message}
btn.disabled=false;input.focus();msgs.scrollTop=msgs.scrollHeight}
input.focus();
</script>
</body>
</html>`
