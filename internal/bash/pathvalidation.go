// Package bash provides path validation for shell commands.
// This file implements working directory validation, restricted path detection,
// project root enforcement, and path extraction from commands.
// Migrated from Claude Code's pathValidation.ts.
package bash

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RestrictedPaths are system directories that should not be modified.
var RestrictedPaths = []string{
	"/etc",
	"/sys",
	"/proc",
	"/boot",
	"/dev",
	"/sbin",
	"/usr/sbin",
	"/usr/bin",
	"/usr/lib",
	"/usr/local/sbin",
	"/var/log",
	"/var/run",
	"/lib",
	"/lib64",
}

// DangerousRemovalPaths are paths that should NEVER be removed.
var DangerousRemovalPaths = []string{
	"/",
	"/bin",
	"/boot",
	"/dev",
	"/etc",
	"/home",
	"/lib",
	"/lib64",
	"/media",
	"/mnt",
	"/opt",
	"/proc",
	"/root",
	"/run",
	"/sbin",
	"/srv",
	"/sys",
	"/tmp",
	"/usr",
	"/var",
}

// ConfigProtectedFiles are files that should never be modified without explicit approval.
var ConfigProtectedFiles = []string{
	".bashrc",
	".bash_profile",
	".profile",
	".zshrc",
	".zsh_history",
	".ssh/authorized_keys",
	".ssh/config",
	".gitconfig",
	".npmrc",
	".env",
	".env.local",
	".env.production",
}

// PathValidationResult contains the result of validating paths in a command.
type PathValidationResult struct {
	Valid       bool
	Message     string
	Paths       []string // extracted paths
	Suggestions []string // suggested permission rules
}

// PathValidator validates paths referenced by shell commands.
type PathValidator struct {
	// ProjectRoot is the root directory of the project.
	ProjectRoot string

	// AllowedDirs are additional directories the tool may operate in.
	AllowedDirs []string

	// HomeDir is the user's home directory.
	HomeDir string
}

// NewPathValidator creates a validator with the given project root.
func NewPathValidator(projectRoot string) *PathValidator {
	home, _ := os.UserHomeDir()
	return &PathValidator{
		ProjectRoot: projectRoot,
		HomeDir:     home,
	}
}

// ValidateWorkingDir checks if a working directory is valid and allowed.
func (pv *PathValidator) ValidateWorkingDir(dir string) PathValidationResult {
	// Must exist
	info, err := os.Stat(dir)
	if err != nil {
		return PathValidationResult{
			Valid:   false,
			Message: "working directory does not exist: " + dir,
		}
	}
	if !info.IsDir() {
		return PathValidationResult{
			Valid:   false,
			Message: "not a directory: " + dir,
		}
	}

	// Check if within allowed directories
	absDir, _ := filepath.Abs(dir)
	if pv.isPathAllowed(absDir) {
		return PathValidationResult{Valid: true}
	}

	// Check if it's a restricted system path
	if pv.isRestrictedPath(absDir) {
		return PathValidationResult{
			Valid:   false,
			Message: "restricted system directory: " + dir,
		}
	}

	return PathValidationResult{Valid: true}
}

// ValidateCommandPaths extracts and validates all paths from a command.
func (pv *PathValidator) ValidateCommandPaths(command string, cwd string) PathValidationResult {
	// Extract paths from the command
	paths := ExtractPathsFromCommand(command)
	if len(paths) == 0 {
		return PathValidationResult{Valid: true}
	}

	var invalidPaths []string
	var suggestions []string

	for _, p := range paths {
		absPath := pv.resolvePath(p, cwd)

		// Check restricted paths
		if pv.isRestrictedPath(absPath) {
			invalidPaths = append(invalidPaths, p)
			continue
		}

		// Check config-protected files
		if pv.isConfigProtectedFile(absPath) {
			invalidPaths = append(invalidPaths, p)
			continue
		}

		// Check if outside allowed directories
		if !pv.isPathAllowed(absPath) {
			invalidPaths = append(invalidPaths, p)
			dir := filepath.Dir(absPath)
			suggestions = append(suggestions, "Read("+dir+"/**)")
		}
	}

	if len(invalidPaths) > 0 {
		return PathValidationResult{
			Valid:       false,
			Message:     "command references paths outside allowed directories: " + strings.Join(invalidPaths, ", "),
			Paths:       invalidPaths,
			Suggestions: uniqueStrings(suggestions),
		}
	}

	return PathValidationResult{Valid: true, Paths: paths}
}

