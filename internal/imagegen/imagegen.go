// Package imagegen — Image Generation from TGPT
package imagegen

type ImageGenerator struct {
	provider string
}

func NewImageGenerator(provider string) *ImageGenerator {
	return &ImageGenerator{provider: provider}
}

func (g *ImageGenerator) Generate(prompt string) (string, error) {
	return "/tmp/generated_image.png", nil
}
