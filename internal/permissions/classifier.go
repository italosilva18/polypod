package permissions

import (
	"regexp"
	"strings"
)

// CommandClassification describes the result of classifying a bash command.
type CommandClassification struct {
	Category    CommandCategory
	SubCategory string
	Confidence  string // "high", "medium", "low"
	Reason      string
	ReadOnly    bool
	Destructive bool
}

// CommandCategory is the broad classification of a shell command.
type CommandCategory int

const (
	CategoryUnknown CommandCategory = iota
	CategoryReadOnly
	CategoryFileWrite
	CategoryFileDelete
	CategoryFileMove
	CategoryNetworkRead
	CategoryNetworkWrite
	CategoryGit
	CategoryGitDestructive
	CategoryPackageManager
	CategoryPackageManagerInstall
	CategoryPackageManagerPublish
	CategoryProcessManagement
	CategorySystemConfig
	CategoryShellExec
	CategoryInterpreter
	CategoryRedirection
	CategoryPipeChain
)

// String returns a human-readable name for a CommandCategory.
func (c CommandCategory) String() string {
	switch c {
	case CategoryReadOnly:
		return "read-only"
	case CategoryFileWrite:
		return "file-write"
	case CategoryFileDelete:
		return "file-delete"
	case CategoryFileMove:
		return "file-move"
	case CategoryNetworkRead:
		return "network-read"
	case CategoryNetworkWrite:
		return "network-write"
	case CategoryGit:
		return "git"
	case CategoryGitDestructive:
		return "git-destructive"
	case CategoryPackageManager:
		return "package-manager"
	case CategoryPackageManagerInstall:
		return "package-manager-install"
	case CategoryPackageManagerPublish:
		return "package-manager-publish"
	case CategoryProcessManagement:
		return "process-management"
	case CategorySystemConfig:
		return "system-config"
	case CategoryShellExec:
		return "shell-exec"
	case CategoryInterpreter:
		return "interpreter"
	case CategoryRedirection:
		return "redirection"
	case CategoryPipeChain:
		return "pipe-chain"
	default:
		return "unknown"
	}
}

// ReadOnlyCommands is the set of commands that are considered read-only.
var ReadOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"less": true, "more": true, "file": true, "stat": true,
	"wc": true, "du": true, "df": true, "find": true,
	"grep": true, "rg": true, "ag": true, "ack": true,
	"which": true, "whereis": true, "type": true, "whatis": true,
	"whoami": true, "id": true, "hostname": true, "uname": true,
	"date": true, "cal": true, "uptime": true, "free": true,
	"top": true, "htop": true, "ps": true, "pgrep": true,
	"echo": true, "printf": true, "test": true, "true": true,
	"false": true, "pwd": true, "env": false, "printenv": true,
	"diff": true, "cmp": true, "md5sum": true, "sha256sum": true,
	"sha1sum": true, "cksum": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "awk": true, "sed": false,
	"tree": true, "realpath": true, "readlink": true,
	"basename": true, "dirname": true, "nproc": true,
	"lsof": true, "strace": true, "ltrace": true,
	"dig": true, "nslookup": true, "host": true, "ping": true,
	"traceroute": true, "ip": true, "ifconfig": true, "netstat": true,
	"ss": true, "lscpu": true, "lsblk": true, "lspci": true,
	"lsusb": true, "dmesg": true, "journalctl": true,
	"git": false, // special handling
	"jq":  true, "yq": true, "xargs": false,
}

// WriteCommands are commands that write/modify files.
var WriteCommands = map[string]bool{
	"touch": true, "mkdir": true, "cp": true, "mv": true,
	"rm": true, "rmdir": true, "ln": true, "install": true,
	"tee": true, "dd": true, "truncate": true, "shred": true,
	"chown": true, "chmod": true, "chgrp": true,
}

