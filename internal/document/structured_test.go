package document

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStructuredOfficeIntegration(t *testing.T) {
	python := os.Getenv("POLYPOD_DOCUMENT_PYTHON")
	if python == "" {
		t.Skip("private document Python environment not configured")
	}
	dir := t.TempDir()
	generator := exec.Command(python, "-c", `
import os, re, sys, zipfile
from openpyxl import Workbook
from docx import Document
from pptx import Presentation
from pptx.util import Inches
root = sys.argv[1]
book = Workbook()
sheet = book.active
sheet.title = "Contas"
sheet["A1"] = "Receita"
sheet["B2"] = "=SUM(1,2)"
book.create_sheet("Resumo")["C3"] = 17
xlsx = os.path.join(root, "fixture.xlsx")
book.save(xlsx)
with zipfile.ZipFile(xlsx) as source:
    files = [(entry, source.read(entry.filename)) for entry in source.infolist()]
with zipfile.ZipFile(xlsx, "w") as target:
    for entry, data in files:
        if entry.filename == "xl/worksheets/sheet1.xml":
            data, count = re.subn(rb"(<f>SUM\(1,2\)</f><v>)[^<]*(</v>)", rb"\g<1>3\g<2>", data)
            assert count == 1
        target.writestr(entry, data)
doc = Document()
doc.add_paragraph("Parágrafo Polypod")
table = doc.add_table(rows=1, cols=2)
table.cell(0, 0).text = "Produto"
table.cell(0, 1).text = "Valor"
doc.save(os.path.join(root, "fixture.docx"))
deck = Presentation()
slide = deck.slides.add_slide(deck.slide_layouts[6])
slide.shapes.add_textbox(Inches(1), Inches(1), Inches(4), Inches(1)).text = "Título Polypod"
slide.notes_slide.notes_text_frame.text = "Notas do apresentador"
deck.save(os.path.join(root, "fixture.pptx"))
`, dir)
	if out, err := generator.CombinedOutput(); err != nil {
		t.Fatalf("creating OOXML fixtures: %v: %s", err, out)
	}
	for _, tc := range []struct {
		file string
		want []string
	}{
		{"fixture.xlsx", []string{"[Planilha \"Contas\"]", "A1: \"Receita\"", "B2: fórmula \"=SUM(1,2)\"; valor armazenado 3", "[Planilha \"Resumo\"]", "C3: 17"}},
		{"fixture.docx", []string{"[Parágrafo] \"Parágrafo Polypod\"", "[Tabela]", "\"Produto\" | \"Valor\""}},
		{"fixture.pptx", []string{"[Slide 1]", "[Texto] \"Título Polypod\"", "[Notas] \"Notas do apresentador\""}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got, err := Extract(context.Background(), filepath.Join(dir, tc.file), Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, phrase := range tc.want {
				if !strings.Contains(got.Text, phrase) {
					t.Fatalf("%s missing %q: %s", tc.file, phrase, got.Text)
				}
			}
		})
	}
}

func TestStructuredArchiveRejectsEntryBomb(t *testing.T) {
	path := filepath.Join(t.TempDir(), "many.xlsx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for i := 0; i <= maxOfficeEntries; i++ {
		if _, err := archive.Create(fmt.Sprintf("entry-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateStructuredOfficeArchive(path); err == nil {
		t.Fatal("accepted an Office archive with too many entries")
	}
}

func TestPDFPerPageSelectionAndOCR(t *testing.T) {
	requireCommands(t, "pdfinfo", "pdftotext", "pdftoppm", "tesseract")
	path := filepath.Join(t.TempDir(), "mixed.pdf")
	if err := writeTwoPagePDF(path, "First page has enough selectable text", "SCAN"); err != nil {
		t.Fatal(err)
	}
	all, err := Extract(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Pages != 2 || all.PageStart != 1 || all.PageLimit != 2 || !all.UsedOCR ||
		!strings.Contains(all.Text, "[Página 1]") || !strings.Contains(all.Text, "[Página 2]") {
		t.Fatalf("mixed PDF extraction = %+v", all)
	}
	second, err := Extract(context.Background(), path, Options{StartPage: 2, EndPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.PageStart != 2 || second.PageLimit != 1 || strings.Contains(second.Text, "[Página 1]") ||
		!strings.Contains(second.Text, "[Página 2]") {
		t.Fatalf("selected PDF page = %+v", second)
	}
	if _, err := Extract(context.Background(), path, Options{StartPage: 3}); err == nil {
		t.Fatal("accepted an out-of-range PDF page")
	}
}

func writeTwoPagePDF(path, first, second string) error {
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	firstStream := fmt.Sprintf("BT /F1 18 Tf 40 150 Td (%s) Tj ET\n", first)
	secondStream := fmt.Sprintf("BT /F1 18 Tf 40 150 Td (%s) Tj ET\n", second)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 500 300] /Resources << /Font << /F1 7 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 500 300] /Resources << /Font << /F1 7 0 R >> >> /Contents 6 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(firstStream), firstStream),
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(secondStream), secondStream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return os.WriteFile(path, pdf.Bytes(), 0600)
}
