package permissions

import (
	"regexp"
	"strings"
)

// DangerSeverity indicates how dangerous a detected pattern is.
type DangerSeverity int

const (
	// SeverityLow indicates a potentially risky but generally recoverable operation.
	SeverityLow DangerSeverity = iota + 1
	// SeverityMedium indicates a risky operation that could cause significant problems.
	SeverityMedium
	// SeverityHigh indicates a dangerous operation that could cause data loss or system damage.
	SeverityHigh
	// SeverityCritical indicates an extremely dangerous operation (system destruction, etc).
	SeverityCritical
)

// String returns a human-readable name for a DangerSeverity.
func (s DangerSeverity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// DangerousPatternCategory groups dangerous patterns by type.
type DangerousPatternCategory string

const (
	PatternCatSystemDestruction   DangerousPatternCategory = "system-destruction"
	PatternCatDataLoss            DangerousPatternCategory = "data-loss"
	PatternCatPrivilegeEscalation DangerousPatternCategory = "privilege-escalation"
	PatternCatNetworkExfiltration DangerousPatternCategory = "network-exfiltration"
	PatternCatEnvDestruction      DangerousPatternCategory = "environment-destruction"
	PatternCatObfuscation         DangerousPatternCategory = "obfuscation"
	PatternCatPersistence         DangerousPatternCategory = "persistence"
	PatternCatCredentialAccess    DangerousPatternCategory = "credential-access"
	PatternCatCodeExecution       DangerousPatternCategory = "code-execution"
)

// DangerousPattern represents a single dangerous shell pattern with metadata.
type DangerousPattern struct {
	Name        string
	Category    DangerousPatternCategory
	Severity    DangerSeverity
	Pattern     *regexp.Regexp
	Description string
	// ExcludeIfLineContains, when non-empty, suppresses a match if the line
	// containing the matched text also contains this substring. This emulates
	// negative-lookahead behavior, which RE2 (Go's regex engine) does not
	// support.
	ExcludeIfLineContains string
}

// DangerousPatternMatch is returned when a command matches a dangerous pattern.
type DangerousPatternMatch struct {
	PatternName string
	Category    DangerousPatternCategory
	Severity    DangerSeverity
	Description string
	MatchedText string
}

// dangerousPatterns is the compiled list of all dangerous command patterns.
// Initialized once at package load time.
var dangerousPatterns []DangerousPattern

func init() {
	dangerousPatterns = buildDangerousPatterns()
}

func buildDangerousPatterns() []DangerousPattern {
	patterns := []DangerousPattern{
		// =====================================================================
		// System Destruction Patterns
		// =====================================================================
		{
			Name:        "rm-rf-root",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rR][a-zA-Z]*\s+-[a-zA-Z]*[fF]|-[a-zA-Z]*[fF][a-zA-Z]*\s+-[a-zA-Z]*[rR]|-[a-zA-Z]*[rRfF]{2,})\s+/\s*$`),
			Description: "Recursive forced deletion of root filesystem",
		},
		{
			Name:        "rm-rf-slash",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\brm\s+-rf\s+/(?:\s|$)`),
			Description: "rm -rf / destroys the entire filesystem",
		},
		{
			Name:        "rm-rf-wildcard",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\brm\s+-[rR]f?\s+\*`),
			Description: "Recursive deletion with wildcard",
		},
		{
			Name:        "rm-rf-home",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\brm\s+-[rR]f\s+~/?\s*$`),
			Description: "Recursive forced deletion of home directory",
		},
		{
			Name:        "mkfs",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\bmkfs\b`),
			Description: "Creating a filesystem destroys all data on the device",
		},
		{
			Name:        "dd-dev-zero",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\bdd\s+.*if=/dev/(zero|urandom|random)\s+.*of=/dev/`),
			Description: "Writing zeros/random data to a device destroys all data",
		},
		{
			Name:        "dd-to-disk",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bdd\s+.*of=/dev/[sh]d[a-z]`),
			Description: "Direct write to a block device",
		},
		{
			Name:        "wipefs",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\bwipefs\b`),
			Description: "Wipes filesystem signatures from a device",
		},
		{
			Name:        "fdisk",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bfdisk\s+/dev/`),
			Description: "Partition table manipulation can destroy data",
		},
		{
			Name:        "format-command",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\b(parted|gdisk|sgdisk|cfdisk|sfdisk)\b`),
			Description: "Disk partitioning tools can destroy data",
		},
		{
			Name:        "shred",
			Category:    PatternCatSystemDestruction,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bshred\s+`),
			Description: "Securely overwrites files making them unrecoverable",
		},

		// =====================================================================
		// Data Loss Patterns
		// =====================================================================
		{
			Name:        "drop-database",
			Category:    PatternCatDataLoss,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`(?i)\bDROP\s+(DATABASE|SCHEMA)\b`),
			Description: "Dropping a database permanently destroys all data",
		},
		{
			Name:        "drop-table",
			Category:    PatternCatDataLoss,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
			Description: "Dropping a table permanently destroys data",
		},
		{
			Name:        "truncate-table",
			Category:    PatternCatDataLoss,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`(?i)\bTRUNCATE\s+(TABLE\s+)?\w`),
			Description: "Truncating a table removes all rows",
		},
		{
			Name:        "delete-without-where",
			Category:    PatternCatDataLoss,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\w+\s*;`),
			Description: "DELETE without WHERE clause removes all rows",
		},
		{
			Name:        "git-reset-hard",
			Category:    PatternCatDataLoss,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bgit\s+reset\s+--hard\b`),
			Description: "git reset --hard discards all uncommitted changes",
		},
		{
			Name:        "git-clean-force",
			Category:    PatternCatDataLoss,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bgit\s+clean\s+-[a-zA-Z]*f`),
			Description: "git clean -f removes untracked files",
		},
		{
			Name:        "git-push-force",
			Category:    PatternCatDataLoss,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bgit\s+push\s+.*--force\b`),
			Description: "Force push can overwrite remote history",
		},
		{
			Name:        "git-push-force-short",
			Category:    PatternCatDataLoss,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bgit\s+push\s+.*\s-f\b`),
			Description: "Force push can overwrite remote history",
		},
		{
			Name:        "truncate-file-redirect",
			Category:    PatternCatDataLoss,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`>\s*/[a-zA-Z]`),
			Description: "Redirect can truncate existing files",
		},
		{
			Name:        "truncate-with-empty",
			Category:    PatternCatDataLoss,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\btruncate\s+`),
			Description: "truncate command can destroy file contents",
		},

		// =====================================================================
		// Privilege Escalation Patterns
		// =====================================================================
		{
			Name:        "sudo-command",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bsudo\s+`),
			Description: "Running commands with elevated privileges",
		},
		{
			Name:        "su-command",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bsu\s+(-\s+)?root\b`),
			Description: "Switching to root user",
		},
		{
			Name:        "chmod-777",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bchmod\s+(-[rR]\s+)?777\b`),
			Description: "Setting permissions to world-writable",
		},
		{
			Name:        "chmod-suid",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bchmod\s+[ugo]*[+]?s\b`),
			Description: "Setting SUID/SGID bit enables privilege escalation",
		},
		{
			Name:        "chown-root",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bchown\s+(-[rR]\s+)?root\b`),
			Description: "Changing ownership to root",
		},
		{
			Name:        "visudo",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bvisudo\b`),
			Description: "Editing sudoers file can grant arbitrary privilege escalation",
		},
		{
			Name:        "sudoers-edit",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`/etc/sudoers`),
			Description: "Modifying sudoers configuration",
		},
		{
			Name:        "passwd-shadow",
			Category:    PatternCatPrivilegeEscalation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\b(cat|less|more|head|tail|vim?|nano|ed|emacs)\s+/etc/(passwd|shadow)\b`),
			Description: "Accessing password/shadow files",
		},

		// =====================================================================
		// Network Exfiltration Patterns
		// =====================================================================
		{
			Name:        "curl-post-file",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bcurl\s+.*(-d\s*@|-F\s*[a-zA-Z_]*=@|--data-binary\s+@|--upload-file\s)`),
			Description: "curl uploading file data to a remote server",
		},
		{
			Name:        "curl-post-data",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bcurl\s+.*-X\s+POST\s`),
			Description: "curl sending POST request",
		},
		{
			Name:        "wget-post",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bwget\s+.*--post-(data|file)\b`),
			Description: "wget sending POST data",
		},
		{
			Name:        "nc-listen",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\b(nc|ncat|netcat)\s+.*-[a-zA-Z]*l`),
			Description: "Listening network connection (potential reverse shell or exfiltration)",
		},
		{
			Name:        "nc-connect",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\b(nc|ncat|netcat)\s+\S+\s+\d+`),
			Description: "Outbound network connection via netcat",
		},
		{
			Name:        "ssh-tunnel",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bssh\s+.*-[LRD]\s`),
			Description: "SSH tunneling/port forwarding",
		},
		{
			Name:        "scp-upload",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bscp\s+[^:]+\s+\S+:`),
			Description: "Copying files to remote server via scp",
		},
		{
			Name:        "rsync-remote",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\brsync\s+.*\S+:\S+`),
			Description: "Syncing files to/from remote server",
		},
		{
			Name:        "gh-gist-create-public",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bgh\s+gist\s+create\s+.*--public\b`),
			Description: "Creating a public gist can expose sensitive data",
		},
		{
			Name:        "pipe-to-nc",
			Category:    PatternCatNetworkExfiltration,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\|\s*(nc|ncat|netcat)\s`),
			Description: "Piping data to netcat for exfiltration",
		},

		// =====================================================================
		// Environment Destruction Patterns
		// =====================================================================
		{
			Name:        "unset-path",
			Category:    PatternCatEnvDestruction,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bunset\s+PATH\b`),
			Description: "Unsetting PATH breaks command resolution",
		},
		{
			Name:        "export-path-empty",
			Category:    PatternCatEnvDestruction,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bexport\s+PATH\s*=\s*["']?\s*["']?`),
			Description: "Setting PATH to empty breaks command resolution",
		},
		{
			Name:                  "export-path-overwrite",
			Category:              PatternCatEnvDestruction,
			Severity:              SeverityLow,
			Pattern:               regexp.MustCompile(`\bexport\s+PATH\s*=`),
			Description:           "Overwriting PATH without preserving existing (should include $PATH)",
			ExcludeIfLineContains: "$PATH",
		},
		{
			Name:        "unset-home",
			Category:    PatternCatEnvDestruction,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bunset\s+HOME\b`),
			Description: "Unsetting HOME breaks many programs",
		},
		{
			Name:        "rm-bashrc",
			Category:    PatternCatEnvDestruction,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\brm\s+.*~/?\.(bashrc|bash_profile|zshrc|profile|zprofile)\b`),
			Description: "Deleting shell configuration files",
		},
		{
			Name:        "edit-shell-config",
			Category:    PatternCatEnvDestruction,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`>\s*~/?\.(bashrc|bash_profile|zshrc|profile|zprofile)\b`),
			Description: "Overwriting shell configuration files via redirect",
		},

		// =====================================================================
		// Obfuscation Detection Patterns
		// =====================================================================
		{
			Name:        "base64-decode-pipe",
			Category:    PatternCatObfuscation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bbase64\s+(-d|--decode)\b.*\|`),
			Description: "Decoding base64 and piping to another command (potential obfuscated payload)",
		},
		{
			Name:        "base64-decode-exec",
			Category:    PatternCatObfuscation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\|\s*base64\s+(-d|--decode)\s*\|\s*(bash|sh|zsh|eval)\b`),
			Description: "Decoding and executing base64-encoded commands",
		},
		{
			Name:        "eval-command",
			Category:    PatternCatObfuscation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\beval\s+`),
			Description: "eval executes dynamically constructed commands",
		},
		{
			Name:        "hex-escape-exec",
			Category:    PatternCatObfuscation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\\x[0-9a-fA-F]{2}.*\\x[0-9a-fA-F]{2}`),
			Description: "Hex-escaped content may be obfuscating malicious commands",
		},
		{
			Name:        "printf-decode-exec",
			Category:    PatternCatObfuscation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bprintf\s+.*\\x.*\|\s*(bash|sh|zsh)\b`),
			Description: "Using printf with hex escapes to execute obfuscated commands",
		},
		{
			Name:        "python-exec",
			Category:    PatternCatObfuscation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bpython[23]?\s+-c\s+.*exec\(`),
			Description: "Python exec() can run obfuscated code",
		},
		{
			Name:        "curl-pipe-bash",
			Category:    PatternCatObfuscation,
			Severity:    SeverityCritical,
			Pattern:     regexp.MustCompile(`\b(curl|wget)\s+.*\|\s*(bash|sh|zsh|sudo\s+bash|sudo\s+sh)\b`),
			Description: "Downloading and executing remote scripts without review",
		},
		{
			Name:        "curl-pipe-python",
			Category:    PatternCatObfuscation,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\b(curl|wget)\s+.*\|\s*python[23]?\b`),
			Description: "Downloading and executing remote Python scripts",
		},
		{
			Name:        "xxd-decode",
			Category:    PatternCatObfuscation,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bxxd\s+-r\b.*\|`),
			Description: "Hex decoding and piping (potential obfuscation)",
		},

		// =====================================================================
		// Persistence Patterns
		// =====================================================================
		{
			Name:        "crontab-edit",
			Category:    PatternCatPersistence,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bcrontab\s+-[eir]\b`),
			Description: "Modifying cron jobs for persistent execution",
		},
		{
			Name:        "cron-directory",
			Category:    PatternCatPersistence,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`/etc/cron\.(d|daily|hourly|weekly|monthly)/`),
			Description: "Writing to cron directories for persistent execution",
		},
		{
			Name:        "systemd-service",
			Category:    PatternCatPersistence,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bsystemctl\s+(enable|start|reload|daemon-reload)\b`),
			Description: "Enabling/starting systemd services for persistence",
		},
		{
			Name:        "systemd-write",
			Category:    PatternCatPersistence,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`/etc/systemd/system/.*\.service`),
			Description: "Writing systemd service files for persistence",
		},
		{
			Name:        "init-script",
			Category:    PatternCatPersistence,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`/etc/init\.d/`),
			Description: "Writing init scripts for persistent execution",
		},
		{
			Name:        "rc-local",
			Category:    PatternCatPersistence,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`/etc/rc\.local`),
			Description: "Modifying rc.local for boot-time execution",
		},
		{
			Name:        "ssh-authorized-keys",
			Category:    PatternCatPersistence,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.ssh/authorized_keys`),
			Description: "Modifying SSH authorized keys for persistent access",
		},
		{
			Name:        "at-job",
			Category:    PatternCatPersistence,
			Severity:    SeverityLow,
			Pattern:     regexp.MustCompile(`\bat\s+`),
			Description: "Scheduling a one-time job with at",
		},
		{
			Name:        "launchd-plist",
			Category:    PatternCatPersistence,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`/Library/Launch(Agents|Daemons)/`),
			Description: "Writing macOS LaunchAgent/Daemon for persistent execution",
		},
		{
			Name:        "git-hook-write",
			Category:    PatternCatPersistence,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\.git/hooks/`),
			Description: "Writing git hooks for code execution on git operations",
		},

		// =====================================================================
		// Credential Access Patterns
		// =====================================================================
		{
			Name:        "ssh-key-access",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.ssh/id_(rsa|dsa|ecdsa|ed25519)`),
			Description: "Accessing SSH private keys",
		},
		{
			Name:        "aws-credentials",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.aws/credentials`),
			Description: "Accessing AWS credential files",
		},
		{
			Name:        "gcloud-credentials",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.config/gcloud/`),
			Description: "Accessing Google Cloud credential files",
		},
		{
			Name:        "kube-config",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.kube/config`),
			Description: "Accessing Kubernetes configuration with credentials",
		},
		{
			Name:        "docker-config",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\.docker/config\.json`),
			Description: "Accessing Docker registry credentials",
		},
		{
			Name:        "env-file-access",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\b(cat|less|more|head|tail)\s+.*\.env\b`),
			Description: "Reading .env files that may contain secrets",
		},
		{
			Name:        "npm-token",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.npmrc`),
			Description: "Accessing npm auth token configuration",
		},
		{
			Name:        "pypirc",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.pypirc`),
			Description: "Accessing PyPI auth configuration",
		},
		{
			Name:        "netrc",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\.(netrc|curlrc)`),
			Description: "Accessing credentials stored in .netrc",
		},
		{
			Name:        "keychain-access",
			Category:    PatternCatCredentialAccess,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bsecurity\s+(find-generic-password|find-internet-password)\b`),
			Description: "Accessing macOS Keychain credentials",
		},

		// =====================================================================
		// Remote Code Execution Patterns
		// =====================================================================
		{
			Name:        "git-config-hooks",
			Category:    PatternCatCodeExecution,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bgit\s+config\s+.*core\.(sshCommand|hooksPath)\b`),
			Description: "git config core.sshCommand/hooksPath can execute arbitrary code",
		},
		{
			Name:        "ssh-command-exec",
			Category:    PatternCatCodeExecution,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bssh\s+.*\s+['"]*[a-zA-Z]`),
			Description: "Executing commands on a remote server via SSH",
		},
		{
			Name:        "docker-exec",
			Category:    PatternCatCodeExecution,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bdocker\s+(exec|run)\b`),
			Description: "Executing commands in a Docker container",
		},
		{
			Name:        "docker-privileged",
			Category:    PatternCatCodeExecution,
			Severity:    SeverityHigh,
			Pattern:     regexp.MustCompile(`\bdocker\s+run\s+.*--privileged\b`),
			Description: "Running Docker container with full host privileges",
		},
		{
			Name:        "kubectl-exec",
			Category:    PatternCatCodeExecution,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bkubectl\s+exec\b`),
			Description: "Executing commands in a Kubernetes pod",
		},
		{
			Name:        "kubectl-apply",
			Category:    PatternCatCodeExecution,
			Severity:    SeverityMedium,
			Pattern:     regexp.MustCompile(`\bkubectl\s+(apply|create|delete)\b`),
			Description: "Modifying Kubernetes resources",
		},
	}

	return patterns
}