// GitSubcommandClassification maps git subcommands to classifications.
var GitSubcommandClassification = map[string]CommandCategory{
	// Read-only
	"status": CategoryReadOnly, "log": CategoryReadOnly,
	"diff": CategoryReadOnly, "show": CategoryReadOnly,
	"branch": CategoryReadOnly, "tag": CategoryReadOnly,
	"describe": CategoryReadOnly, "rev-parse": CategoryReadOnly,
	"remote": CategoryReadOnly, "ls-files": CategoryReadOnly,
	"ls-remote": CategoryReadOnly, "ls-tree": CategoryReadOnly,
	"blame": CategoryReadOnly, "shortlog": CategoryReadOnly,
	"stash": CategoryGit, // stash can lose data
	// Normal write
	"add": CategoryGit, "commit": CategoryGit,
	"checkout": CategoryGit, "switch": CategoryGit,
	"merge": CategoryGit, "rebase": CategoryGit,
	"cherry-pick": CategoryGit, "pull": CategoryGit,
	"fetch": CategoryGit, "clone": CategoryGit,
	"init": CategoryGit, "worktree": CategoryGit,
	// Destructive
	"push":    CategoryGitDestructive,
	"reset":   CategoryGitDestructive,
	"revert":  CategoryGit,
	"clean":   CategoryGitDestructive,
	"gc":      CategoryGit,
	"prune":   CategoryGitDestructive,
	"reflog":  CategoryReadOnly,
	"bisect":  CategoryReadOnly,
	"archive": CategoryReadOnly,
}

// NetworkCommands maps network commands to their classification.
var NetworkCommands = map[string]CommandCategory{
	"curl":  CategoryNetworkRead,
	"wget":  CategoryNetworkRead,
	"ssh":   CategoryNetworkWrite,
	"scp":   CategoryNetworkWrite,
	"rsync": CategoryNetworkWrite,
	"sftp":  CategoryNetworkWrite,
	"ftp":   CategoryNetworkWrite,
	"nc":    CategoryNetworkWrite,
	"ncat":  CategoryNetworkWrite,
	"socat": CategoryNetworkWrite,
}

// PackageManagerCommands maps package manager commands.
var PackageManagerCommands = map[string]bool{
	"npm": true, "yarn": true, "pnpm": true, "bun": true,
	"pip": true, "pip3": true, "pipx": true,
	"gem": true, "cargo": true, "go": true,
	"apt": true, "apt-get": true, "dpkg": true,
	"yum": true, "dnf": true, "pacman": true,
	"brew": true, "snap": true, "flatpak": true,
	"composer": true, "maven": true, "gradle": true,
	"poetry": true, "pdm": true, "uv": true,
}

// PackageManagerPublishSubcommands that indicate publishing.
var PackageManagerPublishSubcommands = map[string]bool{
	"publish": true, "push": true, "upload": true,
	"deploy": true, "release": true,
}

// PackageManagerInstallSubcommands that indicate installing.
var PackageManagerInstallSubcommands = map[string]bool{
	"install": true, "add": true, "update": true, "upgrade": true,
	"remove": true, "uninstall": true,
}

// InterpreterCommands that execute arbitrary code.
var InterpreterCommands = map[string]bool{
	"python": true, "python3": true, "python2": true,
	"node": true, "deno": true, "tsx": true, "ts-node": true,
	"ruby": true, "perl": true, "php": true, "lua": true,
	"bash": true, "sh": true, "zsh": true, "fish": true,
	"eval": true, "exec": true,
}

// ShellExecCommands that run other commands.
var ShellExecCommands = map[string]bool{
	"sudo": true, "su": true, "env": true,
	"nohup": true, "timeout": true, "watch": true,
	"xargs": true, "nice": true, "ionice": true,
}

// TokenizedCommand represents a parsed shell command.
type TokenizedCommand struct {
	RawCommand  string
	Executable  string
	Args        []string
	Flags       []string
	Subcommand  string
	HasPipe     bool
	HasRedirect bool
	RedirectOut bool // > or >>
	RedirectIn  bool // <
	Background  bool // &
	Chained     bool // && or ;
}

