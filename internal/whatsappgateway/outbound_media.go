package whatsappgateway

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/costa/polypod/internal/humanloop"
)

const maxOutboundCaptionRunes = 4000

// OutboundMedia contains bytes loaded from the immutable private outbox blob.
type OutboundMedia struct {
	Kind     string
	MIME     string
	Filename string
	Data     []byte
}

// QueueMedia validates and persists media for a permitted chat. It only wakes
// the durable processor; no network send occurs in this method.
func (g *Gateway) QueueMedia(chatID, caption, filename, mimeType string, data []byte) (humanloop.Message, error) {
	if g == nil || g.store == nil || !g.chats[chatID] {
		return humanloop.Message{}, errors.New("whatsapp: chat de midia nao autorizado")
	}
	media, err := validateOutboundMedia(caption, filename, mimeType, data)
	if err != nil {
		return humanloop.Message{}, err
	}
	queued, err := g.store.EnqueueOutboundMedia(Channel, chatID, caption, "", humanloop.MediaAttachment{
		Kind: media.Kind, MIME: media.MIME, Filename: media.Filename,
	}, data)
	if err != nil {
		return humanloop.Message{}, fmt.Errorf("whatsapp: enfileirando midia: %w", err)
	}
	if g.wake != nil {
		g.wake()
	}
	return queued, nil
}

func validateOutboundMedia(caption, filename, mimeType string, data []byte) (OutboundMedia, error) {
	if len(data) == 0 || len(data) > humanloop.MaxOutboundMediaBytes {
		return OutboundMedia{}, errors.New("whatsapp: midia vazia ou excede 16 MiB")
	}
	if utf8.RuneCountInString(caption) > maxOutboundCaptionRunes {
		return OutboundMedia{}, errors.New("whatsapp: legenda de midia longa demais")
	}
	mediaType, params, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return OutboundMedia{}, errors.New("whatsapp: MIME de midia invalido")
	}
	if filename != "" && (filepath.Base(filename) != filename || strings.ContainsAny(filename, "\\/\x00") || !utf8.ValidString(filename) || len(filename) > 128) {
		return OutboundMedia{}, errors.New("whatsapp: nome de midia invalido")
	}
	if filename != "" {
		expected := outboundMIMEForExtension(strings.ToLower(filepath.Ext(filename)))
		if expected == "" || !strings.HasPrefix(expected, mediaType) {
			return OutboundMedia{}, errors.New("whatsapp: extensao do arquivo nao corresponde ao MIME")
		}
	}
	result := OutboundMedia{MIME: mediaType, Filename: filename, Data: data}
	switch mediaType {
	case "audio/ogg":
		if caption != "" || (params["codecs"] != "" && !strings.EqualFold(params["codecs"], "opus")) ||
			len(data) < 32 || !bytes.Equal(data[:4], []byte("OggS")) || !bytes.Contains(data[:min(len(data), 512)], []byte("OpusHead")) {
			return OutboundMedia{}, errors.New("whatsapp: voz deve ser OGG/Opus sem legenda")
		}
		result.Kind, result.MIME = "audio", "audio/ogg; codecs=opus"
		if filename == "" {
			result.Filename = "voz.ogg"
		}
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		if !matchesImageSignature(mediaType, data) {
			return OutboundMedia{}, errors.New("whatsapp: assinatura da imagem nao corresponde ao MIME")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 25_000_000 || imageMIME(format) != mediaType {
			return OutboundMedia{}, errors.New("whatsapp: imagem invalida ou grande demais")
		}
		result.Kind = "image"
		if filename == "" {
			result.Filename = "imagem" + mediaExtension(mediaType)
		}
	case "application/pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return OutboundMedia{}, errors.New("whatsapp: PDF invalido")
		}
		result.Kind = "document"
		if filename == "" {
			result.Filename = "documento.pdf"
		}
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		if err := validateOfficeArchive(mediaType, data); err != nil {
			return OutboundMedia{}, err
		}
		result.Kind = "document"
		if filename == "" {
			result.Filename = "documento" + mediaExtension(mediaType)
		}
	case "text/plain", "text/csv":
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return OutboundMedia{}, errors.New("whatsapp: texto de documento invalido")
		}
		result.Kind = "document"
		if filename == "" {
			result.Filename = "documento" + mediaExtension(mediaType)
		}
	default:
		return OutboundMedia{}, errors.New("whatsapp: formato de midia de saida nao suportado")
	}
	return result, nil
}

func matchesImageSignature(mimeType string, data []byte) bool {
	switch mimeType {
	case "image/png":
		return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return bytes.HasPrefix(data, []byte("\xff\xd8\xff"))
	case "image/gif":
		return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
	case "image/webp":
		return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
	}
	return false
}

func validateOfficeArchive(mimeType string, data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > 10_000 {
		return errors.New("whatsapp: arquivo Office invalido")
	}
	want := "word/document.xml"
	switch mimeType {
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		want = "xl/workbook.xml"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		want = "ppt/presentation.xml"
	}
	for _, file := range zr.File {
		if file.Name == want {
			return nil
		}
	}
	return errors.New("whatsapp: conteudo Office nao corresponde ao MIME")
}

func mediaExtension(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	}
	return ""
}

// QueueLocalFile is the agent-facing path adapter. The source can disappear
// after this returns because its bytes have already been copied into the outbox.
func (g *Gateway) QueueLocalFile(chatID, path, caption string) (humanloop.Message, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > humanloop.MaxOutboundMediaBytes {
		return humanloop.Message{}, errors.New("whatsapp: arquivo local inexistente, invalido ou grande demais")
	}
	f, err := os.Open(path)
	if err != nil {
		return humanloop.Message{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return humanloop.Message{}, errors.New("whatsapp: arquivo local alterado antes da leitura")
	}
	data, err := io.ReadAll(io.LimitReader(f, humanloop.MaxOutboundMediaBytes+1))
	if err != nil {
		return humanloop.Message{}, err
	}
	if len(data) == 0 || len(data) > humanloop.MaxOutboundMediaBytes {
		return humanloop.Message{}, errors.New("whatsapp: arquivo local excede limite")
	}
	name := filepath.Base(path)
	mimeType := outboundMIMEForExtension(strings.ToLower(filepath.Ext(name)))
	return g.QueueMedia(chatID, caption, name, mimeType, data)
}

func outboundMIMEForExtension(ext string) string {
	switch ext {
	case ".ogg":
		return "audio/ogg; codecs=opus"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	default:
		return ""
	}
}
