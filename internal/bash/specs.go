package bash

import "strings"

// CommandSpec describes the behavior and flags of a specific command
// for security analysis purposes.
type CommandSpec struct {
	// Name is the command name (e.g., "rm", "curl").
	Name string

	// Description of what the command does.
	Description string

	// DangerousFlags are flags that make the command destructive or dangerous.
	DangerousFlags []string

	// SafeFlags are flags that are always safe (read-only, help, version).
	SafeFlags []string

	// RequiresArgs indicates the command must have at least one argument.
	RequiresArgs bool

	// RequiresPath indicates the command operates on file paths.
	RequiresPath bool

	// IsWrapper indicates the command wraps another command (e.g., sudo, timeout).
	IsWrapper bool

	// IsReadOnly indicates the command never modifies state.
	IsReadOnly bool

	// SubcommandSpecs maps subcommand names to their specs (e.g., "git" -> "push").
	SubcommandSpecs map[string]*CommandSpec
}

// commandSpecs is the registry of known command specifications.
var commandSpecs = map[string]*CommandSpec{
	// ─── Shell builtins ───
	"alias": {
		Name:        "alias",
		Description: "Create or list command aliases",
		IsReadOnly:  true,
	},
	"sleep": {
		Name:         "sleep",
		Description:  "Delay for a specified amount of time",
		RequiresArgs: true,
		IsReadOnly:   true,
	},

	// ─── Wrappers ───
	"timeout": {
		Name:         "timeout",
		Description:  "Run a command with a time limit",
		RequiresArgs: true,
		IsWrapper:    true,
		SafeFlags:    []string{"--foreground", "--preserve-status", "--verbose", "-v"},
	},
	"nohup": {
		Name:        "nohup",
		Description: "Run a command immune to hangups",
		IsWrapper:   true,
	},
	"env": {
		Name:        "env",
		Description: "Run a command in a modified environment",
		IsWrapper:   true,
		DangerousFlags: []string{"-S", "--split-string", "-C", "--chdir",
			"-P", "--default-path"},
		SafeFlags: []string{"-i", "-0", "-v", "-u"},
	},
	"sudo": {
		Name:           "sudo",
		Description:    "Execute as another user",
		IsWrapper:      true,
		DangerousFlags: []string{"-s", "--shell", "-i", "--login"},
	},
	"time": {
		Name:        "time",
		Description: "Time a command",
		IsWrapper:   true,
		IsReadOnly:  true,
	},
	"nice": {
		Name:        "nice",
		Description: "Run a command with modified scheduling priority",
		IsWrapper:   true,
	},
	"stdbuf": {
		Name:        "stdbuf",
		Description: "Run a command with modified I/O buffering",
		IsWrapper:   true,
	},

	// ─── File operations ───
	"rm": {
		Name:           "rm",
		Description:    "Remove files or directories",
		DangerousFlags: []string{"-rf", "-r", "-f", "--recursive", "--force", "--no-preserve-root"},
		SafeFlags:      []string{"-i", "--interactive", "-v", "--verbose", "--help", "--version"},
		RequiresArgs:   true,
		RequiresPath:   true,
	},
	"mv": {
		Name:           "mv",
		Description:    "Move (rename) files",
		DangerousFlags: []string{"-f", "--force"},
		SafeFlags:      []string{"-i", "--interactive", "-n", "--no-clobber", "-v", "--verbose", "--help"},
		RequiresArgs:   true,
		RequiresPath:   true,
	},
	"cp": {
		Name:           "cp",
		Description:    "Copy files and directories",
		DangerousFlags: []string{"-f", "--force"},
		SafeFlags:      []string{"-i", "--interactive", "-n", "--no-clobber", "-r", "-R", "--recursive", "-v", "--verbose", "--help"},
		RequiresArgs:   true,
		RequiresPath:   true,
	},
	"mkdir": {
		Name:         "mkdir",
		Description:  "Create directories",
		SafeFlags:    []string{"-p", "--parents", "-v", "--verbose", "--help"},
		RequiresArgs: true,
		RequiresPath: true,
	},
	"touch": {
		Name:         "touch",
		Description:  "Change file timestamps or create files",
		SafeFlags:    []string{"-c", "--no-create", "--help"},
		RequiresArgs: true,
		RequiresPath: true,
	},
	"chmod": {
		Name:           "chmod",
		Description:    "Change file permissions",
		DangerousFlags: []string{"+s", "4755", "2755", "6755", "777"},
		SafeFlags:      []string{"-v", "--verbose", "--help", "-R", "--recursive"},
		RequiresArgs:   true,
		RequiresPath:   true,
	},
	"chown": {
		Name:         "chown",
		Description:  "Change file owner and group",
		SafeFlags:    []string{"-v", "--verbose", "--help", "-R", "--recursive"},
		RequiresArgs: true,
		RequiresPath: true,
	},

	// ─── Text processing ───
	"cat": {
		Name:         "cat",
		Description:  "Concatenate and display files",
		IsReadOnly:   true,
		SafeFlags:    []string{"-n", "--number", "-b", "--number-nonblank", "-E", "--show-ends", "-T", "--show-tabs", "--help"},
		RequiresPath: true,
	},
	"head": {
		Name:        "head",
		Description: "Output the first part of files",
		IsReadOnly:  true,
		SafeFlags:   []string{"-n", "--lines", "-c", "--bytes", "--help"},
	},
	"tail": {
		Name:        "tail",
		Description: "Output the last part of files",
		IsReadOnly:  true,
		SafeFlags:   []string{"-n", "--lines", "-c", "--bytes", "-f", "--follow", "--help"},
	},
	"tee": {
		Name:           "tee",
		Description:    "Read from stdin and write to stdout and files",
		DangerousFlags: []string{"-a", "--append"},
		RequiresPath:   true,
	},
	"sort": {
		Name:        "sort",
		Description: "Sort lines of text",
		IsReadOnly:  true,
		SafeFlags:   []string{"-r", "--reverse", "-n", "--numeric-sort", "-u", "--unique", "--help"},
	},
	"uniq": {
		Name:        "uniq",
		Description: "Report or omit repeated lines",
		IsReadOnly:  true,
		SafeFlags:   []string{"-c", "--count", "-d", "--repeated", "-u", "--unique", "--help"},
	},
	"wc": {
		Name:        "wc",
		Description: "Word, line, character count",
		IsReadOnly:  true,
		SafeFlags:   []string{"-l", "--lines", "-w", "--words", "-c", "--bytes", "-m", "--chars", "--help"},
	},
	"cut": {
		Name:        "cut",
		Description: "Remove sections from lines",
		IsReadOnly:  true,
		SafeFlags:   []string{"-d", "--delimiter", "-f", "--fields", "-c", "--characters", "--help"},
	},
	"tr": {
		Name:        "tr",
		Description: "Translate or delete characters",
		IsReadOnly:  true,
		SafeFlags:   []string{"-d", "--delete", "-s", "--squeeze-repeats", "--help"},
	},
	"diff": {
		Name:        "diff",
		Description: "Compare files line by line",
		IsReadOnly:  true,
		SafeFlags:   []string{"-u", "--unified", "-r", "--recursive", "-q", "--brief", "--color", "--help"},
	},
	"patch": {
		Name:           "patch",
		Description:    "Apply a diff file to an original",
		RequiresPath:   true,
		SafeFlags:      []string{"--dry-run", "-p", "--strip", "--help"},
		DangerousFlags: []string{},
	},

	// ─── Search ───
	"grep": {
		Name:        "grep",
		Description: "Search for patterns in files",
		IsReadOnly:  true,
		SafeFlags: []string{"-r", "--recursive", "-i", "--ignore-case", "-n", "--line-number",
			"-l", "--files-with-matches", "-c", "--count", "-v", "--invert-match",
			"-w", "--word-regexp", "-E", "--extended-regexp", "-P", "--perl-regexp",
			"-A", "--after-context", "-B", "--before-context", "-C", "--context", "--help"},
	},
	"find": {
		Name:           "find",
		Description:    "Search for files in directory hierarchy",
		IsReadOnly:     true,
		DangerousFlags: []string{"-exec", "-execdir", "-delete", "-ok"},
		SafeFlags:      []string{"-name", "-type", "-size", "-mtime", "-print", "-maxdepth", "-mindepth", "--help"},
		RequiresPath:   true,
	},

	// ─── Text editors ───
	"sed": {
		Name:           "sed",
		Description:    "Stream editor for filtering and transforming text",
		DangerousFlags: []string{"-i", "--in-place"},
		SafeFlags:      []string{"-n", "--quiet", "--silent", "-e", "--expression", "-r", "--regexp-extended", "--help"},
	},
	"awk": {
		Name:        "awk",
		Description: "Pattern scanning and processing language",
		IsReadOnly:  true,
		SafeFlags:   []string{"-F", "--field-separator", "-v", "--help"},
	},

	// ─── Version control ───
	"git": {
		Name:        "git",
		Description: "Distributed version control system",
		SubcommandSpecs: map[string]*CommandSpec{
			"status": {Name: "status", IsReadOnly: true},
			"log":    {Name: "log", IsReadOnly: true},
			"diff":   {Name: "diff", IsReadOnly: true},
			"show":   {Name: "show", IsReadOnly: true},
			"branch": {Name: "branch", IsReadOnly: true, DangerousFlags: []string{"-d", "-D", "--delete", "--force"}},
			"remote": {Name: "remote", IsReadOnly: true},
			"config": {Name: "config", IsReadOnly: true},
			"blame":  {Name: "blame", IsReadOnly: true},
			"reflog": {Name: "reflog", IsReadOnly: true},
			"stash": {
				Name: "stash",
				SubcommandSpecs: map[string]*CommandSpec{
					"list":  {Name: "list", IsReadOnly: true},
					"show":  {Name: "show", IsReadOnly: true},
					"drop":  {Name: "drop", DangerousFlags: []string{}},
					"clear": {Name: "clear", DangerousFlags: []string{}},
				},
			},
			"push":     {Name: "push", DangerousFlags: []string{"--force", "-f", "--force-with-lease", "--delete"}},
			"reset":    {Name: "reset", DangerousFlags: []string{"--hard"}},
			"clean":    {Name: "clean", DangerousFlags: []string{"-f", "--force", "-fd", "-fdx"}},
			"checkout": {Name: "checkout", DangerousFlags: []string{"."}},
			"restore":  {Name: "restore", DangerousFlags: []string{"."}},
			"commit":   {Name: "commit", DangerousFlags: []string{"--amend"}},
			"rebase":   {Name: "rebase", DangerousFlags: []string{"--force-rebase", "-f"}},
		},
	},

	// ─── Containers ───
	"docker": {
		Name:        "docker",
		Description: "Container platform",
		SubcommandSpecs: map[string]*CommandSpec{
			"ps":     {Name: "ps", IsReadOnly: true},
			"images": {Name: "images", IsReadOnly: true},
			"logs":   {Name: "logs", IsReadOnly: true},
			"run":    {Name: "run", DangerousFlags: []string{"--privileged", "--pid=host", "--network=host", "-v", "--volume"}},
			"exec":   {Name: "exec"},
			"rm":     {Name: "rm", DangerousFlags: []string{"-f", "--force"}},
			"rmi":    {Name: "rmi", DangerousFlags: []string{"-f", "--force"}},
			"stop":   {Name: "stop"},
			"build":  {Name: "build"},
		},
	},

	// ─── Package managers ───
	"npm": {
		Name:        "npm",
		Description: "Node.js package manager",
		SubcommandSpecs: map[string]*CommandSpec{
			"install":   {Name: "install"},
			"ci":        {Name: "ci"},
			"run":       {Name: "run"},
			"test":      {Name: "test"},
			"start":     {Name: "start"},
			"build":     {Name: "build"},
			"publish":   {Name: "publish", DangerousFlags: []string{}},
			"unpublish": {Name: "unpublish", DangerousFlags: []string{}},
		},
	},
	"pip": {
		Name:        "pip",
		Description: "Python package manager",
		SubcommandSpecs: map[string]*CommandSpec{
			"install":   {Name: "install"},
			"uninstall": {Name: "uninstall"},
			"list":      {Name: "list", IsReadOnly: true},
			"show":      {Name: "show", IsReadOnly: true},
			"freeze":    {Name: "freeze", IsReadOnly: true},
		},
	},

	// ─── Network ───
	"curl": {
		Name:        "curl",
		Description: "Transfer data with URLs",
		DangerousFlags: []string{"-d", "--data", "--data-binary", "--data-raw", "--data-urlencode",
			"-F", "--form", "-T", "--upload-file", "-X", "--request"},
		SafeFlags: []string{"-s", "--silent", "-o", "--output", "-L", "--location",
			"-H", "--header", "-v", "--verbose", "-k", "--insecure", "--help"},
	},
	"wget": {
		Name:           "wget",
		Description:    "Non-interactive network downloader",
		DangerousFlags: []string{"--post-data", "--post-file"},
		SafeFlags:      []string{"-q", "--quiet", "-O", "--output-document", "--help"},
	},
	"ssh": {
		Name:         "ssh",
		Description:  "OpenSSH remote login",
		RequiresArgs: true,
	},
	"scp": {
		Name:         "scp",
		Description:  "Secure copy (remote file copy)",
		RequiresArgs: true,
		RequiresPath: true,
	},
	"rsync": {
		Name:           "rsync",
		Description:    "Fast, versatile remote file copying",
		DangerousFlags: []string{"--delete", "--delete-before", "--delete-during", "--delete-after"},
		SafeFlags: []string{"-a", "--archive", "-v", "--verbose", "-n", "--dry-run",
			"--progress", "-z", "--compress", "--help"},
		RequiresPath: true,
	},

	// ─── Compression ───
	"tar": {
		Name:         "tar",
		Description:  "Archive utility",
		SafeFlags:    []string{"-t", "--list", "-v", "--verbose", "--help"},
		RequiresPath: true,
	},
	"zip": {
		Name:         "zip",
		Description:  "Package and compress files",
		SafeFlags:    []string{"-r", "--recurse-paths", "-v", "--verbose", "--help"},
		RequiresPath: true,
	},
	"unzip": {
		Name:           "unzip",
		Description:    "Extract compressed files",
		DangerousFlags: []string{"-o"},
		SafeFlags:      []string{"-l", "-t", "-v", "--help"},
		RequiresPath:   true,
	},

	// ─── Cluster ───
	"xargs": {
		Name:        "xargs",
		Description: "Build and execute commands from stdin",
		IsWrapper:   true,
		SafeFlags:   []string{"-n", "-0", "--null", "-I", "--max-procs", "-P", "--help"},
	},
}