// ValidateDangerousRemoval checks if an rm/rmdir targets dangerous paths.
func (pv *PathValidator) ValidateDangerousRemoval(command string, cwd string) PathValidationResult {
	base := GetBaseCommand(command)
	if base != "rm" && base != "rmdir" {
		return PathValidationResult{Valid: true}
	}

	paths := ExtractPathsFromCommand(command)
	for _, p := range paths {
		absPath := pv.resolvePath(p, cwd)
		if IsDangerousRemovalPath(absPath) {
			return PathValidationResult{
				Valid:   false,
				Message: "dangerous removal: " + absPath + " is a critical system directory",
				Paths:   []string{absPath},
			}
		}
	}

	return PathValidationResult{Valid: true}
}

// ValidateOutputRedirections checks if output redirections target safe paths.
func (pv *PathValidator) ValidateOutputRedirections(command string, cwd string) PathValidationResult {
	targets := ExtractOutputRedirections(command)
	if len(targets) == 0 {
		return PathValidationResult{Valid: true}
	}

	for _, target := range targets {
		absPath := pv.resolvePath(target, cwd)

		if pv.isRestrictedPath(absPath) {
			return PathValidationResult{
				Valid:   false,
				Message: "output redirection targets restricted path: " + target,
				Paths:   []string{target},
			}
		}

		if pv.isConfigProtectedFile(absPath) {
			return PathValidationResult{
				Valid:   false,
				Message: "output redirection targets protected config file: " + target,
				Paths:   []string{target},
			}
		}
	}

	return PathValidationResult{Valid: true}
}

// resolvePath converts a path to absolute, expanding ~ and resolving relative paths.
func (pv *PathValidator) resolvePath(p string, cwd string) string {
	p = ExpandTilde(p, pv.HomeDir)
	p = strings.Trim(p, "'\"")

	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if cwd != "" {
		return filepath.Clean(filepath.Join(cwd, p))
	}
	return filepath.Clean(filepath.Join(pv.ProjectRoot, p))
}

// isPathAllowed checks if a path is within the project root or allowed directories.
func (pv *PathValidator) isPathAllowed(absPath string) bool {
	// Project root always allowed
	if isSubpath(absPath, pv.ProjectRoot) {
		return true
	}

	// Check additional allowed directories
	for _, dir := range pv.AllowedDirs {
		absDir, _ := filepath.Abs(dir)
		if isSubpath(absPath, absDir) {
			return true
		}
	}

	return false
}

// isRestrictedPath checks if a path is a system restricted directory.
func (pv *PathValidator) isRestrictedPath(absPath string) bool {
	for _, restricted := range RestrictedPaths {
		if isSubpath(absPath, restricted) {
			return true
		}
	}
	return false
}

// isConfigProtectedFile checks if a path matches a protected config file pattern.
func (pv *PathValidator) isConfigProtectedFile(absPath string) bool {
	for _, protected := range ConfigProtectedFiles {
		if protected[0] == '.' {
			// Relative to home dir
			protectedAbs := filepath.Join(pv.HomeDir, protected)
			if absPath == protectedAbs {
				return true
			}
		}
		// Also check by basename
		if filepath.Base(absPath) == filepath.Base(protected) {
			// Additional check: must be in a "sensitive" location
			dir := filepath.Dir(absPath)
			if dir == pv.HomeDir || strings.HasPrefix(dir, filepath.Join(pv.HomeDir, ".ssh")) {
				return true
			}
		}
	}
	return false
}

