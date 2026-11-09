package mcp

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// describeMCPBinary keeps binary tool output available to later file tools
// without inserting base64 data into the model context or application logs.
func describeMCPBinary(kind, mimeType string, data []byte) string {
	path, err := saveMCPBinary(mimeType, data)
	if err != nil {
		return fmt.Sprintf("Conteúdo MCP (%s, %s, %d bytes) não pôde ser salvo: %v", kind, mimeType, len(data), err)
	}
	return fmt.Sprintf("Conteúdo MCP (%s, %s, %d bytes) salvo em arquivo local %q", kind, mimeType, len(data), path)
}

func saveMCPBinary(mimeType string, data []byte) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "polypod", "mcp-content")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}

	suffix := ".bin"
	if mediaType, _, err := mime.ParseMediaType(mimeType); err == nil {
		if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 {
			suffix = extensions[0]
		}
	}
	file, err := os.CreateTemp(dir, "result-*"+suffix)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func describeMCPResourceLink(link *mcpsdk.ResourceLink) string {
	if link == nil {
		return ""
	}
	name := strings.TrimSpace(link.Title)
	if name == "" {
		name = strings.TrimSpace(link.Name)
	}
	if name == "" {
		name = "recurso"
	}
	description := fmt.Sprintf("Recurso MCP %q: URI %q", name, link.URI)
	if link.MIMEType != "" {
		description += fmt.Sprintf(", tipo %q", link.MIMEType)
	}
	if link.Size != nil {
		description += fmt.Sprintf(", %d bytes", *link.Size)
	}
	if link.Description != "" {
		description += fmt.Sprintf(". %s", link.Description)
	}
	return description
}
