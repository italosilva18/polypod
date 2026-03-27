package completion

import "fmt"

// Bash generates bash completion script.
func Bash() string {
	return `_polypod_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local prev="${COMP_WORDS[COMP_CWORD-1]}"

    # Main commands
    if [ "$COMP_CWORD" -eq 1 ]; then
        COMPREPLY=($(compgen -W "--setup --help --version -p -c --continue -r --resume --watch --browser doctor init mcp completion" -- "$cur"))
        return
    fi

    # Subcommands
    case "$prev" in
        -p|--prompt)
            COMPREPLY=()
            ;;
        --output-format)
            COMPREPLY=($(compgen -W "text json stream-json" -- "$cur"))
            ;;
        --mode)
            COMPREPLY=($(compgen -W "edit plan ask auto" -- "$cur"))
            ;;
        mcp)
            COMPREPLY=($(compgen -W "serve" -- "$cur"))
            ;;
        completion)
            COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur"))
            ;;
        *)
            # File completion as default
            COMPREPLY=($(compgen -f -- "$cur"))
            ;;
    esac
}
complete -F _polypod_completions polypod
`
}

// Zsh generates zsh completion script.
func Zsh() string {
	return `#compdef polypod

_polypod() {
    local -a commands
    commands=(
        '--setup:Setup interativo'
        '--help:Ajuda'
        '--version:Versao'
        '-p:Modo headless (prompt unico)'
        '-c:Continuar ultima sessao'
        '-r:Resumir sessao por ID'
        '--watch:Modo watch (monitorar arquivos)'
        '--browser:Abrir interface web'
        '--output-format:Formato de saida (text/json/stream-json)'
        '--mode:Modo de operacao (edit/plan/ask/auto)'
        'doctor:Diagnostico do ambiente'
        'init:Inicializar projeto'
        'mcp:Gerenciar MCP servers'
        'completion:Gerar shell completions'
    )

    _describe 'polypod' commands
}

_polypod "$@"
`
}

// Fish generates fish completion script.
func Fish() string {
	return `# polypod fish completions
complete -c polypod -l setup -d 'Setup interativo'
complete -c polypod -l help -d 'Ajuda'
complete -c polypod -l version -d 'Versao'
complete -c polypod -s p -d 'Modo headless'
complete -c polypod -s c -l continue -d 'Continuar sessao'
complete -c polypod -s r -l resume -d 'Resumir sessao'
complete -c polypod -l watch -d 'Modo watch'
complete -c polypod -l browser -d 'Interface web'
complete -c polypod -l output-format -xa 'text json stream-json' -d 'Formato de saida'
complete -c polypod -l mode -xa 'edit plan ask auto' -d 'Modo de operacao'
complete -c polypod -a doctor -d 'Diagnostico'
complete -c polypod -a init -d 'Inicializar projeto'
complete -c polypod -a mcp -d 'MCP servers'
complete -c polypod -a completion -d 'Shell completions'
`
}

// Generate returns the completion script for the given shell.
func Generate(shell string) (string, error) {
	switch shell {
	case "bash":
		return Bash(), nil
	case "zsh":
		return Zsh(), nil
	case "fish":
		return Fish(), nil
	default:
		return "", fmt.Errorf("shell desconhecido: %s (use: bash, zsh, fish)", shell)
	}
}
