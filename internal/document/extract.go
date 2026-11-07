// Package document extracts bounded text from local documents using installed
// command-line converters. No shell is involved, and temporary files are private.
package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxInputBytes  = 25 << 20
	MaxOutputBytes = 4 << 20
	MaxPDFPages    = 100
	MaxOCRPages    = 20
	maxDuration    = 2 * time.Minute
)

type Options struct {
	OCR bool
	// StartPage and EndPage are 1-based PDF page numbers. Zero uses the default
	// beginning or the current per-call page limit. Other formats ignore them.
	StartPage int
	EndPage   int
	// OCRLanguage is a Tesseract language code such as "por+eng". Empty uses
	// Tesseract's installed default language.
	OCRLanguage string
}

type Result struct {
	Text         string
	Format       string
	Pages        int
	PageStart    int
	PageLimit    int
	UsedOCR      bool
	OCRTruncated bool
	TruncatedPDF bool
}

// Extract reads a regular local file. It bounds input, extracted output, PDF
// pages and total execution time; a caller can paginate the returned text.
func Extract(ctx context.Context, path string, opts Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(path) == "" {
		return Result{}, errors.New("caminho do documento obrigatório")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("abrir documento: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("o caminho deve apontar para um arquivo comum")
	}
	if info.Size() > MaxInputBytes {
		return Result{}, fmt.Errorf("documento excede %d MiB", MaxInputBytes>>20)
	}
	ctx, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	format := strings.ToLower(filepath.Ext(path))
	switch format {
	case ".txt", ".md", ".csv", ".tsv", ".json", ".yaml", ".yml", ".xml", ".html", ".htm", ".log":
		text, err := readText(path)
		return Result{Text: text, Format: format}, err
	case ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp":
		text, err := ocrImage(ctx, path, opts.OCRLanguage)
		return Result{Text: text, Format: format, Pages: 1, PageLimit: 1, UsedOCR: true}, err
	case ".pdf":
		return extractPDF(ctx, path, opts, format)
	case ".docx", ".xlsx", ".pptx":
		if strings.TrimSpace(os.Getenv("POLYPOD_DOCUMENT_PYTHON")) != "" {
			return extractStructuredOffice(ctx, path, format)
		}
		return extractOffice(ctx, path, opts, format)
	case ".doc", ".odt", ".rtf", ".xls", ".ods", ".ppt", ".odp":
		return extractOffice(ctx, path, opts, format)
	default:
		text, err := readText(path)
		if err != nil {
			return Result{}, fmt.Errorf("tipo de documento não suportado: %s: %w", format, err)
		}
		return Result{Text: text, Format: format}, nil
	}
}

func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > MaxOutputBytes {
		return "", fmt.Errorf("texto extraído excede %d MiB", MaxOutputBytes>>20)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", errors.New("arquivo binário ou texto não UTF-8")
	}
	return string(data), nil
}

func extractPDF(ctx context.Context, path string, opts Options, format string) (Result, error) {
	pages, err := pdfPages(ctx, path)
	if err != nil {
		return Result{}, err
	}
	if pages < 1 {
		return Result{}, errors.New("PDF sem páginas")
	}
	start := opts.StartPage
	if start == 0 {
		start = 1
	}
	if start < 1 || start > pages {
		return Result{}, fmt.Errorf("página inicial deve estar entre 1 e %d", pages)
	}
	maxPages := MaxPDFPages
	if opts.OCR {
		maxPages = MaxOCRPages
	}
	end := opts.EndPage
	if end == 0 {
		end = min(pages, start+maxPages-1)
	}
	if end < start || end > pages || end-start+1 > maxPages {
		return Result{}, fmt.Errorf("intervalo de páginas inválido; máximo de %d por chamada", maxPages)
	}
	result := Result{Format: format, Pages: pages, PageStart: start,
		PageLimit: end - start + 1, TruncatedPDF: end < pages}
	var text strings.Builder
	ocrCount := 0
	for page := start; page <= end; page++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		pageText := ""
		if !opts.OCR {
			pageText, err = runTextCommand(ctx, "pdftotext", "-f", strconv.Itoa(page),
				"-l", strconv.Itoa(page), "-layout", "-enc", "UTF-8", path, "-")
			if err != nil {
				return Result{}, fmt.Errorf("extrair texto da página %d: %w", page, err)
			}
		}
		if opts.OCR || len([]rune(strings.TrimSpace(pageText))) < 20 {
			if ocrCount < MaxOCRPages {
				ocrText, ocrErr := ocrPDFPage(ctx, path, page, opts.OCRLanguage)
				if ocrErr != nil {
					return Result{}, fmt.Errorf("OCR da página %d: %w", page, ocrErr)
				}
				if strings.TrimSpace(ocrText) != "" {
					pageText = ocrText
				}
				result.UsedOCR = true
				ocrCount++
			} else {
				result.OCRTruncated = true
				pageText = "[OCR omitido: limite de 20 páginas digitalizadas por chamada.]"
			}
		}
		entry := fmt.Sprintf("[Página %d]\n%s\n\n", page, strings.TrimSpace(pageText))
		if text.Len()+len(entry) > MaxOutputBytes {
			return Result{}, fmt.Errorf("texto extraído excede %d MiB", MaxOutputBytes>>20)
		}
		text.WriteString(entry)
	}
	result.Text = text.String()
	return result, nil
}