// TokenizeCommand parses a raw shell command string into components.
func TokenizeCommand(raw string) TokenizedCommand {
	tc := TokenizedCommand{RawCommand: raw}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tc
	}

	// Detect pipes, redirections, chaining, background
	tc.HasPipe = containsUnquoted(raw, "|") && !containsUnquoted(raw, "||")
	tc.HasRedirect = containsUnquoted(raw, ">") || containsUnquoted(raw, "<")
	tc.RedirectOut = containsUnquoted(raw, ">")
	tc.RedirectIn = containsUnquoted(raw, "<")
	tc.Background = strings.HasSuffix(strings.TrimSpace(raw), "&") &&
		!strings.HasSuffix(strings.TrimSpace(raw), "&&")
	tc.Chained = containsUnquoted(raw, "&&") || containsUnquoted(raw, ";")

	// Split into tokens (simple word splitting, respects quotes)
	tokens := shellSplit(raw)
	if len(tokens) == 0 {
		return tc
	}

	tc.Executable = tokens[0]

	// Strip path prefix to get base command name
	if idx := strings.LastIndex(tc.Executable, "/"); idx >= 0 {
		tc.Executable = tc.Executable[idx+1:]
	}

	if len(tokens) > 1 {
		tc.Args = tokens[1:]
	}

	// Extract flags and detect subcommand
	for _, arg := range tc.Args {
		if strings.HasPrefix(arg, "-") {
			tc.Flags = append(tc.Flags, arg)
		} else if tc.Subcommand == "" && !strings.HasPrefix(arg, "/") &&
			!strings.HasPrefix(arg, ".") && !strings.Contains(arg, "=") {
			tc.Subcommand = arg
		}
	}

	return tc
}

// ClassifyBashCommand evaluates a shell command and returns its classification.
func ClassifyBashCommand(command string) CommandClassification {
	tc := TokenizeCommand(command)

	if tc.Executable == "" {
		return CommandClassification{
			Category:   CategoryUnknown,
			Confidence: "low",
			Reason:     "empty command",
		}
	}

	// Check for output redirection (always treated as write)
	if tc.RedirectOut {
		cls := classifyBaseCommand(tc)
		cls.Category = CategoryRedirection
		cls.ReadOnly = false
		cls.Destructive = false
		cls.Reason = "command writes to file via output redirection"
		return cls
	}

	// Check for pipe chains
	if tc.HasPipe {
		cls := classifyBaseCommand(tc)
		cls.SubCategory = "pipe-chain"
		if cls.ReadOnly {
			cls.Reason = "read-only pipe chain"
		} else {
			cls.Category = CategoryPipeChain
			cls.Reason = "pipe chain with potential write operations"
		}
		return cls
	}

	return classifyBaseCommand(tc)
}

