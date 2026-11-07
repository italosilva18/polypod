package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/whatsappgateway"
)

// Check represents a single diagnostic check.
type Check struct {
	Name    string
	Status  string // "ok", "warn", "fail"
	Message string
}

// RunDiagnostics performs all health checks and returns results.
func RunDiagnostics(cfg *config.Config) []Check {
	return RunDiagnosticsContext(context.Background(), cfg, false)
}

func runBasicDiagnostics(cfg *config.Config) []Check {
	var checks []Check
	checks = append(checks, checkRemote(cfg.Remote))
	checks = append(checks, checkWhatsApp(cfg.WhatsApp, cfg.Data.Dir))

	// 1. Go version
	checks = append(checks, checkCommand("Go", "go", "version"))

	// 2. Git
	checks = append(checks, checkCommand("Git", "git", "--version"))

	// 3. AI provider connectivity
	if cfg.AI.Provider == "chatgpt" {
		checks = append(checks, Check{Name: "AI Provider", Status: "ok", Message: "ChatGPT selecionado; autenticacao verificada separadamente"})
	} else {
		checks = append(checks, checkAPIKey("AI Provider", cfg.AI.APIKey, cfg.AI.BaseURL))
	}

	// 4. Database
	if cfg.Database.Enabled {
		switch cfg.Database.Driver {
		case "sqlite":
			checks = append(checks, checkFile("SQLite DB", cfg.Database.Path))
		default:
			checks = append(checks, checkCommand("PostgreSQL", "psql", "--version"))
		}
	} else {
		checks = append(checks, Check{Name: "Banco de dados", Status: "ok", Message: "desativado (usando JSON)"})
	}

	// 5. Docker (for sandbox)
	checks = append(checks, checkCommand("Docker", "docker", "info"))

	// 6. Voice tools
	checks = append(checks, checkLocalTranscription(cfg.WhatsApp.AudioTranscription))
	checks = append(checks, checkVoiceOutput(cfg.WhatsApp.VoiceOutput))
	checks = append(checks, checkSemanticMemory(cfg.Embedding))
	if !cfg.WhatsApp.VoiceOutput.Enabled {
		checks = append(checks, checkOptionalTool("espeak-ng (TTS)", "espeak-ng"))
	}

	// 7. Data directories
	checks = append(checks, checkDir("Data dir", cfg.Data.Dir))
	checks = append(checks, checkDir("Agents dir", cfg.Data.AgentsDir))
	checks = append(checks, checkDreamMemory(filepath.Join(cfg.Data.Dir, "dream-state.json")))

	// 8. Templates
	checks = append(checks, checkDir("Templates", cfg.Templates.Dir))

	// 9. MCP servers
	for _, mcp := range cfg.MCP {
		if mcp.Transport == "stdio" {
			checks = append(checks, checkCommand("MCP: "+mcp.Name, mcp.Command, "--version"))
		}
	}

	// 11. Linters
	checks = append(checks, checkOptionalTool("golangci-lint", "golangci-lint"))
	checks = append(checks, checkOptionalTool("semgrep", "semgrep"))

	// 12. Config file integrity
	checks = append(checks, Check{Name: "Config", Status: "ok", Message: fmt.Sprintf("provider=%s model=%s", cfg.AI.Provider, cfg.AI.Model)})

	return checks
}

// FormatReport formats check results for display.
func FormatReport(checks []Check) string {
	var sb strings.Builder
	sb.WriteString("## Polypod Doctor\n\n")

	okCount, warnCount, failCount := 0, 0, 0
	for _, c := range checks {
		var icon string
		switch c.Status {
		case "ok":
			icon = "[OK]"
			okCount++
		case "warn":
			icon = "[!!]"
			warnCount++
		case "fail":
			icon = "[XX]"
			failCount++
		}
		fmt.Fprintf(&sb, "  %s %-25s %s\n", icon, c.Name, c.Message)
	}

	sb.WriteString(fmt.Sprintf("\nResultado: %d ok, %d avisos, %d falhas\n", okCount, warnCount, failCount))
	if failCount == 0 && warnCount == 0 {
		sb.WriteString("Polypod esta funcionando corretamente.\n")
	} else if failCount == 0 {
		sb.WriteString("Polypod esta ativo, com avisos que precisam de verificacao.\n")
	}
	return sb.String()
}