// DetectDangerousPatterns checks a command against all known dangerous patterns.
// Returns all matches found.
func DetectDangerousPatterns(command string) []DangerousPatternMatch {
	var matches []DangerousPatternMatch

	for _, dp := range dangerousPatterns {
		loc := dp.Pattern.FindStringIndex(command)
		if loc == nil {
			continue
		}
		// Emulate negative lookahead: skip the match if the surrounding line
		// contains the excluded substring (used to express things like
		// "export PATH=... unless the value references $PATH").
		if dp.ExcludeIfLineContains != "" {
			lineStart := strings.LastIndexByte(command[:loc[0]], '\n') + 1
			lineEnd := loc[1] + strings.IndexByte(command[loc[1]:], '\n')
			if lineEnd < loc[1] {
				lineEnd = len(command)
			}
			if strings.Contains(command[lineStart:lineEnd], dp.ExcludeIfLineContains) {
				continue
			}
		}
		matches = append(matches, DangerousPatternMatch{
			PatternName: dp.Name,
			Category:    dp.Category,
			Severity:    dp.Severity,
			Description: dp.Description,
			MatchedText: command[loc[0]:loc[1]],
		})
	}

	return matches
}

// MaxDangerSeverity returns the highest severity among matches, or 0 if none.
func MaxDangerSeverity(matches []DangerousPatternMatch) DangerSeverity {
	var max DangerSeverity
	for _, m := range matches {
		if m.Severity > max {
			max = m.Severity
		}
	}
	return max
}