// GetCommandSpec returns the spec for a command, or nil if not found.
func GetCommandSpec(name string) *CommandSpec {
	return commandSpecs[name]
}

// GetSubcommandSpec returns the spec for a subcommand (e.g., "git", "push").
func GetSubcommandSpec(command, subcommand string) *CommandSpec {
	spec := commandSpecs[command]
	if spec == nil || spec.SubcommandSpecs == nil {
		return nil
	}
	return spec.SubcommandSpecs[subcommand]
}

// IsDangerousFlag checks if a flag is dangerous for the given command.
func IsDangerousFlag(command, flag string) bool {
	spec := commandSpecs[command]
	if spec == nil {
		return false
	}
	for _, df := range spec.DangerousFlags {
		if flag == df || strings.HasPrefix(flag, df+"=") {
			return true
		}
	}
	return false
}

// IsDangerousFlagForSubcommand checks if a flag is dangerous for a subcommand.
func IsDangerousFlagForSubcommand(command, subcommand, flag string) bool {
	spec := GetSubcommandSpec(command, subcommand)
	if spec == nil {
		return false
	}
	for _, df := range spec.DangerousFlags {
		if flag == df || strings.HasPrefix(flag, df+"=") {
			return true
		}
	}
	return false
}

// RequiresPath returns true if the command operates on file paths.
func RequiresPathArg(command string) bool {
	spec := commandSpecs[command]
	if spec == nil {
		return false
	}
	return spec.RequiresPath
}

// IsWrapperCommand returns true if the command wraps another command.
func IsWrapperCommand(command string) bool {
	spec := commandSpecs[command]
	if spec == nil {
		return false
	}
	return spec.IsWrapper
}

// IsReadOnlyCommandSpec returns true if the command spec says it's read-only.
func IsReadOnlyCommandSpec(command string) bool {
	spec := commandSpecs[command]
	if spec == nil {
		return false
	}
	return spec.IsReadOnly
}

// RegisterCommandSpec adds or overrides a command spec in the registry.
func RegisterCommandSpec(spec *CommandSpec) {
	if spec != nil && spec.Name != "" {
		commandSpecs[spec.Name] = spec
	}
}

// AllCommandSpecs returns all registered command specs (for iteration).
func AllCommandSpecs() map[string]*CommandSpec {
	return commandSpecs
}
