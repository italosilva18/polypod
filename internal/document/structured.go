package document

import (
	"archive/zip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	maxExpandedOfficeBytes = 128 << 20
	maxOfficeEntries       = 5000
)

//go:embed structured_worker.py
var structuredWorker string

// extractStructuredOffice uses an isolated Python environment selected by the
// operator. The embedded worker reads document structure without executing
// spreadsheet formulas or fetching linked content. It prints bounded text.
func extractStructuredOffice(ctx context.Context, path, format string) (Result, error) {
	if err := validateStructuredOfficeArchive(path); err != nil {
		return Result{}, err
	}
	python := strings.TrimSpace(os.Getenv("POLYPOD_DOCUMENT_PYTHON"))
	if python == "" {
		return Result{}, errors.New("Python estruturado não configurado")
	}
	text, err := runTextCommand(ctx, python, "-c", structuredWorker, format, path)
	if err != nil {
		return Result{}, fmt.Errorf("extrair estrutura de %s: %w", format, err)
	}
	return Result{Text: text, Format: format}, nil
}

func validateStructuredOfficeArchive(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("abrir pacote Office: %w", err)
	}
	defer reader.Close()
	if len(reader.File) == 0 || len(reader.File) > maxOfficeEntries {
		return errors.New("pacote Office excede limite de entradas")
	}
	var total uint64
	for _, entry := range reader.File {
		if entry.Flags&1 != 0 {
			return errors.New("pacote Office criptografado não suportado")
		}
		if entry.UncompressedSize64 > maxExpandedOfficeBytes ||
			total > maxExpandedOfficeBytes-entry.UncompressedSize64 {
			return errors.New("pacote Office excede limite de dados expandidos")
		}
		total += entry.UncompressedSize64
	}
	return nil
}
