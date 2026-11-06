package powershell

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ─── Security validation result ────────────────────────────────────────

// SecurityResult describes the outcome of command security analysis.
type SecurityResult struct {
	Behavior string // "allow", "ask", "deny"
	Message  string
}

// ─── Cmdlet classification ─────────────────────────────────────────────

// CmdletClass categorizes the danger level of a cmdlet.
type CmdletClass string

const (
	CmdletReadOnly    CmdletClass = "read-only"
	CmdletWrite       CmdletClass = "write"
	CmdletDestructive CmdletClass = "destructive"
	CmdletDangerous   CmdletClass = "dangerous"
	CmdletUnknown     CmdletClass = "unknown"
)

// DangerousCmdlets are cmdlets that are always dangerous and should be blocked
// or require explicit confirmation.
var DangerousCmdlets = map[string]string{
	"remove-item":             "Deletes files/directories",
	"format-volume":           "Formats a disk volume (DATA LOSS)",
	"clear-disk":              "Clears a disk (DATA LOSS)",
	"clear-content":           "Clears file content",
	"stop-process":            "Kills processes",
	"stop-service":            "Stops system services",
	"stop-computer":           "Shuts down the computer",
	"restart-computer":        "Restarts the computer",
	"clear-recyclebin":        "Permanently deletes recycled files",
	"invoke-expression":       "Executes arbitrary code (eval)",
	"invoke-command":          "Executes commands (potentially remote)",
	"start-process":           "Launches processes",
	"new-psdrive":             "Creates drive mappings",
	"set-executionpolicy":     "Changes script execution policy",
	"unregister-scheduledjob": "Removes scheduled jobs",
	"disable-computerrestore": "Disables system restore",
	"set-mppreference":        "Modifies Windows Defender settings",
}

