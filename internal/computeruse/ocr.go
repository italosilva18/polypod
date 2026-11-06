package computeruse

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const maxOCRLines = 240

type ocrLine struct {
	page, block, paragraph, line int
	x, y, width, height          int
	words                        []string
}

// DescribeScreenshot extracts visible text and click coordinates from an
// image. The textual map is returned to non-vision models so they can inspect
// and operate ordinary desktop interfaces.
func DescribeScreenshot(ctx context.Context, imageData []byte) (string, error) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", fmt.Errorf("tesseract nao instalado")
	}
	cmd := exec.CommandContext(ctx, "tesseract", "stdin", "stdout", "-l", "por+eng", "tsv")
	cmd.Stdin = bytes.NewReader(imageData)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("OCR da tela: %w", err)
	}
	lines := parseOCRTSV(string(out))
	if len(lines) == 0 {
		return "Nenhum texto visivel foi reconhecido.", nil
	}
	var result strings.Builder
	result.WriteString("Mapa visual OCR. Coordenadas x,y indicam o centro clicavel de cada linha:\n")
	for i, line := range lines {
		if i >= maxOCRLines {
			fmt.Fprintf(&result, "... %d linhas adicionais omitidas\n", len(lines)-i)
			break
		}
		centerX := line.x + line.width/2
		centerY := line.y + line.height/2
		fmt.Fprintf(&result, "- [x=%d y=%d box=%d,%d,%d,%d] %s\n",
			centerX, centerY, line.x, line.y, line.width, line.height,
			strings.Join(line.words, " "))
	}
	return result.String(), nil
}

func parseOCRTSV(raw string) []ocrLine {
	grouped := make(map[[4]int]*ocrLine)
	order := make([][4]int, 0)
	for index, row := range strings.Split(raw, "\n") {
		if index == 0 || strings.TrimSpace(row) == "" {
			continue
		}
		fields := strings.SplitN(row, "\t", 12)
		if len(fields) != 12 || fields[0] != "5" {
			continue
		}
		text := strings.TrimSpace(fields[11])
		if text == "" {
			continue
		}
		confidence, err := strconv.ParseFloat(fields[10], 64)
		if err != nil || confidence < 25 {
			continue
		}
		values := make([]int, 9)
		valid := true
		for i := 1; i <= 9; i++ {
			values[i-1], err = strconv.Atoi(fields[i])
			if err != nil {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		key := [4]int{values[0], values[1], values[2], values[3]}
		wordX, wordY, wordW, wordH := values[5], values[6], values[7], values[8]
		line, ok := grouped[key]
		if !ok {
			line = &ocrLine{
				page: values[0], block: values[1], paragraph: values[2], line: values[3],
				x: wordX, y: wordY, width: wordW, height: wordH,
			}
			grouped[key] = line
			order = append(order, key)
		} else {
			right := maxInt(line.x+line.width, wordX+wordW)
			bottom := maxInt(line.y+line.height, wordY+wordH)
			line.x = minInt(line.x, wordX)
			line.y = minInt(line.y, wordY)
			line.width = right - line.x
			line.height = bottom - line.y
		}
		line.words = append(line.words, text)
	}

	sort.SliceStable(order, func(i, j int) bool {
		a, b := grouped[order[i]], grouped[order[j]]
		if a.y != b.y {
			return a.y < b.y
		}
		return a.x < b.x
	})
	result := make([]ocrLine, 0, len(order))
	for _, key := range order {
		result = append(result, *grouped[key])
	}
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
