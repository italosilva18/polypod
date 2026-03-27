package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers voice I/O skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "voice_record",
		Description: "Gravar audio do microfone",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"duration": {Type: jsonschema.String, Description: "Duracao em segundos (default: 5)"},
				"output":   {Type: jsonschema.String, Description: "Caminho do arquivo (default: /tmp/polypod_audio.wav)"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			duration := args["duration"]
			if duration == "" {
				duration = "5"
			}
			output := args["output"]
			if output == "" {
				output = "/tmp/polypod_audio.wav"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if _, err := exec.LookPath("arecord"); err == nil {
				cmd := exec.CommandContext(ctx, "arecord", "-d", duration, "-f", "cd", "-t", "wav", output)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return fmt.Sprintf("Audio gravado: %s (%ss)", output, duration), nil
			}
			if _, err := exec.LookPath("sox"); err == nil {
				cmd := exec.CommandContext(ctx, "sox", "-d", "-r", "16000", "-c", "1", output, "trim", "0", duration)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				return fmt.Sprintf("Audio gravado: %s (%ss)", output, duration), nil
			}
			return "", fmt.Errorf("nenhuma ferramenta de gravacao encontrada (instale arecord ou sox)")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "voice_transcribe",
		Description: "Transcrever audio para texto usando Whisper",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":     {Type: jsonschema.String, Description: "Caminho do arquivo de audio"},
				"language": {Type: jsonschema.String, Description: "Idioma (default: pt)"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := args["path"]
			lang := args["language"]
			if lang == "" {
				lang = "pt"
			}
			if _, err := os.Stat(path); err != nil {
				return "", fmt.Errorf("arquivo nao encontrado: %s", path)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			// Try whisper CLI
			if _, err := exec.LookPath("whisper"); err == nil {
				cmd := exec.CommandContext(ctx, "whisper", path, "--language", lang, "--model", "base", "--output_format", "txt")
				out, err := cmd.CombinedOutput()
				if err == nil {
					return strings.TrimSpace(string(out)), nil
				}
			}
			// Try faster-whisper
			if _, err := exec.LookPath("faster-whisper"); err == nil {
				cmd := exec.CommandContext(ctx, "faster-whisper", path, "--language", lang)
				out, err := cmd.CombinedOutput()
				if err == nil {
					return strings.TrimSpace(string(out)), nil
				}
			}
			return "", fmt.Errorf("nenhuma ferramenta de transcricao encontrada (instale whisper ou faster-whisper)")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "voice_speak",
		Description: "Converter texto em fala (TTS)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"text":   {Type: jsonschema.String, Description: "Texto para falar"},
				"output": {Type: jsonschema.String, Description: "Arquivo de saida (vazio = reproduzir direto)"},
			},
			Required: []string{"text"},
		},
		Execute: func(args map[string]string) (string, error) {
			text := args["text"]
			output := args["output"]
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if _, err := exec.LookPath("espeak-ng"); err == nil {
				cmdArgs := []string{"-v", "pt-br"}
				if output != "" {
					cmdArgs = append(cmdArgs, "-w", output)
				}
				cmdArgs = append(cmdArgs, text)
				cmd := exec.CommandContext(ctx, "espeak-ng", cmdArgs...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return string(out), nil
				}
				if output != "" {
					return fmt.Sprintf("Audio gerado: %s", output), nil
				}
				return "Texto falado com sucesso.", nil
			}
			if _, err := exec.LookPath("espeak"); err == nil {
				cmd := exec.CommandContext(ctx, "espeak", "-v", "pt-br", text)
				cmd.CombinedOutput()
				return "Texto falado.", nil
			}
			return "", fmt.Errorf("nenhuma ferramenta TTS encontrada (instale espeak-ng)")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "voice_available",
		Description: "Verificar ferramentas de voz disponiveis no sistema",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			tools := []string{"whisper", "faster-whisper", "arecord", "sox", "espeak-ng", "espeak", "piper", "ffmpeg"}
			var sb strings.Builder
			sb.WriteString("## Ferramentas de voz\n\n")
			for _, t := range tools {
				status := "nao encontrado"
				if _, err := exec.LookPath(t); err == nil {
					status = "disponivel"
				}
				fmt.Fprintf(&sb, "- **%s**: %s\n", t, status)
			}
			return sb.String(), nil
		},
	})
}
