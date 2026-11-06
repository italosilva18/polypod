package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// CDP connects only to an explicitly configured local debugging endpoint.
// It never starts a browser, changes its profile, or enables remote debugging.
type CDP struct{ Endpoint string }
type Tab struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	Type         string `json:"type"`
	WebSocketURL string `json:"webSocketDebuggerUrl,omitempty"`
}
type Observation struct {
	TabID      string    `json:"tab_id"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Text       string    `json:"text"`
	Elements   []Element `json:"elements"`
	Truncated  bool      `json:"truncated"`
	Screenshot string    `json:"-"`
	Warning    string    `json:"warning,omitempty"`
}
type Element struct {
	Selector string  `json:"selector"`
	Tag      string  `json:"tag"`
	Role     string  `json:"role"`
	Name     string  `json:"name"`
	Disabled bool    `json:"disabled"`
	Checked  bool    `json:"checked"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}
type Action struct {
	TabID        string
	Kind         string
	Selector     string
	Text         string
	ExpectedText string
	ExpectedURL  string
}
type ActionResult struct {
	Executed     bool         `json:"executed"`
	Verified     bool         `json:"verified"`
	Verification string       `json:"verification"`
	Observation  *Observation `json:"observation,omitempty"`
}

func localURL(raw string, schemes ...string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, s := range schemes {
		if u.Scheme == s {
			allowed = true
		}
	}
	if !allowed || u.User != nil || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") {
		return nil, fmt.Errorf("CDP requer endpoint HTTP/WebSocket local")
	}
	return u, nil
}
func (c CDP) Tabs(ctx context.Context) ([]Tab, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9222"
	}
	u, err := localURL(endpoint, "http")
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/json/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirecionamento CDP recusado") }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CDP indisponível: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDP HTTP %d", resp.StatusCode)
	}
	var all []Tab
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&all); err != nil {
		return nil, err
	}
	var tabs []Tab
	for _, tab := range all {
		if tab.Type == "page" {
			tabs = append(tabs, tab)
		}
	}
	return tabs, nil
}
func (c CDP) connect(ctx context.Context, tabID string) (*cdpSession, Tab, error) {
	tabs, err := c.Tabs(ctx)
	if err != nil {
		return nil, Tab{}, err
	}
	var selected *Tab
	for i := range tabs {
		if tabs[i].ID == tabID || (tabID == "" && len(tabs) == 1) {
			selected = &tabs[i]
			break
		}
	}
	if selected == nil {
		return nil, Tab{}, fmt.Errorf("informe tab_id de uma aba observada; há %d abas", len(tabs))
	}
	if _, err := localURL(selected.WebSocketURL, "ws"); err != nil {
		return nil, Tab{}, err
	}
	cfg, err := websocket.NewConfig(selected.WebSocketURL, "http://localhost")
	if err != nil {
		return nil, Tab{}, err
	}
	conn, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, Tab{}, err
	}
	conn.MaxPayloadBytes = 16 << 20
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return &cdpSession{conn: conn, stop: stop}, *selected, nil
}

type cdpSession struct {
	conn *websocket.Conn
	id   int
	stop func() bool
}

