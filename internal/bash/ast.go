package bash

import "fmt"

// NodeType identifies the type of a bash AST node.
type NodeType int

const (
	NodeCommand             NodeType = iota // Simple command
	NodePipeline                            // cmd1 | cmd2
	NodeAndOr                               // cmd1 && cmd2, cmd1 || cmd2
	NodeList                                // cmd1 ; cmd2
	NodeSubshell                            // (cmd)
	NodeCommandSubstitution                 // $(cmd) or `cmd`
	NodeProcessSubstitution                 // <(cmd) or >(cmd)
	NodeRedirect                            // > file, >> file, etc.
	NodeWord                                // Literal word
	NodeAssignment                          // VAR=value
	NodeFunctionDef                         // name() { body; }
	NodeIf                                  // if/then/elif/else/fi
	NodeFor                                 // for VAR in WORDS; do BODY; done
	NodeWhile                               // while COND; do BODY; done
	NodeUntil                               // until COND; do BODY; done
	NodeCase                                // case WORD in pattern) body ;; esac
	NodeCaseItem                            // pattern) body ;;
	NodeGroup                               // { cmd; }
	NodeNegation                            // ! cmd
	NodeArithmetic                          // $(( expr ))
	NodeTestCommand                         // [[ expr ]] or [ expr ]
	NodeComment                             // # comment
	NodeDeclaration                         // export, declare, etc.
	NodeHeredoc                             // << or <<- body
	NodeHerestring                          // <<< word
	NodeProgram                             // Root node
	NodeUnset                               // unset VAR
)

// String returns a human-readable name for the node type.
func (n NodeType) String() string {
	switch n {
	case NodeCommand:
		return "Command"
	case NodePipeline:
		return "Pipeline"
	case NodeAndOr:
		return "AndOr"
	case NodeList:
		return "List"
	case NodeSubshell:
		return "Subshell"
	case NodeCommandSubstitution:
		return "CommandSubstitution"
	case NodeProcessSubstitution:
		return "ProcessSubstitution"
	case NodeRedirect:
		return "Redirect"
	case NodeWord:
		return "Word"
	case NodeAssignment:
		return "Assignment"
	case NodeFunctionDef:
		return "FunctionDef"
	case NodeIf:
		return "If"
	case NodeFor:
		return "For"
	case NodeWhile:
		return "While"
	case NodeUntil:
		return "Until"
	case NodeCase:
		return "Case"
	case NodeCaseItem:
		return "CaseItem"
	case NodeGroup:
		return "Group"
	case NodeNegation:
		return "Negation"
	case NodeArithmetic:
		return "Arithmetic"
	case NodeTestCommand:
		return "TestCommand"
	case NodeComment:
		return "Comment"
	case NodeDeclaration:
		return "Declaration"
	case NodeHeredoc:
		return "Heredoc"
	case NodeHerestring:
		return "Herestring"
	case NodeProgram:
		return "Program"
	case NodeUnset:
		return "Unset"
	default:
		return fmt.Sprintf("Unknown(%d)", int(n))
	}
}

// RedirectOp is a redirection operator type.
type RedirectOp int

const (
	RedirectInput      RedirectOp = iota // <
	RedirectOutput                       // >
	RedirectAppend                       // >>
	RedirectHeredoc                      // <<
	RedirectHeredocTab                   // <<-
	RedirectHerestring                   // <<<
	RedirectDupInput                     // <&
	RedirectDupOutput                    // >&
	RedirectClobber                      // >|
	RedirectBothOut                      // &>
	RedirectBothAppend                   // &>>
)

// String returns the shell syntax for the redirect operator.
func (r RedirectOp) String() string {
	switch r {
	case RedirectInput:
		return "<"
	case RedirectOutput:
		return ">"
	case RedirectAppend:
		return ">>"
	case RedirectHeredoc:
		return "<<"
	case RedirectHeredocTab:
		return "<<-"
	case RedirectHerestring:
		return "<<<"
	case RedirectDupInput:
		return "<&"
	case RedirectDupOutput:
		return ">&"
	case RedirectClobber:
		return ">|"
	case RedirectBothOut:
		return "&>"
	case RedirectBothAppend:
		return "&>>"
	default:
		return "?"
	}
}

// WordType classifies the type of a word token.
type WordType int

const (
	WordLiteral      WordType = iota // Plain unquoted word
	WordSingleQuoted                 // 'text'
	WordDoubleQuoted                 // "text"
	WordVariable                     // $VAR or ${VAR}
	WordGlob                         // *, ?, [...]
	WordTilde                        // ~, ~user
	WordAnsiC                        // $'text'
	WordNumber                       // 42, 0xff
	WordBraceExpand                  // {a,b,c} or {1..5}
)

// Operator represents a list/pipeline separator.
type Operator int

const (
	OpAnd        Operator = iota // &&
	OpOr                         // ||
	OpPipe                       // |
	OpPipeErr                    // |&
	OpSemicolon                  // ;
	OpBackground                 // &
	OpNewline                    // \n
)

// String returns the shell syntax for the operator.
func (o Operator) String() string {
	switch o {
	case OpAnd:
		return "&&"
	case OpOr:
		return "||"
	case OpPipe:
		return "|"
	case OpPipeErr:
		return "|&"
	case OpSemicolon:
		return ";"
	case OpBackground:
		return "&"
	case OpNewline:
		return "\\n"
	default:
		return "?"
	}
}