func classifyBaseCommand(tc TokenizedCommand) CommandClassification {
	exe := strings.ToLower(tc.Executable)

	// Interpreters
	if InterpreterCommands[exe] {
		return CommandClassification{
			Category:    CategoryInterpreter,
			SubCategory: exe,
			Confidence:  "high",
			Reason:      exe + " can execute arbitrary code",
			ReadOnly:    false,
			Destructive: false,
		}
	}

	// Shell exec commands
	if ShellExecCommands[exe] {
		return CommandClassification{
			Category:    CategoryShellExec,
			SubCategory: exe,
			Confidence:  "high",
			Reason:      exe + " executes other commands with elevated privileges or modified behavior",
			ReadOnly:    false,
			Destructive: exe == "sudo",
		}
	}

	// Git
	if exe == "git" {
		return classifyGitCommand(tc)
	}

	// Network commands
	if netCat, ok := NetworkCommands[exe]; ok {
		cls := CommandClassification{
			Category:    netCat,
			SubCategory: exe,
			Confidence:  "high",
			ReadOnly:    netCat == CategoryNetworkRead,
		}
		// curl/wget with POST data is a write
		if (exe == "curl" || exe == "wget") && hasPostFlags(tc.Flags, tc.Args) {
			cls.Category = CategoryNetworkWrite
			cls.ReadOnly = false
			cls.Reason = exe + " sending data (POST/PUT)"
		} else if netCat == CategoryNetworkRead {
			cls.Reason = exe + " reading data"
		} else {
			cls.Reason = exe + " network connection"
		}
		return cls
	}

	// Package managers
	if PackageManagerCommands[exe] {
		return classifyPackageManagerCommand(tc)
	}

	// File write commands
	if WriteCommands[exe] {
		destructive := false
		cat := CategoryFileWrite
		switch exe {
		case "rm", "rmdir", "shred":
			cat = CategoryFileDelete
			destructive = hasForceFlags(tc.Flags)
		case "mv":
			cat = CategoryFileMove
		case "dd":
			destructive = true
		}
		return CommandClassification{
			Category:    cat,
			SubCategory: exe,
			Confidence:  "high",
			Reason:      exe + " modifies filesystem",
			ReadOnly:    false,
			Destructive: destructive,
		}
	}

	// Read-only commands
	if ro, ok := ReadOnlyCommands[exe]; ok && ro {
		return CommandClassification{
			Category:    CategoryReadOnly,
			SubCategory: exe,
			Confidence:  "high",
			Reason:      exe + " is read-only",
			ReadOnly:    true,
			Destructive: false,
		}
	}

	// sed with -i is write
	if exe == "sed" && hasInPlaceFlags(tc.Flags) {
		return CommandClassification{
			Category:    CategoryFileWrite,
			SubCategory: "sed",
			Confidence:  "high",
			Reason:      "sed with in-place editing",
			ReadOnly:    false,
			Destructive: false,
		}
	}
	if exe == "sed" {
		return CommandClassification{
			Category:   CategoryReadOnly,
			Confidence: "medium",
			Reason:     "sed without -i is read-only (output to stdout)",
			ReadOnly:   true,
		}
	}

	return CommandClassification{
		Category:   CategoryUnknown,
		Confidence: "low",
		Reason:     "unrecognized command: " + exe,
	}
}

func classifyGitCommand(tc TokenizedCommand) CommandClassification {
	sub := tc.Subcommand

	if sub == "" {
		return CommandClassification{
			Category:   CategoryGit,
			Confidence: "medium",
			Reason:     "git without subcommand",
		}
	}

	cat, found := GitSubcommandClassification[sub]
	if !found {
		return CommandClassification{
			Category:    CategoryGit,
			SubCategory: sub,
			Confidence:  "low",
			Reason:      "unrecognized git subcommand: " + sub,
		}
	}

	cls := CommandClassification{
		Category:    cat,
		SubCategory: sub,
		Confidence:  "high",
		ReadOnly:    cat == CategoryReadOnly,
		Destructive: cat == CategoryGitDestructive,
	}

	// Special cases for destructive git operations
	switch sub {
	case "push":
		if hasFlag(tc.Flags, "--force") || hasFlag(tc.Flags, "-f") || hasFlag(tc.Flags, "--force-with-lease") {
			cls.Destructive = true
			cls.Reason = "git push --force can rewrite remote history"
		} else {
			cls.Destructive = false
			cls.Reason = "git push to remote"
		}
	case "reset":
		if hasFlag(tc.Flags, "--hard") {
			cls.Destructive = true
			cls.Reason = "git reset --hard discards uncommitted changes"
		} else {
			cls.Destructive = false
			cls.Reason = "git reset (soft/mixed)"
		}
	case "clean":
		cls.Destructive = true
		cls.Reason = "git clean removes untracked files"
	case "rebase":
		cls.Reason = "git rebase rewrites commit history"
	case "merge":
		cls.Reason = "git merge integrates changes"
	default:
		if cat == CategoryReadOnly {
			cls.Reason = "git " + sub + " is read-only"
		} else {
			cls.Reason = "git " + sub + " modifies repository"
		}
	}

	return cls
}

