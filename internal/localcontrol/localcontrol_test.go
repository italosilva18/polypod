package localcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/humanloop"
)

func TestQRCodeMatrixAndANSIRendering(t *testing.T) {
	url := "http://192.168.1.42:8789/#token=0123456789abcdef0123456789abcdef"
	mat, err := EncodeQRMatrix(url)
	if err != nil {
		t.Fatalf("EncodeQRMatrix: %v", err)
	}
	size := len(mat)
	if size < 21 || (size-17)%4 != 0 {
		t.Fatalf("invalid QR symbol dimension %d", size)
	}
	// Verify top-left, top-right, and bottom-left 7x7 finder corners are dark.
	for _, corner := range [][2]int{{0, 0}, {0, size - 7}, {size - 7, 0}} {
		r, c := corner[0], corner[1]
		if !mat[r][c] || !mat[r+6][c+6] || !mat[r+3][c+3] {
			t.Fatalf("finder pattern corrupted at (%d,%d)", r, c)
		}
	}

	ansiOut, err := RenderQRANSI(url)
	if err != nil {
		t.Fatalf("RenderQRANSI: %v", err)
	}
	if !strings.ContainsAny(ansiOut, "█▀▄ ") {
		t.Fatalf("expected Unicode half-block QR output, got %q", ansiOut)
	}
}

func TestWebShellAuthChatAndTokenRevocation(t *testing.T) {
	qQueue := humanloop.NewMultiChildQuestionQueue()
	srv, err := NewServer(Config{
		ListenAddr:   "127.0.0.1:0",
		LocalControl: true,
		Workspace:    "/tmp/poly-ws",
		ModelLabel:   "qwen/qwen3-coder-plus",
		Questions:    qQueue,
		Executor: func(ctx context.Context, prompt string, onDelta func(string)) (string, error) {
			onDelta("Olá ")
			onDelta("mobile!")
			return "Olá mobile! Recebi: " + prompt, nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	pairURL := srv.PairingURL("192.168.0.10:8789")
	if !strings.Contains(pairURL, "/#token="+srv.Token()) || strings.Contains(pairURL, "?token=") {
		t.Fatalf("expected fragment-only #token= URL (never query string), got %q", pairURL)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Unauthenticated call to /api/state must return 401.
	res, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", res.StatusCode)
	}

	// 2. Authenticated POST /api/chat executes prompt and appends transcript.
	oldToken := srv.Token()
	reqBody := bytes.NewBufferString(`{"prompt":"status do build"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/chat", reqBody)
	req.Header.Set("Authorization", "Bearer "+oldToken)
	req.Header.Set("Content-Type", "application/json")
	chatRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/chat: %v", err)
	}
	chatRes.Body.Close()
	if chatRes.StatusCode != http.StatusOK && chatRes.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 200/202 on authenticated /api/chat, got %d", chatRes.StatusCode)
	}

	// Wait briefly for the async turn goroutine to finish and verify /api/state.
	var payload struct {
		Busy       bool              `json:"busy"`
		Transcript []TranscriptEntry `json:"transcript"`
	}
	for attempt := 0; attempt < 40; attempt++ {
		stateReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/state", nil)
		stateReq.Header.Set("Authorization", "Bearer "+oldToken)
		stateRes, err := http.DefaultClient.Do(stateReq)
		if err != nil {
			t.Fatalf("GET /api/state auth: %v", err)
		}
		if err := json.NewDecoder(stateRes.Body).Decode(&payload); err != nil {
			stateRes.Body.Close()
			t.Fatalf("decode state: %v", err)
		}
		stateRes.Body.Close()
		if !payload.Busy && len(payload.Transcript) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(payload.Transcript) != 2 || !strings.Contains(payload.Transcript[1].Content, "Olá mobile!") {
		t.Fatalf("unexpected transcript: %+v", payload.Transcript)
	}

	// 3. POST /api/revoke immediately invalidates oldToken!
	revReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/revoke", nil)
	revReq.Header.Set("Authorization", "Bearer "+oldToken)
	revRes, err := http.DefaultClient.Do(revReq)
	if err != nil {
		t.Fatalf("POST /api/revoke: %v", err)
	}
	revRes.Body.Close()
	if revRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /api/revoke, got %d", revRes.StatusCode)
	}

	checkOld, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/state", nil)
	checkOld.Header.Set("Authorization", "Bearer "+oldToken)
	oldAfterRevoke, err := http.DefaultClient.Do(checkOld)
	if err != nil {
		t.Fatalf("recheck old token: %v", err)
	}
	oldAfterRevoke.Body.Close()
	if oldAfterRevoke.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected old token to be rejected with 401 after revoke, got %d", oldAfterRevoke.StatusCode)
	}
}
