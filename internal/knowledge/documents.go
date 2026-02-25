package knowledge

import (
	"strings"
	"unicode/utf8"
)

const (
	DefaultChunkSize    = 500  // characters
	DefaultChunkOverlap = 50   // characters overlap between chunks
)

// Chunk represents a piece of a document.
type Chunk struct {
	Source     string
	Title     string
	Index     int
	Content   string
}

// ChunkText splits text into overlapping chunks.
func ChunkText(source, title, text string, chunkSize, overlap int) []Chunk {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	if overlap < 0 {
		overlap = DefaultChunkOverlap
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Try splitting by paragraphs first
	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	var current strings.Builder
	idx := 0

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		if current.Len()+utf8.RuneCountInString(para) > chunkSize && current.Len() > 0 {
			chunks = append(chunks, Chunk{
				Source:  source,
				Title:   title,
				Index:   idx,
				Content: current.String(),
			})
			idx++

			// Keep overlap from end of current chunk
			content := current.String()
			current.Reset()
			if overlap > 0 && len(content) > overlap {
				current.WriteString(content[len(content)-overlap:])
				current.WriteString(" ")
			}
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}

	if current.Len() > 0 {
		chunks = append(chunks, Chunk{
			Source:  source,
			Title:   title,
			Index:   idx,
			Content: current.String(),
		})
	}

	return chunks
}