func classifyPackageManagerCommand(tc TokenizedCommand) CommandClassification {
	sub := tc.Subcommand

	if PackageManagerPublishSubcommands[sub] {
		return CommandClassification{
			Category:    CategoryPackageManagerPublish,
			SubCategory: tc.Executable + " " + sub,
			Confidence:  "high",
			Reason:      tc.Executable + " " + sub + " publishes a package",
			Destructive: true,
		}
	}
	if PackageManagerInstallSubcommands[sub] {
		return CommandClassification{
			Category:    CategoryPackageManagerInstall,
			SubCategory: tc.Executable + " " + sub,
			Confidence:  "high",
			Reason:      tc.Executable + " " + sub + " modifies dependencies",
		}
	}
	// run, exec, test, etc.
	if sub == "run" || sub == "exec" || sub == "x" {
		return CommandClassification{
			Category:    CategoryInterpreter,
			SubCategory: tc.Executable + " " + sub,
			Confidence:  "high",
			Reason:      tc.Executable + " " + sub + " executes arbitrary code",
		}
	}
	if sub == "test" || sub == "lint" || sub == "check" || sub == "info" ||
		sub == "list" || sub == "ls" || sub == "outdated" || sub == "audit" ||
		sub == "doctor" || sub == "why" || sub == "show" || sub == "view" {
		return CommandClassification{
			Category:    CategoryReadOnly,
			SubCategory: tc.Executable + " " + sub,
			Confidence:  "high",
			Reason:      tc.Executable + " " + sub + " is read-only",
			ReadOnly:    true,
		}
	}
	return CommandClassification{
		Category:    CategoryPackageManager,
		SubCategory: tc.Executable,
		Confidence:  "medium",
		Reason:      tc.Executable + " command",
	}
}

// --- Helpers ---

func hasPostFlags(flags, args []string) bool {
	for _, f := range flags {
		switch f {
		case "-X", "--request":
			// Next arg should be POST/PUT/PATCH/DELETE
			continue
		case "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode",
			"-F", "--form", "--upload-file", "-T":
			return true
		}
	}
	for i, a := range args {
		if (a == "-X" || a == "--request") && i+1 < len(args) {
			method := strings.ToUpper(args[i+1])
			if method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE" {
				return true
			}
		}
	}
	return false
}

func hasForceFlags(flags []string) bool {
	for _, f := range flags {
		if f == "-f" || f == "--force" || f == "-rf" || f == "-fr" {
			return true
		}
	}
	return false
}

func hasInPlaceFlags(flags []string) bool {
	for _, f := range flags {
		if f == "-i" || strings.HasPrefix(f, "-i") || f == "--in-place" {
			return true
		}
	}
	return false
}

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

// containsUnquoted checks if a string contains a substring outside of quotes.
func containsUnquoted(s, sub string) bool {
	inSingle := false
	inDouble := false
	escaped := false

	subLen := len(sub)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && i+subLen <= len(s) && s[i:i+subLen] == sub {
			return true
		}
	}
	return false
}

// shellSplit does basic shell-like token splitting, respecting quotes.
func shellSplit(s string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		// Outside quotes, whitespace and special chars split tokens
		if !inSingle && !inDouble {
			if ch == ' ' || ch == '\t' || ch == '\n' {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				continue
			}
			// Stop at redirections, pipes, semicolons etc. for the first command
			if ch == '|' || ch == '>' || ch == '<' || ch == ';' || ch == '&' {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				break
			}
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// ExtractOutputRedirections finds > and >> redirections in a command.
func ExtractOutputRedirections(command string) (string, []string) {
	re := regexp.MustCompile(`\s*>{1,2}\s*\S+`)
	redirections := re.FindAllString(command, -1)
	cleaned := re.ReplaceAllString(command, "")
	var trimmed []string
	for _, r := range redirections {
		trimmed = append(trimmed, strings.TrimSpace(r))
	}
	return strings.TrimSpace(cleaned), trimmed
}
