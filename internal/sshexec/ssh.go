package sshexec

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Host represents an SSH host from config.
type Host struct {
	Name     string
	Hostname string
	User     string
	Port     string
}

// DiscoverHosts reads ~/.ssh/config and returns known hosts.
func DiscoverHosts() []Host {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	configPath := filepath.Join(home, ".ssh", "config")
	f, err := os.Open(configPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var hosts []Host
	var current *Host
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "host":
			if current != nil {
				hosts = append(hosts, *current)
			}
			if value != "*" {
				current = &Host{Name: value}
			} else {
				current = nil
			}
		case "hostname":
			if current != nil {
				current.Hostname = value
			}
		case "user":
			if current != nil {
				current.User = value
			}
		case "port":
			if current != nil {
				current.Port = value
			}
		}
	}
	if current != nil {
		hosts = append(hosts, *current)
	}

	return hosts
}

// RegisterSkills registers SSH remote execution skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "ssh_exec",
		Description: "Executar comando em servidor remoto via SSH",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"host":    {Type: jsonschema.String, Description: "Hostname ou alias SSH (de ~/.ssh/config)"},
				"command": {Type: jsonschema.String, Description: "Comando a executar remotamente"},
				"user":    {Type: jsonschema.String, Description: "Usuario SSH (opcional, usa config)"},
				"port":    {Type: jsonschema.String, Description: "Porta SSH (opcional, default: 22)"},
			},
			Required: []string{"host", "command"},
		},
		Execute: func(args map[string]string) (string, error) {
			host := args["host"]
			command := args["command"]

			sshArgs := []string{}
			if port := args["port"]; port != "" {
				sshArgs = append(sshArgs, "-p", port)
			}

			target := host
			if user := args["user"]; user != "" {
				target = user + "@" + host
			}
			sshArgs = append(sshArgs, target, command)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
			out, err := cmd.CombinedOutput()
			result := strings.TrimSpace(string(out))

			if ctx.Err() == context.DeadlineExceeded {
				return result + "\n[timeout: 60s]", nil
			}
			if err != nil {
				return result + "\n[exit: " + err.Error() + "]", nil
			}
			return fmt.Sprintf("[remote:%s] %s", host, result), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "ssh_hosts",
		Description: "Listar hosts SSH disponiveis (de ~/.ssh/config)",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			hosts := DiscoverHosts()
			if len(hosts) == 0 {
				return "Nenhum host encontrado em ~/.ssh/config", nil
			}
			var sb strings.Builder
			sb.WriteString("## Hosts SSH\n\n")
			for _, h := range hosts {
				hostname := h.Hostname
				if hostname == "" {
					hostname = h.Name
				}
				user := h.User
				if user == "" {
					user = "(default)"
				}
				port := h.Port
				if port == "" {
					port = "22"
				}
				fmt.Fprintf(&sb, "- **%s** → %s@%s:%s\n", h.Name, user, hostname, port)
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "ssh_copy",
		Description: "Copiar arquivo via SCP (local → remoto ou remoto → local)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"source": {Type: jsonschema.String, Description: "Origem (local ou host:path)"},
				"dest":   {Type: jsonschema.String, Description: "Destino (local ou host:path)"},
			},
			Required: []string{"source", "dest"},
		},
		Execute: func(args map[string]string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, "scp", "-r", args["source"], args["dest"])
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out), nil
			}
			return fmt.Sprintf("Copiado: %s → %s", args["source"], args["dest"]), nil
		},
	})
}