func (s *cdpSession) close() { s.stop(); s.conn.Close() }
func (s *cdpSession) call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetDeadline(deadline); err != nil {
		return err
	}
	s.id++
	if err := websocket.JSON.Send(s.conn, map[string]any{"id": s.id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := websocket.JSON.Receive(s.conn, &reply); err != nil {
			return err
		}
		if reply.ID != s.id {
			continue
		}
		if reply.Error != nil {
			return fmt.Errorf("CDP %s: %s", method, reply.Error.Message)
		}
		return json.Unmarshal(reply.Result, out)
	}
}
func (s *cdpSession) evaluate(ctx context.Context, expression string, out any) error {
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if err := s.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &result); err != nil {
		return err
	}
	if len(result.Exception) > 0 {
		return fmt.Errorf("DOM indisponível ou alterado durante a operação")
	}
	return json.Unmarshal(result.Result.Value, out)
}
func (s *cdpSession) observe(ctx context.Context, id string) (*Observation, error) {
	var obs Observation
	if err := s.evaluate(ctx, observeDOM, &obs); err != nil {
		return nil, err
	}
	obs.TabID = id
	var shot struct {
		Data string `json:"data"`
	}
	if err := s.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}, &shot); err != nil {
		obs.Warning = "Captura visual indisponível: " + err.Error()
	} else {
		obs.Screenshot = shot.Data
	}
	return &obs, nil
}
func (c CDP) Observe(ctx context.Context, tabID string) (*Observation, error) {
	s, tab, err := c.connect(ctx, tabID)
	if err != nil {
		return nil, err
	}
	defer s.close()
	return s.observe(ctx, tab.ID)
}
func (c CDP) Act(ctx context.Context, a Action) (*ActionResult, error) {
	if a.Kind != "click" && a.Kind != "fill" {
		return nil, fmt.Errorf("ação deve ser click ou fill")
	}
	if a.Selector == "" {
		return nil, fmt.Errorf("selector obrigatório")
	}
	s, tab, err := c.connect(ctx, a.TabID)
	if err != nil {
		return nil, err
	}
	defer s.close()
	input, _ := json.Marshal(map[string]string{"kind": a.Kind, "selector": a.Selector, "text": a.Text})
	var outcome struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := s.evaluate(ctx, "("+actDOM+")("+string(input)+")", &outcome); err != nil {
		return &ActionResult{Verification: "Resultado da ação desconhecido; observe antes de tentar novamente."}, err
	}
	if !outcome.OK {
		return nil, fmt.Errorf("ação recusada: %s", outcome.Error)
	}
	result := &ActionResult{Executed: true, Verification: "Ação executada; confira a observação para verificar o efeito esperado."}
	obs, err := s.observe(ctx, tab.ID)
	if err != nil {
		result.Verification = "Ação executada; observação pendente: " + err.Error()
		return result, nil
	}
	result.Observation = obs
	if a.ExpectedText != "" || a.ExpectedURL != "" {
		result.Verified = (a.ExpectedText == "" || strings.Contains(obs.Text, a.ExpectedText)) && (a.ExpectedURL == "" || obs.URL == a.ExpectedURL)
		if result.Verified {
			result.Verification = "Expectativa confirmada na observação posterior."
		} else {
			result.Verification = "Expectativa ainda não confirmada; observe novamente sem repetir a ação."
		}
	}
	return result, nil
}

const observeDOM = `(() => {
 const visible=e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return r.width>0&&r.height>0&&s.display!=='none'&&s.visibility!=='hidden'};
 const selector=e=>{if(e.id&&document.querySelectorAll('#'+CSS.escape(e.id)).length===1)return '#'+CSS.escape(e.id);let p=[];while(e&&e.nodeType===1){let tag=e.tagName.toLowerCase(),i=1,n=e;while((n=n.previousElementSibling))if(n.tagName===e.tagName)i++;p.unshift(tag+':nth-of-type('+i+')');e=e.parentElement}return p.join(' > ')};
 const all=[...document.querySelectorAll('a,button,input,textarea,select,[role],[contenteditable="true"]')].filter(visible);
 return {url:location.href,title:document.title,text:(document.body?.innerText||'').slice(0,20000),truncated:all.length>150,elements:all.slice(0,150).map(e=>{const r=e.getBoundingClientRect();return {selector:selector(e),tag:e.tagName.toLowerCase(),role:e.getAttribute('role')||'',name:(e.getAttribute('aria-label')||e.labels?.[0]?.innerText||e.innerText||e.getAttribute('placeholder')||'').slice(0,200),disabled:!!e.disabled||e.getAttribute('aria-disabled')==='true',checked:!!e.checked,x:r.x+r.width/2,y:r.y+r.height/2}})};
})()`
const actDOM = `a=>{
 let list;try{list=document.querySelectorAll(a.selector)}catch{return {ok:false,error:'seletor inválido'}}
 if(list.length!==1)return {ok:false,error:'seletor deve identificar exatamente um elemento'};
 const e=list[0];if(e.disabled||e.getAttribute('aria-disabled')==='true')return {ok:false,error:'elemento desabilitado'};
 e.scrollIntoView({block:'center',inline:'center'});const r=e.getBoundingClientRect(),s=getComputedStyle(e);
 if(!r.width||!r.height||s.visibility==='hidden'||s.display==='none')return {ok:false,error:'elemento invisível'};
 const hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);if(!hit||(hit!==e&&!e.contains(hit)))return {ok:false,error:'elemento encoberto'};
 if(a.kind==='click'){e.click();return {ok:true}};
 if(!(e instanceof HTMLInputElement||e instanceof HTMLTextAreaElement)||e.readOnly||['file','password','checkbox','radio','hidden','submit','button'].includes(e.type))return {ok:false,error:'campo não suportado; use entrada desktop'};
 e.focus();const proto=e instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(proto,'value').set.call(e,a.text);e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return {ok:e.value===a.text,error:'valor não aplicado'};
}`
