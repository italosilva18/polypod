package skill

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// MCPSkillSource is the dependency MCPSkillLoader needs to talk to a set of
// MCP servers. It is deliberately decoupled from any concrete MCP client so
// the skill package can stay free of MCP-protocol imports and be unit-tested
// with a lightweight fake.
//
// Implementations are expected to be safe to call from multiple goroutines.
// In production this is typically satisfied by a thin adapter over the
// official MCP SDK used in internal/mcp.
type MCPSkillSource interface {
	// ListPrompts returns every prompt exposed by the given MCP server via
	// the prompts/list capability. An empty slice (not an error) should be
	// returned when the server advertises no prompts.
	ListPrompts(ctx context.Context, server string) ([]MCPPrompt, error)

	// GetPrompt expands the named prompt on the given server, returning the
	// full text the agent should be given. args are the prompt-template
	// arguments declared by the prompt's Arguments list.
	GetPrompt(ctx context.Context, server, name string, args map[string]any) (string, error)
}

// MCPPrompt describes a single prompt exposed by an MCP server, mirroring
// the shape of the MCP protocol's Prompt object. Only the fields the skill
// loader actually needs are kept.
type MCPPrompt struct {
	Name        string
	Description string
	Arguments   []MCPPromptArgument
}

// MCPPromptArgument describes one templated argument of an MCPPrompt.
type MCPPromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// MCPSkillLoader turns MCP prompts into ParsedSkill entries so the rest of
// the skill pipeline (registry, agent routing, tool listing) can treat them
// uniformly with on-disk and bundled skills.
//
// Each prompt becomes a skill named "mcp__<server>__<prompt>" with an empty
// Body: the actual prompt text is fetched lazily via InvokeSkill, because
// MCP prompt expansion is dynamic and may depend on arguments.
type MCPSkillLoader struct {
	source  MCPSkillSource
	servers []string
}

// NewMCPSkillLoader builds a loader that will query the given servers in
// order. Callers can pass zero servers for a no-op loader.
func NewMCPSkillLoader(source MCPSkillSource, servers ...string) *MCPSkillLoader {
	return &MCPSkillLoader{source: source, servers: servers}
}

// LoadAll queries every configured server for its prompts and converts the
// results into ParsedSkill entries. A failure on one server is recorded but
// does not stop loading from the remaining servers; the joined error (if
// any) is returned alongside all successfully-loaded skills.
func (l *MCPSkillLoader) LoadAll(ctx context.Context) ([]*ParsedSkill, error) {
	if l == nil || l.source == nil {
		return nil, nil
	}

	var (
		skills []*ParsedSkill
		errs   []error
	)

	for _, server := range l.servers {
		prompts, err := l.source.ListPrompts(ctx, server)
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp skill loader: list prompts from %q: %w", server, err))
			continue
		}
		for _, p := range prompts {
			skills = append(skills, promptToSkill(server, p))
		}
	}

	return skills, errors.Join(errs...)
}

// InvokeSkill expands an MCP-backed skill into its prompt text. It inspects
// the skill's name to figure out which server/prompt to target and then
// delegates to the source's GetPrompt.
func (l *MCPSkillLoader) InvokeSkill(ctx context.Context, skill *ParsedSkill, args map[string]any) (string, error) {
	if skill == nil {
		return "", fmt.Errorf("mcp skill loader: nil skill")
	}
	if l == nil || l.source == nil {
		return "", fmt.Errorf("mcp skill loader: no source configured")
	}
	server, prompt, ok := ParseMCPSkillName(skill.Frontmatter.Name)
	if !ok {
		return "", fmt.Errorf("mcp skill loader: %q is not an MCP skill name", skill.Frontmatter.Name)
	}
	return l.source.GetPrompt(ctx, server, prompt, args)
}

// ParseMCPSkillName reverses promptToSkill's naming scheme. It returns the
// (server, prompt) pair encoded in an "mcp__server__prompt" identifier and
// reports whether the name had the expected shape. Prompt identifiers may
// themselves contain double underscores, so the split is strictly
// three-segment: [prefix, server, prompt-rest].
func ParseMCPSkillName(name string) (server, prompt string, ok bool) {
	const prefix = "mcp__"
	if !strings.HasPrefix(name, prefix) {
		return "", "", false
	}
	rest := name[len(prefix):]
	// Find the first "__" that separates server from prompt.
	sep := strings.Index(rest, "__")
	if sep <= 0 || sep+2 >= len(rest) {
		return "", "", false
	}
	server = rest[:sep]
	prompt = rest[sep+2:]
	if server == "" || prompt == "" {
		return "", "", false
	}
	return server, prompt, true
}

// promptToSkill constructs a ParsedSkill for the given prompt. Body stays
// empty because the prompt is expanded on demand by InvokeSkill.
func promptToSkill(server string, p MCPPrompt) *ParsedSkill {
	return &ParsedSkill{
		Frontmatter: Frontmatter{
			Name:        "mcp__" + server + "__" + p.Name,
			Description: p.Description,
		},
		Body:       "",
		SourcePath: "mcp://" + server + "/" + p.Name,
	}
}
