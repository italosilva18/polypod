package ai

import (
	"context"
	"encoding/json"
	"fmt"

	openai "github.com/sashabaranov/go-openai"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/workdir"
)

// executeSkillResult keeps legacy skill registries usable without discarding
// structured results from registries that support images.
func executeSkillResult(ctx context.Context, reg SkillRegistry, name, args string) (types.ToolResult, error) {
	if _, isAdapter := reg.(*ToolRegistryAdapter); !isAdapter {
		if st, ok := tool.SessionToolFromContext(ctx, name); ok {
			var parsed map[string]any
			if args != "" {
				if err := json.Unmarshal([]byte(args), &parsed); err != nil {
					return types.ToolResult{}, fmt.Errorf("parsing tool args: %w", err)
				}
			}
			if vr := st.ValidateInput(parsed); !vr.Valid {
				return types.ToolResult{Content: fmt.Sprintf("entrada invalida: %s", vr.Message), IsError: true}, nil
			}
			res, err := st.Execute(types.ToolContext{Context: ctx, WorkingDir: workdir.Dir(ctx, ".")}, parsed)
			if err == nil && !res.IsError {
				evolution.ObserveTool(ctx, name)
			}
			return res, err
		}
	}
	var result types.ToolResult
	var err error
	if rich, ok := reg.(interface {
		ExecuteResultContext(context.Context, string, string) (types.ToolResult, error)
	}); ok {
		result, err = rich.ExecuteResultContext(ctx, name, args)
	} else if rich, ok := reg.(interface {
		ExecuteResult(string, string) (types.ToolResult, error)
	}); ok {
		result, err = rich.ExecuteResult(name, args)
	} else {
		var output string
		output, err = reg.Execute(name, args)
		result = types.ToolResult{Content: output, IsError: err != nil}
	}
	if err == nil && !result.IsError {
		// The context-aware adapter routes through the orchestrator, where
		// execution is already observed. Legacy registries and direct adapter
		// execution have no orchestrator and are observed here.
		adapter, isAdapter := reg.(*ToolRegistryAdapter)
		if !isAdapter || adapter.orchestrateContext == nil {
			evolution.ObserveTool(ctx, name)
		}
	}
	return result, err
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