func checkCommand(name, cmd string, args ...string) Check {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.CombinedOutput()
	if err != nil {
		return Check{Name: name, Status: "fail", Message: fmt.Sprintf("nao encontrado (%v)", err)}
	}
	firstLine := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	if len(firstLine) > 60 {
		firstLine = firstLine[:60]
	}
	return Check{Name: name, Status: "ok", Message: firstLine}
}

func checkOptionalTool(name, cmd string) Check {
	if _, err := exec.LookPath(cmd); err != nil {
		return Check{Name: name, Status: "warn", Message: "nao instalado (opcional)"}
	}
	return Check{Name: name, Status: "ok", Message: "disponivel"}
}

// checkLocalTranscription only inspects configuration and files. It never
// starts Python or ffmpeg, opens audio, or prints configured paths.
func checkLocalTranscription(cfg config.AudioTranscriptionConfig) Check {
	c := Check{Name: "Transcrição local", Status: "warn"}
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if !cfg.Enabled {
		c.Message = "desativada em whatsapp.audio_transcription.enabled"
		return c
	}
	if backend == "" || backend == "http" {
		c.Message = "backend HTTP selecionado; transcrição local não configurada"
		return c
	}
	if backend != "local" {
		c.Status = "fail"
		c.Message = "backend de transcrição desconhecido"
		return c
	}

	python := cfg.PythonBinary
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	ffmpeg := cfg.FFmpegBinary
	if strings.TrimSpace(ffmpeg) == "" {
		ffmpeg = "ffmpeg"
	}
	var missing []string
	if !transcriptionExecutableExists(python) {
		missing = append(missing, "Python")
	}
	if !transcriptionFileExists(cfg.ScriptPath) {
		missing = append(missing, "script")
	}
	if !transcriptionFileExists(cfg.ModelPath) {
		missing = append(missing, "modelo")
	}
	if !transcriptionExecutableExists(ffmpeg) {
		missing = append(missing, "ffmpeg")
	}
	if len(missing) != 0 {
		c.Status = "fail"
		c.Message = "dependências ausentes ou inválidas: " + strings.Join(missing, ", ")
		return c
	}
	c.Status = "ok"
	c.Message = "backend local configurado; Python, script, modelo e ffmpeg presentes; transcrição não executada"
	return c
}

func checkVoiceOutput(cfg config.VoiceOutputConfig) Check {
	c := Check{Name: "Voz local", Status: "warn", Message: "desativada em whatsapp.voice_output.enabled"}
	if !cfg.Enabled {
		return c
	}
	python := cfg.PythonBinary
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	ffmpeg := cfg.FFmpegBinary
	if strings.TrimSpace(ffmpeg) == "" {
		ffmpeg = "ffmpeg"
	}
	var missing []string
	if !transcriptionExecutableExists(python) {
		missing = append(missing, "Python")
	}
	if !transcriptionExecutableExists(ffmpeg) {
		missing = append(missing, "ffmpeg")
	}
	if !transcriptionFileExists(cfg.ModelPath) || !transcriptionFileExists(cfg.ModelPath+".json") {
		missing = append(missing, "modelo Piper")
	}
	if len(missing) != 0 {
		c.Status = "fail"
		c.Message = "dependências ausentes ou inválidas: " + strings.Join(missing, ", ")
		return c
	}
	c.Status = "ok"
	c.Message = "Piper e ffmpeg presentes; síntese não executada"
	return c
}

