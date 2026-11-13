package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/skill"
)

// BindPluginRuntime makes the slash commands use the same transactional
// plugin manager as the agent tools and the live skill registry.
func BindPluginRuntime(reg *command.Registry, manager *skill.PluginManager) {
	if reg == nil || manager == nil {
		return
	}
	run := func(args []string, _ command.CommandContext) (*command.CommandResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if len(args) == 0 {
			return b13Err("uso: /plugin list|info|enable|disable|install|update|remove|reload"), nil
		}
		sub := strings.ToLower(args[0])
		rest := args[1:]
		switch sub {
		case "list":
			if len(rest) != 0 {
				return b13Err("uso: /plugin list"), nil
			}
			plugins, err := manager.List(ctx)
			if err != nil {
				return b13Err(err.Error()), nil
			}
			if len(plugins) == 0 {
				return b13OK("Nenhum plugin instalado."), nil
			}
			var out strings.Builder
			for _, plugin := range plugins {
				state := "desativado"
				if plugin.Enabled {
					state = "ativo"
				}
				fmt.Fprintf(&out, "%s  %s  %s  %s\n", plugin.ID, plugin.Name, plugin.Version, state)
			}
			return b13OK(strings.TrimSpace(out.String())), nil
		case "info":
			if len(rest) != 1 {
				return b13Err("uso: /plugin info <id>"), nil
			}
			plugins, err := manager.List(ctx)
			if err != nil {
				return b13Err(err.Error()), nil
			}
			for _, plugin := range plugins {
				if plugin.ID == rest[0] {
					return b13OK(fmt.Sprintf("ID: %s\nNome: %s\nVersão: %s\nAutor: %s\nPasta: %s\nAtivo: %t\nSkills: %s\nCarregadas: %s", plugin.ID, plugin.Name, plugin.Version, plugin.Author, plugin.Path, plugin.Enabled, strings.Join(plugin.SkillFiles, ", "), strings.Join(plugin.LoadedSkills, ", "))), nil
				}
			}
			return b13Err("plugin não encontrado: " + rest[0]), nil
		case "enable", "disable":
			if len(rest) != 1 {
				return b13Err("uso: /plugin " + sub + " <id>"), nil
			}
			plugin, err := manager.SetEnabled(ctx, rest[0], sub == "enable")
			if err != nil {
				return b13Err(err.Error()), nil
			}
			return b13OK(fmt.Sprintf("Plugin %s atualizado; ativo: %t", plugin.ID, plugin.Enabled)), nil
		case "install":
			if len(rest) != 1 {
				return b13Err("uso: /plugin install <pasta-ou-URL-Git>"), nil
			}
			plugin, err := manager.Install(ctx, rest[0])
			if err != nil {
				return b13Err(err.Error()), nil
			}
			return b13OK(fmt.Sprintf("Plugin %s instalado (versão %s).", plugin.ID, plugin.Version)), nil
		case "update":
			if len(rest) != 2 {
				return b13Err("uso: /plugin update <id> <pasta-ou-URL-Git>"), nil
			}
			plugin, err := manager.Update(ctx, rest[0], rest[1])
			if err != nil {
				return b13Err(err.Error()), nil
			}
			return b13OK(fmt.Sprintf("Plugin %s atualizado para %s.", plugin.ID, plugin.Version)), nil
		case "remove":
			if len(rest) != 1 {
				return b13Err("uso: /plugin remove <id>"), nil
			}
			plugin, err := manager.Uninstall(ctx, rest[0])
			if err != nil {
				return b13Err(err.Error()), nil
			}
			return b13OK("Plugin removido: " + plugin.ID), nil
		case "reload":
			if len(rest) > 1 {
				return b13Err("uso: /plugin reload [id]"), nil
			}
			id := ""
			if len(rest) == 1 {
				id = rest[0]
			}
			count, err := manager.Reload(ctx, id)
			if err != nil {
				return b13Err(err.Error()), nil
			}
			return b13OK(fmt.Sprintf("Plugins recarregados; %d skills ativas.", count)), nil
		default:
			return b13Err("subcomando desconhecido: " + sub), nil
		}
	}
	reg.Register(&command.Command{Name: "plugin", Description: "Gerenciar plugins locais", Type: command.CommandLocal, Source: command.SourceBuiltin, Execute: run})
	reg.Register(&command.Command{Name: "reload-plugins", Description: "Recarregar plugins locais", Type: command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return run(append([]string{"reload"}, args...), ctx)
		}})
}