// Node is a node in the bash AST.
type Node struct {
	Type     NodeType
	Text     string // Raw source text of this node
	Start    int    // Byte offset in source (inclusive)
	End      int    // Byte offset in source (exclusive)
	Children []*Node

	// Fields used by specific node types:

	// Command: argv[0]=Name, argv[1:]=Args
	Name string   // Command name (for NodeCommand, NodeFunctionDef)
	Args []string // Arguments (for NodeCommand, NodeDeclaration)

	// Redirect
	RedirOp RedirectOp // Redirect operator
	Target  string     // Redirect target (filename or fd)
	FD      int        // File descriptor (-1 if not specified)

	// Word
	WordKind WordType // Word classification

	// Assignment
	VarName  string // Variable name (for NodeAssignment)
	VarValue string // Variable value (for NodeAssignment)
	IsAppend bool   // += vs =

	// Control structures
	Condition []*Node // Condition commands (for NodeIf, NodeWhile, NodeUntil)
	Body      []*Node // Body commands
	ElseIf    []*Node // elif clauses (for NodeIf)
	Else      []*Node // else clause (for NodeIf)

	// Case
	CaseWord    string       // Word being matched
	CaseItems   []*CaseItem  // Case items
	Operator    Operator     // Separator operator (for pipeline, list)
	Separators  []Operator   // All separators in a list/pipeline
	EnvVars     []Assignment // Leading VAR=val assignments on a command
	Redirects   []*Node      // Redirections attached to a command
	PipelinePos int          // Position in pipeline (0-based)

	// Heredoc
	HeredocDelim  string // Heredoc delimiter
	HeredocBody   string // Heredoc body content
	HeredocQuoted bool   // Whether delimiter is quoted (no expansion)
	HeredocStrip  bool   // Whether to strip tabs (<<-)
}

// CaseItem represents a single case item: pattern) body ;;
type CaseItem struct {
	Patterns []string
	Body     []*Node
	Term     string // ";;" or ";&" or ";;&"
}

// Assignment represents a VAR=value assignment.
type Assignment struct {
	Name     string
	Value    string
	IsAppend bool
}

// SimpleCommand is a fully-resolved command with its argv, env, and redirects.
// This is the output of security-focused AST analysis.
type SimpleCommand struct {
	Argv      []string     // argv[0] = command name, rest = arguments
	EnvVars   []Assignment // Leading VAR=val assignments
	Redirects []Redirect   // Output/input redirects
	Text      string       // Original source span
}

// Redirect is a parsed redirect with operator, target, and optional fd.
type Redirect struct {
	Op     string // ">", ">>", "<", "<<", "<<<", ">&", "<&", ">|", "&>", "&>>"
	Target string
	FD     int // -1 if not specified
}

// ParseForSecurityResult is the result of security-focused parsing.
type ParseForSecurityResult struct {
	Kind     string          // "simple", "too-complex", "parse-unavailable"
	Commands []SimpleCommand // Only set when Kind == "simple"
	Reason   string          // Only set when Kind == "too-complex"
	NodeType string          // Optional: the node type that caused too-complex
}

// Visitor is a function called for each node during tree walking.
// Return false to skip visiting children of the current node.
type Visitor func(node *Node, depth int) bool

// Walk traverses the AST in depth-first pre-order, calling the visitor
// for each node. If the visitor returns false, children are skipped.
func Walk(node *Node, visitor Visitor) {
	walkDepth(node, visitor, 0)
}

func walkDepth(node *Node, visitor Visitor, depth int) {
	if node == nil {
		return
	}
	if !visitor(node, depth) {
		return
	}
	for _, child := range node.Children {
		walkDepth(child, visitor, depth+1)
	}
	for _, child := range node.Condition {
		walkDepth(child, visitor, depth+1)
	}
	for _, child := range node.Body {
		walkDepth(child, visitor, depth+1)
	}
	for _, child := range node.ElseIf {
		walkDepth(child, visitor, depth+1)
	}
	for _, child := range node.Else {
		walkDepth(child, visitor, depth+1)
	}
	for _, child := range node.Redirects {
		walkDepth(child, visitor, depth+1)
	}
}

// WalkCommands calls the visitor for every NodeCommand in the tree.
func WalkCommands(node *Node, visitor func(cmd *Node)) {
	Walk(node, func(n *Node, _ int) bool {
		if n.Type == NodeCommand {
			visitor(n)
		}
		return true
	})
}

// CollectCommands returns all command nodes found anywhere in the tree.
func CollectCommands(node *Node) []*Node {
	var cmds []*Node
	WalkCommands(node, func(cmd *Node) {
		cmds = append(cmds, cmd)
	})
	return cmds
}

// HasNodeType returns true if the tree contains a node of the given type.
func HasNodeType(node *Node, typ NodeType) bool {
	found := false
	Walk(node, func(n *Node, _ int) bool {
		if n.Type == typ {
			found = true
			return false
		}
		return !found
	})
	return found
}

// IsCompound returns true if the node represents a compound command structure.
func IsCompound(node *Node) bool {
	switch node.Type {
	case NodePipeline, NodeAndOr, NodeList, NodeSubshell, NodeGroup,
		NodeIf, NodeFor, NodeWhile, NodeUntil, NodeCase, NodeFunctionDef:
		return true
	default:
		return false
	}
}

// IsControlFlow returns true if the node is a control-flow structure.
func IsControlFlow(node *Node) bool {
	switch node.Type {
	case NodeIf, NodeFor, NodeWhile, NodeUntil, NodeCase:
		return true
	default:
		return false
	}
}