// DestructivePatterns are regex patterns that match destructive commands.
var DestructivePatterns = []struct {
	Pattern *regexp.Regexp
	Message string
}{
	{regexp.MustCompile(`(?i)(?:^|[|;&\n({])\s*(?:Remove-Item|rm|del|rd|rmdir|ri)\b[^|;&\n}]*-Recurse\b[^|;&\n}]*-Force\b`),
		"Recursive forced removal of files/directories"},
	{regexp.MustCompile(`(?i)(?:^|[|;&\n({])\s*(?:Remove-Item|rm|del|rd|rmdir|ri)\b[^|;&\n}]*-Force\b[^|;&\n}]*-Recurse\b`),
		"Recursive forced removal of files/directories"},
	{regexp.MustCompile(`(?i)(?:^|[|;&\n({])\s*(?:Remove-Item|rm|del|rd|rmdir|ri)\b[^|;&\n}]*-Recurse\b`),
		"Recursive removal of files"},
	{regexp.MustCompile(`(?i)\bClear-Content\b[^|;&\n]*\*`),
		"Clearing content of multiple files"},
	{regexp.MustCompile(`(?i)\bFormat-Volume\b`),
		"Formatting a disk volume"},
	{regexp.MustCompile(`(?i)\bClear-Disk\b`),
		"Clearing a disk"},
	{regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`),
		"Discarding uncommitted changes"},
	{regexp.MustCompile(`(?i)\bgit\s+push\b[^|;&\n]*\s+(?:--force|--force-with-lease|-f)\b`),
		"Force-pushing may overwrite remote history"},
	{regexp.MustCompile(`(?i)\bgit\s+clean\b[^|;&\n]*-[a-zA-Z]*f`),
		"Permanently deleting untracked files"},
	{regexp.MustCompile(`(?i)\bgit\s+stash\s+(?:drop|clear)\b`),
		"Permanently removing stashed changes"},
	{regexp.MustCompile(`(?i)\bStop-Computer\b`),
		"Shutting down the computer"},
	{regexp.MustCompile(`(?i)\bRestart-Computer\b`),
		"Restarting the computer"},
	{regexp.MustCompile(`(?i)\bClear-RecycleBin\b`),
		"Permanently deleting recycled files"},
	{regexp.MustCompile(`(?i)\b(?:DROP|TRUNCATE)\s+(?:TABLE|DATABASE|SCHEMA)\b`),
		"Dropping or truncating database objects"},
}

// DangerousScriptBlockCmdlets are cmdlets that accept script blocks and
// can execute arbitrary code.
var DangerousScriptBlockCmdlets = map[string]bool{
	"invoke-command":        true,
	"invoke-expression":     true,
	"start-job":             true,
	"start-threadjob":       true,
	"register-scheduledjob": true,
	"register-objectevent":  true,
	"register-engineevent":  true,
	"register-wmievent":     true,
	"new-pssessionoption":   true,
}

// ModuleLoadingCmdlets are cmdlets that load/install modules and can
// execute arbitrary code from .psm1 files.
var ModuleLoadingCmdlets = map[string]bool{
	"import-module":      true,
	"install-module":     true,
	"save-module":        true,
	"install-script":     true,
	"save-script":        true,
	"install-psresource": true,
	"save-psresource":    true,
}

// FilePathExecutionCmdlets are cmdlets that execute scripts from -FilePath.
var FilePathExecutionCmdlets = map[string]bool{
	"invoke-command":        true,
	"start-job":             true,
	"start-threadjob":       true,
	"register-scheduledjob": true,
}

// ScheduledTaskCmdlets create/modify persistence primitives.
var ScheduledTaskCmdlets = map[string]bool{
	"register-scheduledtask":  true,
	"new-scheduledtask":       true,
	"new-scheduledtaskaction": true,
	"set-scheduledtask":       true,
}

// RuntimeStateCmdlets can hijack future command resolution.
var RuntimeStateCmdlets = map[string]bool{
	"set-alias":    true,
	"new-alias":    true,
	"set-variable": true,
	"new-variable": true,
}

// WMISpawnCmdlets can spawn arbitrary processes via WMI.
var WMISpawnCmdlets = map[string]bool{
	"invoke-wmimethod": true,
	"invoke-cimmethod": true,
}

// DownloaderCmdlets download files from the network.
var DownloaderCmdlets = map[string]bool{
	"invoke-webrequest":  true,
	"invoke-restmethod":  true,
	"new-object":         true,
	"start-bitstransfer": true,
}

// PowerShellExecutables are PS executable names that indicate nested invocation.
var PowerShellExecutables = map[string]bool{
	"pwsh":           true,
	"pwsh.exe":       true,
	"powershell":     true,
	"powershell.exe": true,
}

// CommonAliases maps PowerShell aliases to their canonical cmdlet names.
var CommonAliases = map[string]string{
	"rm":      "remove-item",
	"del":     "remove-item",
	"rd":      "remove-item",
	"rmdir":   "remove-item",
	"ri":      "remove-item",
	"erase":   "remove-item",
	"cd":      "set-location",
	"sl":      "set-location",
	"chdir":   "set-location",
	"pushd":   "push-location",
	"popd":    "pop-location",
	"ls":      "get-childitem",
	"dir":     "get-childitem",
	"gci":     "get-childitem",
	"cat":     "get-content",
	"gc":      "get-content",
	"type":    "get-content",
	"cp":      "copy-item",
	"copy":    "copy-item",
	"ci":      "copy-item",
	"mv":      "move-item",
	"move":    "move-item",
	"mi":      "move-item",
	"ren":     "rename-item",
	"rni":     "rename-item",
	"cls":     "clear-host",
	"clear":   "clear-host",
	"echo":    "write-output",
	"write":   "write-output",
	"ps":      "get-process",
	"gps":     "get-process",
	"kill":    "stop-process",
	"spps":    "stop-process",
	"iex":     "invoke-expression",
	"icm":     "invoke-command",
	"iwr":     "invoke-webrequest",
	"irm":     "invoke-restmethod",
	"saps":    "start-process",
	"start":   "start-process",
	"ni":      "new-item",
	"mkdir":   "new-item",
	"md":      "new-item",
	"si":      "set-item",
	"cli":     "clear-item",
	"sc":      "set-content",
	"ac":      "add-content",
	"select":  "select-object",
	"where":   "where-object",
	"%":       "foreach-object",
	"foreach": "foreach-object",
	"sort":    "sort-object",
	"group":   "group-object",
	"measure": "measure-object",
	"ft":      "format-table",
	"fl":      "format-list",
	"fw":      "format-wide",
	"fc":      "format-custom",
	"oh":      "out-host",
	"sal":     "set-alias",
	"nal":     "new-alias",
	"sv":      "set-variable",
	"nv":      "new-variable",
	"ii":      "invoke-item",
	"iwmi":    "invoke-wmimethod",
}

// ─── Security analysis ────────────────────────────────────────────────

// ValidateCommand performs comprehensive security analysis on a PowerShell
// command. Returns the security result with behavior and message.
func ValidateCommand(command string) SecurityResult {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return SecurityResult{Behavior: "allow", Message: "Empty command"}
	}

	// Run all security checks.
	checks := []func(string) SecurityResult{
		checkInvokeExpression,
		checkEncodedCommand,
		checkNestedPowerShell,
		checkDownloadCradles,
		checkDownloadUtilities,
		checkAddType,
		checkComObject,
		checkInvokeItem,
		checkScheduledTasks,
		checkModuleLoading,
		checkRuntimeState,
		checkWMISpawn,
		checkScriptBlocks,
		checkSubExpressions,
		checkSplatting,
		checkStopParsing,
		checkMemberInvocations,
		checkEnvVarManipulation,
		checkDangerousPatterns,
	}

	for _, check := range checks {
		result := check(trimmed)
		if result.Behavior != "allow" {
			return result
		}
	}

	return SecurityResult{Behavior: "allow"}
}

// ClassifyCmdlet returns the danger classification of a cmdlet name.
func ClassifyCmdlet(name string) CmdletClass {
	canonical := ResolveToCanonical(name)

	if _, ok := DangerousCmdlets[canonical]; ok {
		return CmdletDestructive
	}

	if DangerousScriptBlockCmdlets[canonical] ||
		ModuleLoadingCmdlets[canonical] ||
		ScheduledTaskCmdlets[canonical] ||
		WMISpawnCmdlets[canonical] {
		return CmdletDangerous
	}

	// Write cmdlets.
	writeCmdlets := map[string]bool{
		"set-content":   true,
		"add-content":   true,
		"out-file":      true,
		"new-item":      true,
		"copy-item":     true,
		"move-item":     true,
		"rename-item":   true,
		"set-item":      true,
		"clear-item":    true,
		"export-csv":    true,
		"export-clixml": true,
		"tee-object":    true,
	}
	if writeCmdlets[canonical] {
		return CmdletWrite
	}

	// Read-only cmdlets.
	readOnlyCmdlets := map[string]bool{
		"get-childitem":    true,
		"get-content":      true,
		"get-item":         true,
		"get-itemproperty": true,
		"test-path":        true,
		"resolve-path":     true,
		"get-filehash":     true,
		"get-acl":          true,
		"select-string":    true,
		"get-process":      true,
		"get-service":      true,
		"get-location":     true,
		"get-date":         true,
		"get-host":         true,
		"get-computerinfo": true,
		"get-member":       true,
		"get-unique":       true,
		"compare-object":   true,
		"convertto-json":   true,
		"convertfrom-json": true,
		"write-output":     true,
		"write-host":       true,
		"format-table":     true,
		"format-list":      true,
		"select-object":    true,
		"sort-object":      true,
		"where-object":     true,
		"measure-object":   true,
		"group-object":     true,
	}
	if readOnlyCmdlets[canonical] {
		return CmdletReadOnly
	}

	return CmdletUnknown
}

// ResolveToCanonical resolves a cmdlet name or alias to its canonical form.
func ResolveToCanonical(name string) string {
	lower := strings.ToLower(name)
	// Strip Windows PATHEXT extensions from bare names.
	if !strings.Contains(lower, "\\") && !strings.Contains(lower, "/") {
		for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
			lower = strings.TrimSuffix(lower, ext)
		}
	}
	if canonical, ok := CommonAliases[lower]; ok {
		return canonical
	}
	return lower
}

// ─── Individual security checks ────────────────────────────────────────

func checkInvokeExpression(cmd string) SecurityResult {
	if containsCmdlet(cmd, "invoke-expression") || containsCmdlet(cmd, "iex") {
		return SecurityResult{
			Behavior: "ask",
			Message:  "Command uses Invoke-Expression which can execute arbitrary code",
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkEncodedCommand(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	for _, exe := range []string{"pwsh", "powershell", "pwsh.exe", "powershell.exe"} {
		if strings.Contains(lower, exe) {
			if matchParamAbbrev(lower, "-encodedcommand", "-e") {
				return SecurityResult{
					Behavior: "ask",
					Message:  "Command uses encoded parameters which obscure intent",
				}
			}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkNestedPowerShell(cmd string) SecurityResult {
	tokens := extractCommandTokens(cmd)
	for _, tok := range tokens {
		if PowerShellExecutables[strings.ToLower(tok)] {
			return SecurityResult{
				Behavior: "ask",
				Message:  "Command spawns a nested PowerShell process which cannot be validated",
			}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkDownloadCradles(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	hasDownloader := false
	hasIex := false
	for dl := range DownloaderCmdlets {
		if strings.Contains(lower, dl) {
			hasDownloader = true
			break
		}
	}
	if strings.Contains(lower, "invoke-expression") || strings.Contains(lower, "iex") {
		hasIex = true
	}
	if hasDownloader && hasIex {
		return SecurityResult{
			Behavior: "ask",
			Message:  "Command downloads and executes remote code",
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkDownloadUtilities(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "start-bitstransfer") {
		return SecurityResult{Behavior: "ask", Message: "Command downloads files via BITS transfer"}
	}
	if (strings.Contains(lower, "certutil") || strings.Contains(lower, "certutil.exe")) &&
		(strings.Contains(lower, "-urlcache") || strings.Contains(lower, "/urlcache")) {
		return SecurityResult{Behavior: "ask", Message: "Command uses certutil to download from a URL"}
	}
	if (strings.Contains(lower, "bitsadmin") || strings.Contains(lower, "bitsadmin.exe")) &&
		strings.Contains(lower, "/transfer") {
		return SecurityResult{Behavior: "ask", Message: "Command downloads files via BITS transfer"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkAddType(cmd string) SecurityResult {
	if containsCmdlet(cmd, "add-type") {
		return SecurityResult{Behavior: "ask", Message: "Command compiles and loads .NET code"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkComObject(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "new-object") && matchParamAbbrev(lower, "-comobject", "-com") {
		return SecurityResult{Behavior: "ask", Message: "Command instantiates a COM object which may have execution capabilities"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkInvokeItem(cmd string) SecurityResult {
	if containsCmdlet(cmd, "invoke-item") || containsCmdlet(cmd, "ii") {
		return SecurityResult{Behavior: "ask", Message: "Invoke-Item opens files with the default handler. On executable files this runs arbitrary code."}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkScheduledTasks(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	for task := range ScheduledTaskCmdlets {
		if strings.Contains(lower, task) {
			return SecurityResult{Behavior: "ask", Message: fmt.Sprintf("%s creates or modifies a scheduled task (persistence primitive)", task)}
		}
	}
	if (strings.Contains(lower, "schtasks") || strings.Contains(lower, "schtasks.exe")) &&
		(strings.Contains(lower, "/create") || strings.Contains(lower, "/change") ||
			strings.Contains(lower, "-create") || strings.Contains(lower, "-change")) {
		return SecurityResult{Behavior: "ask", Message: "schtasks with create/change modifies scheduled tasks"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkModuleLoading(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	for mod := range ModuleLoadingCmdlets {
		if strings.Contains(lower, mod) {
			return SecurityResult{Behavior: "ask", Message: "Command loads, installs, or downloads a PowerShell module, which can execute arbitrary code"}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkRuntimeState(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	for rs := range RuntimeStateCmdlets {
		if strings.Contains(lower, rs) {
			return SecurityResult{Behavior: "ask", Message: "Command creates or modifies an alias or variable that can affect future command resolution"}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkWMISpawn(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	for wmi := range WMISpawnCmdlets {
		if strings.Contains(lower, wmi) {
			return SecurityResult{Behavior: "ask", Message: fmt.Sprintf("%s can spawn arbitrary processes via WMI/CIM", wmi)}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkScriptBlocks(cmd string) SecurityResult {
	// Detect script blocks { ... } that aren't inside safe cmdlets.
	if strings.Contains(cmd, "{") && strings.Contains(cmd, "}") {
		// Check if the script block is in a safe context.
		lower := strings.ToLower(cmd)
		safeCmdlets := []string{"where-object", "sort-object", "select-object",
			"group-object", "format-table", "format-list", "format-wide", "format-custom"}
		for _, safe := range safeCmdlets {
			if strings.Contains(lower, safe) {
				return SecurityResult{Behavior: "allow"}
			}
		}
		// Check for dangerous script block cmdlets.
		for dsc := range DangerousScriptBlockCmdlets {
			if strings.Contains(lower, dsc) {
				return SecurityResult{Behavior: "ask", Message: "Command contains script block with dangerous cmdlet"}
			}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkSubExpressions(cmd string) SecurityResult {
	if strings.Contains(cmd, "$(") {
		return SecurityResult{Behavior: "ask", Message: "Command contains subexpressions $()"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkSplatting(cmd string) SecurityResult {
	if matched, _ := regexp.MatchString(`(?:^|[^\w.])@\w+`, cmd); matched {
		return SecurityResult{Behavior: "ask", Message: "Command uses splatting (@variable)"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkStopParsing(cmd string) SecurityResult {
	if strings.Contains(cmd, "--%") {
		return SecurityResult{Behavior: "ask", Message: "Command uses stop-parsing token (--%%)"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkMemberInvocations(cmd string) SecurityResult {
	if matched, _ := regexp.MatchString(`\.\w+\s*\(`, cmd); matched {
		return SecurityResult{Behavior: "ask", Message: "Command invokes .NET methods"}
	}
	if strings.Contains(cmd, "::") {
		return SecurityResult{Behavior: "ask", Message: "Command uses static method invocation"}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkEnvVarManipulation(cmd string) SecurityResult {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "$env:") && !strings.Contains(lower, "env:") {
		return SecurityResult{Behavior: "allow"}
	}

	// Check for assignment patterns.
	if matched, _ := regexp.MatchString(`\$env:\w+\s*=`, cmd); matched {
		return SecurityResult{Behavior: "ask", Message: "Command modifies environment variables"}
	}

	envWriteCmdlets := []string{"set-item", "new-item", "remove-item", "clear-item",
		"set-content", "add-content"}
	for _, c := range envWriteCmdlets {
		if strings.Contains(lower, c) {
			return SecurityResult{Behavior: "ask", Message: "Command modifies environment variables"}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func checkDangerousPatterns(cmd string) SecurityResult {
	for _, dp := range DestructivePatterns {
		if dp.Pattern.MatchString(cmd) {
			return SecurityResult{Behavior: "ask", Message: dp.Message}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

// ─── Parameter validation ─────────────────────────────────────────────

// ValidateParameters checks for dangerous parameter combinations.
func ValidateParameters(cmdlet string, args []string) SecurityResult {
	canonical := ResolveToCanonical(cmdlet)

	switch canonical {
	case "remove-item":
		return validateRemoveItemParams(args)
	case "stop-process":
		return validateStopProcessParams(args)
	case "start-process":
		return validateStartProcessParams(args)
	}

	return SecurityResult{Behavior: "allow"}
}

func validateRemoveItemParams(args []string) SecurityResult {
	hasRecurse := false
	hasForce := false
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "-recurse" || strings.HasPrefix(lower, "-r") && "-recurse"[:len(lower)] == lower {
			hasRecurse = true
		}
		if lower == "-force" || lower == "-f" {
			hasForce = true
		}
	}

	if hasRecurse && hasForce {
		return SecurityResult{Behavior: "ask", Message: "Remove-Item with -Recurse -Force can cause data loss"}
	}
	if hasRecurse {
		return SecurityResult{Behavior: "ask", Message: "Remove-Item with -Recurse removes directory trees"}
	}
	return SecurityResult{Behavior: "allow"}
}

func validateStopProcessParams(args []string) SecurityResult {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "-force" || lower == "-f" {
			return SecurityResult{Behavior: "ask", Message: "Stop-Process with -Force kills processes without cleanup"}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

func validateStartProcessParams(args []string) SecurityResult {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "runas") {
			return SecurityResult{Behavior: "ask", Message: "Start-Process requests elevated privileges (RunAs)"}
		}
	}
	// Check if target is a PowerShell executable.
	for _, arg := range args {
		stripped := strings.Trim(arg, "'\"")
		if PowerShellExecutables[strings.ToLower(stripped)] {
			return SecurityResult{Behavior: "ask", Message: "Start-Process launches a nested PowerShell process"}
		}
	}
	return SecurityResult{Behavior: "allow"}
}

// ─── Pipeline analysis ─────────────────────────────────────────────────

// AnalyzePipeline checks for dangerous pipe chains.
func AnalyzePipeline(command string) SecurityResult {
	// Split on pipe.
	segments := strings.Split(command, "|")
	if len(segments) < 2 {
		return SecurityResult{Behavior: "allow"}
	}

	// Check for download | execute patterns.
	hasDownloader := false
	hasExecutor := false
	for _, seg := range segments {
		lower := strings.ToLower(strings.TrimSpace(seg))
		firstWord := strings.Fields(lower)
		if len(firstWord) == 0 {
			continue
		}
		canonical := ResolveToCanonical(firstWord[0])

		if DownloaderCmdlets[canonical] {
			hasDownloader = true
		}
		if canonical == "invoke-expression" || canonical == "invoke-command" {
			hasExecutor = true
		}
	}

	if hasDownloader && hasExecutor {
		return SecurityResult{
			Behavior: "ask",
			Message:  "Pipeline downloads and executes remote code",
		}
	}

	return SecurityResult{Behavior: "allow"}
}

// ─── Execution policy ─────────────────────────────────────────────────

// CheckExecutionPolicy checks the current PowerShell execution policy.
func CheckExecutionPolicy() (string, error) {
	psPath, _ := DetectPowerShell()
	if psPath == "" {
		return "", fmt.Errorf("PowerShell not available")
	}

	cmd := exec.Command(psPath, "-NoProfile", "-NonInteractive", "-Command",
		"Get-ExecutionPolicy")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get execution policy: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// ─── Helpers ──────────────────────────────────────────────────────────

// containsCmdlet checks if a command contains a cmdlet (case-insensitive,
// word-boundary aware).
func containsCmdlet(cmd, cmdlet string) bool {
	lower := strings.ToLower(cmd)
	cmdletLower := strings.ToLower(cmdlet)

	idx := strings.Index(lower, cmdletLower)
	if idx < 0 {
		return false
	}

	// Check word boundaries.
	if idx > 0 {
		c := lower[idx-1]
		if isWordByte(c) {
			return false
		}
	}
	end := idx + len(cmdletLower)
	if end < len(lower) {
		c := lower[end]
		if isWordByte(c) {
			return false
		}
	}
	return true
}

func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_' || c == '-'
}

// matchParamAbbrev checks if a command string contains a parameter
// matching the given abbreviation prefix.
func matchParamAbbrev(cmd, fullParam, minPrefix string) bool {
	lower := strings.ToLower(cmd)
	fullLower := strings.ToLower(fullParam)
	minLower := strings.ToLower(minPrefix)

	// Check exact match.
	if strings.Contains(lower, fullLower) {
		return true
	}

	// Check abbreviations from min prefix length to full param.
	for i := len(minLower); i <= len(fullLower); i++ {
		prefix := fullLower[:i]
		if strings.Contains(lower, prefix) {
			// Verify it's followed by non-alpha or end of string.
			idx := strings.Index(lower, prefix)
			end := idx + len(prefix)
			if end >= len(lower) || !isWordByte(lower[end]) {
				return true
			}
		}
	}
	return false
}

// extractCommandTokens splits a command into its first-position tokens
// (the actual command names in each pipeline/statement segment).
func extractCommandTokens(cmd string) []string {
	var tokens []string
	segments := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n'
	})
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		// Strip call operator.
		seg = strings.TrimLeft(seg, "&. ")
		fields := strings.Fields(seg)
		if len(fields) > 0 {
			tokens = append(tokens, fields[0])
		}
	}
	return tokens
}