// isSubpath checks if child is under parent (or equal to parent).
func isSubpath(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)

	if child == parent {
		return true
	}

	parentSlash := parent
	if !strings.HasSuffix(parentSlash, string(filepath.Separator)) {
		parentSlash += string(filepath.Separator)
	}
	return strings.HasPrefix(child, parentSlash)
}

// ExpandTilde expands a leading ~ to the home directory.
func ExpandTilde(path string, homeDir string) string {
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	if path == "~" {
		return homeDir
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// IsDangerousRemovalPath returns true if removing this path would be catastrophic.
func IsDangerousRemovalPath(absPath string) bool {
	absPath = filepath.Clean(absPath)
	for _, dangerous := range DangerousRemovalPaths {
		if absPath == dangerous {
			return true
		}
	}
	return false
}

// ExtractPathsFromCommand extracts file/directory paths from a shell command.
// This is a heuristic extraction that handles common patterns.
func ExtractPathsFromCommand(command string) []string {
	stripped := StripSafeWrappers(strings.TrimSpace(command))
	fields := tokenizeCommand(stripped)
	if len(fields) == 0 {
		return nil
	}

	base := fields[0]
	args := fields[1:]

	extractor, ok := pathExtractors[base]
	if ok {
		return extractor(args)
	}

	// Default: extract non-flag arguments that look like paths
	return filterPathLikeArgs(args)
}

// ExtractOutputRedirections extracts output redirection targets from a command.
func ExtractOutputRedirections(command string) []string {
	var targets []string
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
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

		if !inSingle && !inDouble && ch == '>' {
			// Check for >> or >
			next := i + 1
			if next < len(command) && command[next] == '>' {
				next++ // >>
			}
			// Skip whitespace
			for next < len(command) && (command[next] == ' ' || command[next] == '\t') {
				next++
			}
			// Extract the target path
			var target strings.Builder
			for next < len(command) {
				c := command[next]
				if c == ' ' || c == '\t' || c == ';' || c == '|' || c == '&' || c == '\n' {
					break
				}
				target.WriteByte(c)
				next++
			}
			t := target.String()
			// Skip /dev/null redirections
			if t != "" && t != "/dev/null" {
				targets = append(targets, t)
			}
		}
	}
	return targets
}

// tokenizeCommand splits a command into tokens, respecting quotes.
func tokenizeCommand(command string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]
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

		if !inSingle && !inDouble && (ch == ' ' || ch == '\t') {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// filterOutFlags removes arguments starting with - (except after --).
func filterOutFlags(args []string) []string {
	var result []string
	afterDoubleDash := false
	for _, arg := range args {
		if afterDoubleDash {
			result = append(result, arg)
		} else if arg == "--" {
			afterDoubleDash = true
		} else if !strings.HasPrefix(arg, "-") {
			result = append(result, arg)
		}
	}
	return result
}

// filterPathLikeArgs extracts arguments that look like file paths.
func filterPathLikeArgs(args []string) []string {
	var paths []string
	pathRe := regexp.MustCompile(`^[~./]|/`)
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if pathRe.MatchString(arg) || strings.Contains(arg, "/") {
			paths = append(paths, arg)
		}
	}
	return paths
}

// Path extractor functions for specific commands.
type pathExtractorFunc func(args []string) []string

var pathExtractors = map[string]pathExtractorFunc{
	"cd":    extractCdPaths,
	"ls":    extractLsPaths,
	"cat":   extractSimplePaths,
	"head":  extractSimplePaths,
	"tail":  extractSimplePaths,
	"less":  extractSimplePaths,
	"more":  extractSimplePaths,
	"wc":    extractSimplePaths,
	"file":  extractSimplePaths,
	"stat":  extractSimplePaths,
	"touch": extractSimplePaths,
	"mkdir": extractSimplePaths,
	"rm":    extractSimplePaths,
	"rmdir": extractSimplePaths,
	"chmod": extractChmodPaths,
	"chown": extractChmodPaths,
	"chgrp": extractChmodPaths,
	"cp":    extractCpPaths,
	"mv":    extractCpPaths,
	"ln":    extractCpPaths,
	"find":  extractFindPaths,
	"grep":  extractGrepPaths,
	"rg":    extractGrepPaths,
	"sed":   extractSedPaths,
	"diff":  extractSimplePaths,
	"sort":  extractSimplePaths,
	"uniq":  extractSimplePaths,
	"cut":   extractSimplePaths,
	"awk":   extractAwkPaths,
	"git":   extractGitPaths,
	"jq":    extractJqPaths,
}

func extractCdPaths(args []string) []string {
	nonFlags := filterOutFlags(args)
	if len(nonFlags) == 0 {
		home, _ := os.UserHomeDir()
		return []string{home}
	}
	return []string{strings.Join(nonFlags, " ")}
}

func extractLsPaths(args []string) []string {
	paths := filterOutFlags(args)
	if len(paths) == 0 {
		return []string{"."}
	}
	return paths
}

func extractSimplePaths(args []string) []string {
	return filterOutFlags(args)
}

func extractChmodPaths(args []string) []string {
	nonFlags := filterOutFlags(args)
	// First non-flag is the mode/owner, rest are paths
	if len(nonFlags) <= 1 {
		return nil
	}
	return nonFlags[1:]
}

func extractCpPaths(args []string) []string {
	return filterOutFlags(args)
}

func extractFindPaths(args []string) []string {
	var paths []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break // find puts paths before options
		}
		paths = append(paths, arg)
	}
	if len(paths) == 0 {
		return []string{"."}
	}
	return paths
}

