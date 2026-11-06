package ai

import (
	"fmt"

	openai "github.com/sashabaranov/go-openai"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/types"
)

// executeSkillResult keeps legacy skill registries usable without discarding
// structured results from registries that support images.
func executeSkillResult(reg SkillRegistry, name, args string) (types.ToolResult, error) {
	if rich, ok := reg.(interface {
		ExecuteResult(string, string) (types.ToolResult, error)
	}); ok {
		return rich.ExecuteResult(name, args)
	}
	text, err := reg.Execute(name, args)
	return types.ToolResult{Content: text, IsError: err != nil}, err
}

// Observations follow ALL tool responses in a batch. Inserting a user message
// between tool responses breaks the tool-call protocol on several providers.
func imageObservations(results []ToolCallResult) []provider.Message {
	var messages []provider.Message
	for _, result := range results {
		if result.IsError || len(result.Images) == 0 {
			continue
		}
		label := fmt.Sprintf("Observação visual da ferramenta %s (chamada %s). Verifique o estado observado antes da próxima ação.", result.Name, result.ID)
		parts := []provider.ContentPart{{Type: "text", Text: label}}
		for _, img := range result.Images {
			if img.Base64 == "" {
				continue
			}
			parts = append(parts, provider.ContentPart{Type: "image_base64", ImageB64: img.Base64, MimeType: img.MIMEType})
		}
		messages = append(messages, provider.Message{Role: "user", Parts: parts})
	}
	return messages
}

func openAIImageObservations(results []ToolCallResult) []openai.ChatCompletionMessage {
	var messages []openai.ChatCompletionMessage
	for _, msg := range imageObservations(results) {
		messages = append(messages, openai.ChatCompletionMessage{Role: msg.Role, MultiContent: openAIContentParts(msg.Parts)})
	}
	return messages
}

func openAIContentParts(parts []provider.ContentPart) []openai.ChatMessagePart {
	var output []openai.ChatMessagePart
	for _, part := range parts {
		switch part.Type {
		case "text":
			output = append(output, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: part.Text})
		case "image_base64":
			output = append(output, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:" + part.MimeType + ";base64," + part.ImageB64}})
		case "image_url":
			output = append(output, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: part.ImageURL}})
		}
	}
	return output
}

func openAIChatMessage(msg ChatMessage) openai.ChatCompletionMessage {
	result := openai.ChatCompletionMessage{Role: msg.Role, Content: msg.Content}
	if len(msg.Parts) > 0 {
		result.Content = ""
		if msg.Content != "" {
			result.MultiContent = append(result.MultiContent, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: msg.Content})
		}
		result.MultiContent = append(result.MultiContent, openAIContentParts(msg.Parts)...)
	}
	return result
}