// IsDangerousBashPermission checks if a permission rule content matches a known
// dangerous pattern that could bypass security (e.g., allowing "python:*"
// would let the model run arbitrary code). Based on DANGEROUS_BASH_PATTERNS
// from the TypeScript source.
func IsDangerousBashPermission(ruleContent string) bool {
	content := strings.TrimSpace(ruleContent)

	// Check for patterns like "python:*", "python *", "python -*"
	for _, pattern := range DangerousBashPrefixes {
		// Exact match
		if content == pattern {
			return true
		}
		// Prefix with wildcard: "python:*", "python *", "python -*"
		if strings.HasPrefix(content, pattern+":") ||
			strings.HasPrefix(content, pattern+" ") ||
			strings.HasPrefix(content, pattern+"\t") {
			// Check if the rest is a wildcard
			rest := content[len(pattern):]
			rest = strings.TrimLeft(rest, ": \t")
			if rest == "*" || rest == "-*" || strings.HasSuffix(rest, "*") {
				return true
			}
		}
	}
	return false
}

// DangerousBashPrefixes is the list of command prefixes that are dangerous
// to allow as broad permission rules. Corresponds to DANGEROUS_BASH_PATTERNS
// in the TypeScript source.
var DangerousBashPrefixes = []string{
	// Cross-platform code execution entry points
	"python", "python3", "python2",
	"node", "deno", "tsx",
	"ruby", "perl", "php", "lua",
	"npx", "bunx",
	"npm run", "yarn run", "pnpm run", "bun run",
	"bash", "sh",
	"ssh",
	// Unix-specific
	"zsh", "fish",
	"eval", "exec", "env",
	"xargs", "sudo",
}
