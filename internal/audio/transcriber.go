// Package audio provides bounded speech-to-text adapters for remote media.
package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxAudioBytes = 16 << 20
	maxReplyBytes = 1 << 20
)

var ErrUnsupportedAudio = errors.New("formato de áudio não suportado")

// HTTPTranscriber calls an explicitly configured OpenAI-compatible audio
// transcription endpoint. No media is sent unless the caller constructs it.
type HTTPTranscriber struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

func NewHTTPTranscriber(endpoint, model, apiKey string, client *http.Client) (*HTTPTranscriber, error) {
	endpoint, model = strings.TrimSpace(endpoint), strings.TrimSpace(model)
	if endpoint == "" || model == "" {
		return nil, errors.New("endpoint e modelo de transcrição são obrigatórios")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("endpoint de transcrição inválido")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("endpoint de transcrição remoto exige HTTPS")
		}
	} else if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("chave de transcrição remota não configurada")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPTranscriber{endpoint: endpoint, model: model, apiKey: apiKey, client: &copyClient}, nil
}

func (t *HTTPTranscriber) Transcribe(ctx context.Context, data []byte, declaredMIME string) (string, error) {
	if len(data) == 0 || len(data) > MaxAudioBytes {
		return "", ErrUnsupportedAudio
	}
	mediaType, _, err := mime.ParseMediaType(declaredMIME)
	if err != nil {
		return "", ErrUnsupportedAudio
	}
	ext, valid := audioExtension(mediaType, data)
	if !valid {
		return "", ErrUnsupportedAudio
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", t.model); err != nil {
		return "", err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("file", "audio"+ext)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcrição remota indisponível: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Provider bodies can echo credentials and media; retain only the code.
		return "", fmt.Errorf("transcrição remota retornou HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes+1))
	if err != nil {
		return "", fmt.Errorf("ler resposta de transcrição: %w", err)
	}
	if len(raw) > maxReplyBytes {
		return "", errors.New("resposta de transcrição excede limite")
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decodificar transcrição: %w", err)
	}
	result.Text = strings.TrimSpace(result.Text)
	if !utf8.ValidString(result.Text) || result.Text == "" {
		return "", errors.New("transcrição vazia ou inválida")
	}
	return result.Text, nil
}

func audioExtension(mediaType string, data []byte) (string, bool) {
	switch mediaType {
	case "audio/ogg", "audio/opus":
		return ".ogg", bytes.HasPrefix(data, []byte("OggS"))
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav", len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
	case "audio/mpeg", "audio/mp3":
		return ".mp3", bytes.HasPrefix(data, []byte("ID3")) || len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0
	case "audio/flac", "audio/x-flac":
		return ".flac", bytes.HasPrefix(data, []byte("fLaC"))
	case "audio/webm":
		return ".webm", bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
	case "audio/mp4", "audio/m4a":
		return ".m4a", len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp"))
	default:
		return "", false
	}
}