func extractGrepPaths(args []string) []string {
	// grep flags that take an argument
	flagsWithArgs := map[string]bool{
		"-e": true, "--regexp": true, "-f": true, "--file": true,
		"-m": true, "--max-count": true, "-A": true, "-B": true, "-C": true,
		"--include": true, "--exclude": true, "--exclude-dir": true,
		"-d": true, "--directories": true,
	}

	var paths []string
	patternFound := false
	afterDoubleDash := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !afterDoubleDash && arg == "--" {
			afterDoubleDash = true
			continue
		}

		if !afterDoubleDash && strings.HasPrefix(arg, "-") {
			flag := strings.SplitN(arg, "=", 2)[0]
			if flag == "-e" || flag == "--regexp" || flag == "-f" || flag == "--file" {
				patternFound = true
			}
			if flagsWithArgs[flag] && !strings.Contains(arg, "=") {
				i++ // skip value
			}
			continue
		}

		if !patternFound {
			patternFound = true
			continue // skip pattern
		}
		paths = append(paths, arg)
	}
	return paths
}

func extractSedPaths(args []string) []string {
	// For sed, paths come after the expression(s)
	var paths []string
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "-e" || arg == "-f" || arg == "--expression" || arg == "--file" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// First non-flag non-expression is the script, rest are files
		if i == 0 {
			continue // skip script
		}
		paths = append(paths, arg)
	}
	return paths
}

func extractAwkPaths(args []string) []string {
	nonFlags := filterOutFlags(args)
	// First non-flag is the program, rest are files
	if len(nonFlags) <= 1 {
		return nil
	}
	return nonFlags[1:]
}

func extractGitPaths(args []string) []string {
	// Git paths depend on the subcommand; return empty for safety
	// Git path validation is handled separately
	return nil
}

func extractJqPaths(args []string) []string {
	nonFlags := filterOutFlags(args)
	// First non-flag is the filter, rest are files
	if len(nonFlags) <= 1 {
		return nil
	}
	return nonFlags[1:]
}

// uniqueStrings deduplicates a string slice preserving order.
func uniqueStrings(s []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
