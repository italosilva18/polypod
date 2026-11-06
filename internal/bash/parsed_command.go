package bash

import (
	"strings"
)

// ParsedCommand represents a parsed shell command with methods to query
// its structure. Migrated from Claude Code's ParsedCommand.ts.
type ParsedCommand struct {
	OriginalCommand string
	root            *Node
	pipePositions   []int
	redirections    []OutputRedirection
}

// OutputRedirection represents an output redirect (> or >>).
type OutputRedirection struct {
	Target   string
	Operator string // ">" or ">>"
}

// ParseCommand parses a shell command string and returns a ParsedCommand.
// Returns nil if the command is empty or parsing fails.
func ParseCommand(command string) *ParsedCommand {
	if command == "" {
		return nil
	}
	root := Parse(command)
	if root == nil {
		// Fallback: return a basic ParsedCommand from the raw string.
		return &ParsedCommand{
			OriginalCommand: command,
		}
	}
	pc := &ParsedCommand{
		OriginalCommand: command,
		root:            root,
	}
	pc.pipePositions = pc.extractPipePositions()
	pc.redirections = pc.extractRedirections()
	return pc
}

// String returns the original command string.
func (pc *ParsedCommand) String() string {
	return pc.OriginalCommand
}

// GetPipeSegments splits the command into segments by pipe operators.
func (pc *ParsedCommand) GetPipeSegments() []string {
	if len(pc.pipePositions) == 0 {
		return []string{pc.OriginalCommand}
	}

	segments := make([]string, 0, len(pc.pipePositions)+1)
	currentStart := 0
	for _, pipePos := range pc.pipePositions {
		if pipePos > currentStart && pipePos <= len(pc.OriginalCommand) {
			seg := strings.TrimSpace(pc.OriginalCommand[currentStart:pipePos])
			if seg != "" {
				segments = append(segments, seg)
			}
		}
		currentStart = pipePos + 1
	}
	if currentStart < len(pc.OriginalCommand) {
		seg := strings.TrimSpace(pc.OriginalCommand[currentStart:])
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	return segments
}

// WithoutOutputRedirections returns the command with output redirections removed.
func (pc *ParsedCommand) WithoutOutputRedirections() string {
	if len(pc.redirections) == 0 {
		return pc.OriginalCommand
	}
	// Fallback: simple regex-based removal for common patterns
	result := pc.OriginalCommand
	for _, r := range pc.redirections {
		pattern := r.Operator + " " + r.Target
		result = strings.Replace(result, pattern, "", 1)
		// Try without space
		pattern2 := r.Operator + r.Target
		result = strings.Replace(result, pattern2, "", 1)
	}
	return strings.TrimSpace(result)
}

// GetOutputRedirections returns all output redirections found in the command.
func (pc *ParsedCommand) GetOutputRedirections() []OutputRedirection {
	return pc.redirections
}

// SplitCompoundCommand splits a compound command (with &&, ||, ;) into
// individual simple commands.
func SplitCompoundCommand(command string) []string {
	root := Parse(command)
	if root == nil {
		return []string{command}
	}
	var commands []string
	collectCommandTexts(root, &commands)
	if len(commands) == 0 {
		return []string{command}
	}
	return commands
}

func collectCommandTexts(node *Node, out *[]string) {
	if node == nil {
		return
	}
	switch node.Type {
	case NodeCommand, NodeDeclaration, NodeUnset:
		if node.Text != "" {
			*out = append(*out, strings.TrimSpace(node.Text))
		}
	case NodeAssignment:
		// Standalone assignments are not commands
	default:
		for _, child := range node.Children {
			collectCommandTexts(child, out)
		}
		for _, child := range node.Body {
			collectCommandTexts(child, out)
		}
		for _, child := range node.Condition {
			collectCommandTexts(child, out)
		}
	}
}

// ExtractBaseCommand strips environment variable prefixes and known wrappers
// (sudo, env, time, timeout, nice, nohup, stdbuf) from a command,
// returning the underlying command name. This is a detailed version
// with more wrapper-stripping logic than GetBaseCommand in bashtool.go.
func ExtractBaseCommand(command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}

	parts := strings.Fields(cmd)
	i := 0

	// Skip leading VAR=val assignments
	for i < len(parts) && strings.Contains(parts[i], "=") && !strings.HasPrefix(parts[i], "-") {
		i++
	}

	// Strip wrappers
	for i < len(parts) {
		switch parts[i] {
		case "sudo":
			i++
			// Skip sudo flags
			for i < len(parts) && strings.HasPrefix(parts[i], "-") {
				if parts[i] == "-u" || parts[i] == "-g" || parts[i] == "-C" {
					i += 2 // flag + argument
				} else {
					i++
				}
			}
		case "env":
			i++
			for i < len(parts) {
				p := parts[i]
				if strings.Contains(p, "=") && !strings.HasPrefix(p, "-") {
					i++ // VAR=val
				} else if p == "-i" || p == "-0" || p == "-v" {
					i++ // flags
				} else if p == "-u" && i+1 < len(parts) {
					i += 2
				} else if strings.HasPrefix(p, "-") {
					break // unknown flag
				} else {
					break // the actual command
				}
			}
		case "time", "nohup":
			i++
		case "nice":
			i++
			if i < len(parts) && parts[i] == "-n" && i+1 < len(parts) {
				i += 2
			} else if i < len(parts) && len(parts[i]) > 1 && parts[i][0] == '-' && isDigit(parts[i][1]) {
				i++
			}
		case "timeout":
			i++
			// Skip flags
			for i < len(parts) && strings.HasPrefix(parts[i], "-") {
				if parts[i] == "-k" || parts[i] == "-s" || parts[i] == "--kill-after" || parts[i] == "--signal" {
					i += 2
				} else {
					i++
				}
			}
			// Skip duration
			if i < len(parts) {
				i++
			}
		case "stdbuf":
			i++
			for i < len(parts) && strings.HasPrefix(parts[i], "-") {
				p := parts[i]
				if (p == "-i" || p == "-o" || p == "-e") && i+1 < len(parts) {
					i += 2
				} else if strings.HasPrefix(p, "--") {
					i++
				} else {
					i++
				}
			}
		default:
			goto done
		}
	}
done:
	if i >= len(parts) {
		return ""
	}
	return parts[i]
}

