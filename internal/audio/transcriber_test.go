package audio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPTranscriberSendsBoundedAudioAndParsesTranscript(t *testing.T) {
	const key = "fixture-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("unexpected method or auth header")
		}
		if err := r.ParseMultipartForm(MaxAudioBytes + 1024); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		if got := r.FormValue("model"); got != "whisper-test" {
			t.Errorf("model = %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("audio file: %v", err)
			return
		}
		defer file.Close()
		if header.Filename != "audio.ogg" {
			t.Errorf("filename = %q", header.Filename)
		}
		var buf [8]byte
		n, err := file.Read(buf[:])
		if err != nil && n == 0 {
			t.Errorf("read file: %v", err)
		}
		if string(buf[:n]) != "OggSdata" {
			t.Errorf("audio payload changed")
		}
		_, _ = w.Write([]byte(`{"text":" Olá mundo "}`))
	}))
	defer server.Close()
	tr, err := NewHTTPTranscriber(server.URL, "whisper-test", key, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.Transcribe(context.Background(), []byte("OggSdata"), "audio/ogg; codecs=opus")
	if err != nil || got != "Olá mundo" {
		t.Fatalf("transcript = %q, %v", got, err)
	}
}

func TestHTTPTranscriberRejectsUnsupportedAndInsecureMedia(t *testing.T) {
	if _, err := NewHTTPTranscriber("http://example.com/audio/transcriptions", "m", "key", nil); err == nil {
		t.Fatal("remote cleartext endpoint accepted")
	}
	if _, err := NewHTTPTranscriber("https://example.com/audio/transcriptions", "m", "", nil); err == nil {
		t.Fatal("remote endpoint without key accepted")
	}
	tr, err := NewHTTPTranscriber("http://127.0.0.1:9999/audio/transcriptions", "m", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mediaType := range []string{"audio/ogg", "application/pdf"} {
		if _, err := tr.Transcribe(context.Background(), []byte("bad"), mediaType); !errors.Is(err, ErrUnsupportedAudio) {
			t.Fatalf("%s: expected unsupported media, got %v", mediaType, err)
		}
	}
	if _, err := tr.Transcribe(context.Background(), []byte("OggS"+strings.Repeat("x", MaxAudioBytes)), "audio/ogg"); !errors.Is(err, ErrUnsupportedAudio) {
		t.Fatalf("oversized audio: %v", err)
	}
}

func TestHTTPTranscriberDoesNotFollowRedirectOrExposeErrorBody(t *testing.T) {
	calledRedirect := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			calledRedirect = true
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte("secret-response"))
	}))
	defer server.Close()
	tr, err := NewHTTPTranscriber(server.URL, "m", "key", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tr.Transcribe(context.Background(), []byte("OggSdata"), "audio/ogg")
	if err == nil || calledRedirect || strings.Contains(err.Error(), "secret-response") {
		t.Fatalf("redirect or body leaked: %v, called=%t", err, calledRedirect)
	}
}