func extractOffice(ctx context.Context, path string, opts Options, format string) (Result, error) {
	tmp, err := os.MkdirTemp("", "polypod-document-*")
	if err != nil {
		return Result{}, fmt.Errorf("preparar conversão Office: %w", err)
	}
	defer os.RemoveAll(tmp)
	profileURL := (&url.URL{Scheme: "file", Path: filepath.Join(tmp, "profile")}).String()
	if _, err := runTextCommand(ctx, "libreoffice", "-env:UserInstallation="+profileURL,
		"--headless", "--convert-to", "pdf", "--outdir", tmp, path); err != nil {
		return Result{}, fmt.Errorf("converter Office: %w", err)
	}
	converted, err := filepath.Glob(filepath.Join(tmp, "*.pdf"))
	if err != nil || len(converted) != 1 {
		return Result{}, errors.New("LibreOffice não produziu um PDF único")
	}
	info, err := os.Stat(converted[0])
	if err != nil {
		return Result{}, fmt.Errorf("verificar PDF convertido: %w", err)
	}
	if info.Size() > MaxInputBytes {
		return Result{}, errors.New("PDF convertido excede o limite de tamanho")
	}
	result, err := extractPDF(ctx, converted[0], opts, format)
	return result, err
}

func pdfPages(ctx context.Context, path string) (int, error) {
	out, err := runTextCommand(ctx, "pdfinfo", path)
	if err != nil {
		return 0, fmt.Errorf("inspecionar PDF: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "Pages:") {
			continue
		}
		pages, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
		if parseErr != nil {
			return 0, fmt.Errorf("ler número de páginas: %w", parseErr)
		}
		return pages, nil
	}
	return 0, errors.New("pdfinfo não informou o número de páginas")
}

func ocrPDFPage(ctx context.Context, path string, page int, language string) (string, error) {
	tmp, err := os.MkdirTemp("", "polypod-document-ocr-*")
	if err != nil {
		return "", fmt.Errorf("preparar OCR: %w", err)
	}
	defer os.RemoveAll(tmp)
	prefix := filepath.Join(tmp, "page")
	if _, err := runTextCommand(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page),
		"-scale-to", "1800", "-png", "-singlefile", path, prefix); err != nil {
		return "", fmt.Errorf("renderizar página %d: %w", page, err)
	}
	return ocrImage(ctx, prefix+".png", language)
}

func ocrImage(ctx context.Context, path, language string) (string, error) {
	args := []string{path, "stdout"}
	if language != "" {
		args = append(args, "-l", language)
	}
	return runTextCommand(ctx, "tesseract", args...)
}

type boundedWriter struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.limit - w.Len()
	if remaining < len(p) {
		w.overflow = true
		p = p[:max(remaining, 0)]
	}
	_, err := w.Buffer.Write(p)
	return n, err
}

func runTextCommand(ctx context.Context, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s não instalado", name)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	stdout := &boundedWriter{limit: MaxOutputBytes}
	stderr := &boundedWriter{limit: 4096}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s interrompido: %w", name, ctx.Err())
		}
		return "", fmt.Errorf("%s falhou: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	if stdout.overflow {
		return "", fmt.Errorf("%s produziu mais de %d MiB", name, MaxOutputBytes>>20)
	}
	return stdout.String(), nil
}