func checkSemanticMemory(cfg config.EmbeddingConfig) Check {
	c := Check{Name: "Busca semântica", Status: "warn", Message: "desativada em embedding.enabled"}
	if !cfg.Enabled {
		return c
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "local" {
		configured, err := memory.NewLocalEmbedder(memory.LocalEmbeddingConfig{
			PythonBinary: cfg.PythonBinary, CacheDir: cfg.CacheDir,
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			return Check{Name: c.Name, Status: "fail", Message: "Python ou modelo local indisponível"}
		}
		_ = configured.Close()
		return Check{Name: c.Name, Status: "ok", Message: "Python e cache locais presentes; inferência não executada neste diagnóstico"}
	}
	if backend == "" || backend == "http" {
		if _, err := memory.NewHTTPEmbedder(memory.HTTPEmbeddingConfig{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model}); err != nil {
			return Check{Name: c.Name, Status: "fail", Message: "endpoint ou modelo HTTP inválido"}
		}
		return Check{Name: c.Name, Status: "ok", Message: "endpoint HTTP configurado; consulta não executada"}
	}
	return Check{Name: c.Name, Status: "fail", Message: "backend desconhecido"}
}

func checkDreamMemory(path string) Check {
	c := Check{Name: "Memória automática", Status: "warn"}
	data, err := os.ReadFile(path)
	if err != nil {
		c.Message = "ainda sem estado de consolidação"
		return c
	}
	var state struct {
		Stats struct {
			Runs           int `json:"runs"`
			SuccessfulRuns int `json:"successful_runs"`
			FailedRuns     int `json:"failed_runs"`
			MemoriesAdded  int `json:"memories_added"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		c.Status = "fail"
		c.Message = "estado de consolidação inválido"
		return c
	}
	if state.Stats.Runs == 0 {
		c.Message = "serviço ligado; sem métricas de execução registradas"
		return c
	}
	if state.Stats.FailedRuns > 0 {
		c.Message = fmt.Sprintf("%d execuções; %d falhas; %d memórias salvas", state.Stats.Runs,
			state.Stats.FailedRuns, state.Stats.MemoriesAdded)
		return c
	}
	c.Status = "ok"
	c.Message = fmt.Sprintf("%d execuções; %d concluídas; %d memórias salvas", state.Stats.Runs,
		state.Stats.SuccessfulRuns, state.Stats.MemoriesAdded)
	return c
}

func transcriptionExecutableExists(path string) bool {
	_, err := exec.LookPath(transcriptionExpandHome(strings.TrimSpace(path)))
	return err == nil
}

func transcriptionFileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(transcriptionExpandHome(path))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func transcriptionExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func checkAPIKey(name, key, baseURL string) Check {
	if key == "" || strings.HasPrefix(key, "${") || strings.HasPrefix(key, "sk-your") {
		return Check{Name: name, Status: "fail", Message: "API key nao configurada"}
	}
	return Check{Name: name, Status: "ok", Message: "configurada; valor omitido"}
}

func checkFile(name, path string) Check {
	if _, err := os.Stat(path); err != nil {
		return Check{Name: name, Status: "warn", Message: "arquivo nao existe (sera criado no primeiro uso)"}
	}
	return Check{Name: name, Status: "ok", Message: path}
}

func checkDir(name, path string) Check {
	if info, err := os.Stat(path); err != nil {
		return Check{Name: name, Status: "warn", Message: fmt.Sprintf("%s nao existe", path)}
	} else if !info.IsDir() {
		return Check{Name: name, Status: "fail", Message: fmt.Sprintf("%s nao e diretorio", path)}
	}
	return Check{Name: name, Status: "ok", Message: path}
}

func checkRemote(cfg config.RemoteConfig) Check {
	check := Check{Name: "Telegram remoto", Status: "ok", Message: "desativado"}
	if !cfg.Enabled {
		return check
	}
	if err := cfg.Validate(); err != nil {
		check.Status, check.Message = "fail", err.Error()
		return check
	}
	check.Message = "configurado com filtro de chat e remetente; conectividade nao verificada"
	return check
}

func checkWhatsApp(cfg config.WhatsAppConfig, dataDir string) Check {
	c := Check{Name: "WhatsApp nativo", Status: "ok", Message: "desativado"}
	if !cfg.Enabled {
		return c
	}
	if len(cfg.AllowedChats) == 0 || len(cfg.AllowedSenders) == 0 {
		c.Status, c.Message = "fail", "configure allowed_chats e allowed_senders antes de ativar"
		return c
	}
	if err := (&whatsappgateway.Native{SessionDir: cfg.SessionDir}).Preflight(); err != nil {
		c.Status, c.Message = "fail", "sessao isolada indisponivel; confira session_dir e o backup da sessao pareada"
		return c
	}
	status, err := whatsappgateway.ReadStatus(filepath.Join(dataDir, "whatsapp-status.json"))
	if err != nil {
		c.Status, c.Message = "warn", "sessao isolada preparada; nenhuma conexao recente foi registrada"
		return c
	}
	if status.CheckedAt.IsZero() || time.Since(status.CheckedAt) > time.Minute || time.Until(status.CheckedAt) > time.Minute {
		c.Status, c.Message = "warn", "estado do canal desatualizado; confira o processo e os logs"
		return c
	}
	if status.State != "connected" || !status.Authenticated || !status.SocketConnected {
		if status.SocketConnected && !status.Authenticated {
			c.Status, c.Message = "warn", "websocket aberto, mas sessao WhatsApp ainda nao autenticada"
		} else {
			c.Status, c.Message = "warn", "canal sem conexao ativa; reconexao automatica em andamento"
		}
		return c
	}
	c.Message = "conexao ativa; confirme recebimento e resposta em teste real"
	return c
}