// IsCompoundCommand returns true if the command contains multiple
// sub-commands separated by operators.
func IsCompoundCommand(command string) bool {
	root := Parse(command)
	if root == nil {
		return false
	}
	return hasCompoundStructure(root)
}

func hasCompoundStructure(node *Node) bool {
	switch node.Type {
	case NodeAndOr, NodePipeline:
		return true
	case NodeList:
		return len(node.Children) > 1
	}
	for _, child := range node.Children {
		if hasCompoundStructure(child) {
			return true
		}
	}
	return false
}

// GetAllCommands returns all command names from a (possibly compound) command.
func GetAllCommands(command string) []string {
	root := Parse(command)
	if root == nil {
		base := GetBaseCommand(command)
		if base != "" {
			return []string{base}
		}
		return nil
	}
	var names []string
	WalkCommands(root, func(cmd *Node) {
		if cmd.Name != "" {
			names = append(names, cmd.Name)
		}
	})
	return names
}

// extractPipePositions finds byte offsets of | operators in pipelines.
func (pc *ParsedCommand) extractPipePositions() []int {
	if pc.root == nil {
		return nil
	}
	var positions []int
	Walk(pc.root, func(n *Node, _ int) bool {
		if n.Type == NodePipeline {
			// Find pipe operators between children
			for i := 0; i < len(n.Children)-1; i++ {
				child := n.Children[i]
				next := n.Children[i+1]
				// Pipe position is between the end of child and start of next
				pipePos := child.End
				for pipePos < next.Start && pipePos < len(pc.OriginalCommand) {
					if pc.OriginalCommand[pipePos] == '|' {
						positions = append(positions, pipePos)
						break
					}
					pipePos++
				}
			}
		}
		return true
	})
	return positions
}

// extractRedirections finds output redirections in the AST.
func (pc *ParsedCommand) extractRedirections() []OutputRedirection {
	if pc.root == nil {
		return nil
	}
	var redirections []OutputRedirection
	Walk(pc.root, func(n *Node, _ int) bool {
		if n.Type == NodeRedirect {
			switch n.RedirOp {
			case RedirectOutput:
				redirections = append(redirections, OutputRedirection{
					Target:   n.Target,
					Operator: ">",
				})
			case RedirectAppend:
				redirections = append(redirections, OutputRedirection{
					Target:   n.Target,
					Operator: ">>",
				})
			}
		}
		return true
	})
	return redirections
}
