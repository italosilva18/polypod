package document

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestExtractTextAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("Olá, Polypod.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Extract(context.Background(), path, Options{})
	if err != nil || got.Text != "Olá, Polypod.\n" || got.Format != ".md" {
		t.Fatalf("extract text = %+v, %v", got, err)
	}

	binary := filepath.Join(dir, "binary.dat")
	if err := os.WriteFile(binary, []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), binary, Options{}); err == nil {
		t.Fatal("binary file was accepted")
	}
	oversize := filepath.Join(dir, "big.txt")
	file, err := os.Create(oversize)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxInputBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), oversize, Options{}); err == nil {
		t.Fatal("oversized file was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Extract(cancelled, path, Options{}); err == nil {
		t.Fatal("cancelled context was ignored")
	}
}

func TestExtractPDF(t *testing.T) {
	requireCommands(t, "pdfinfo", "pdftotext")
	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := writeFixturePDF(path, "Polypod PDF text extraction works"); err != nil {
		t.Fatal(err)
	}
	got, err := Extract(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "Polypod PDF text extraction works") || got.Pages != 1 || got.UsedOCR {
		t.Fatalf("unexpected PDF extraction: %+v", got)
	}
}

func TestExtractOfficeDOCX(t *testing.T) {
	requireCommands(t, "libreoffice", "pdfinfo", "pdftotext")
	path := filepath.Join(t.TempDir(), "fixture.docx")
	if err := writeFixtureDOCX(path); err != nil {
		t.Fatal(err)
	}
	got, err := Extract(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "Polypod Office document") || got.Format != ".docx" {
		t.Fatalf("unexpected DOCX extraction: %+v", got)
	}
}

func TestExtractImageOCR(t *testing.T) {
	requireCommands(t, "tesseract")
	small := image.NewRGBA(image.Rect(0, 0, 230, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 230; x++ {
			small.Set(x, y, color.White)
		}
	}
	d := font.Drawer{Dst: small, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13, Dot: fixed.P(10, 25)}
	d.DrawString("POLYPOD OCR TEST")
	large := image.NewRGBA(image.Rect(0, 0, 920, 160))
	draw.NearestNeighbor.Scale(large, large.Bounds(), small, small.Bounds(), draw.Src, nil)
	path := filepath.Join(t.TempDir(), "fixture.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, large); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := Extract(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(got.Text), "POLYPOD") || !got.UsedOCR {
		t.Fatalf("unexpected image OCR: %+v", got)
	}
}

func requireCommands(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s unavailable", name)
		}
	}
}

func writeFixturePDF(path, message string) error {
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 500 300] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	stream := fmt.Sprintf("BT /F1 18 Tf 40 150 Td (%s) Tj ET\n", message)
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
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

func writeFixtureDOCX(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Polypod Office document</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, body := range parts {
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return file.Close()
}
