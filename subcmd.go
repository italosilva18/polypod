package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/costa/polypod/internal/autoupdate"
	"github.com/costa/polypod/internal/completion"
	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/doctor"
	"github.com/costa/polypod/internal/githook"
	"github.com/costa/polypod/internal/initcmd"
	"github.com/costa/polypod/internal/mcpserver"
	"github.com/costa/polypod/internal/observability"
	"github.com/costa/polypod/internal/skill"
)

// handleSubcommand checks os.Args for a known subcommand and executes it.
// Returns true if a subcommand was handled (caller should exit).
func handleSubcommand() bool {
	if len(os.Args) < 2 {
		return false
	}

	switch os.Args[1] {
	case "doctor":
		cfg, _ := config.Load(defaultConfigPath)
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		checks := doctor.RunDiagnostics(cfg)
		fmt.Print(doctor.FormatReport(checks))
		return true

	case "init":
		stack := initcmd.Detect(".")
		files, err := initcmd.WriteInit(".", stack)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erro: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Projeto inicializado. %d arquivo(s) criado(s):\n", len(files))
		for _, f := range files {
			fmt.Printf("  - %s\n", f)
		}
		return true

	case "completion":
		shell := "bash"
		if len(os.Args) >= 3 {
			shell = os.Args[2]
		}
		script, err := completion.Generate(shell)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Print(script)
		return true

	case "hook":
		action := "status"
		if len(os.Args) >= 3 {
			action = os.Args[2]
		}
		switch action {
		case "install":
			installed, err := githook.Install()
			if err != nil {
				fmt.Fprintf(os.Stderr, "erro: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Hooks instalados: %s\n", strings.Join(installed, ", "))
		case "uninstall":
			removed, _ := githook.Uninstall()
			fmt.Printf("Hooks removidos: %s\n", strings.Join(removed, ", "))
		case "status":
			active, _ := githook.Status()
			if len(active) == 0 {
				fmt.Println("Nenhum hook polypod instalado.")
			} else {
				fmt.Printf("Hooks ativos: %s\n", strings.Join(active, ", "))
			}
		}
		return true

	case "mcp":
		if len(os.Args) >= 3 && os.Args[2] == "serve" {
			logger := observability.NewLogger("info", "text")
			skills := skill.NewRegistry()
			srv := mcpserver.New(skills, logger)
			if err := srv.Serve(); err != nil {
				fmt.Fprintf(os.Stderr, "mcp serve: %v\n", err)
				os.Exit(1)
			}
			return true
		}
		return false

	case "--version", "-v":
		fmt.Printf("polypod v%s\n", autoupdate.CurrentVersion())
		return true
	}

	return false
}
