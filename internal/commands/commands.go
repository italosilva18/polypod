package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Command is a project-level slash command defined in .polypod/commands/*.md.
type Command struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Disabled    bool   `yaml:"disabled"`    // if true, can't be invoked
	AIOnly      bool   `yaml:"ai_only"`     // if true, only AI can invoke
	Content     string `yaml:"-"`           // the markdown body (prompt)
	FilePath    string `yaml:"-"`
}

// Recipe is a multi-step automation workflow.
type Recipe struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Parameters  []RecipeParam `yaml:"parameters"`
	Steps       []RecipeStep `yaml:"steps"`
	FilePath    string       `yaml:"-"`
}

// RecipeParam is a recipe parameter.
type RecipeParam struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
}

// RecipeStep is a single step in a recipe.
type RecipeStep struct {
	Name    string `yaml:"name"`
	Prompt  string `yaml:"prompt"`  // AI prompt for this step
	Command string `yaml:"command"` // shell command (alternative to prompt)
	OnError string `yaml:"on_error"` // "stop", "continue", "retry"
}

// Registry holds loaded commands and recipes.
type Registry struct {
	Commands map[string]*Command
	Recipes  map[string]*Recipe
}

// NewRegistry creates an empty command/recipe registry.
func NewRegistry() *Registry {
	return &Registry{
		Commands: make(map[string]*Command),
		Recipes:  make(map[string]*Recipe),
	}
}

// LoadFromDir scans .polypod/commands/ for markdown command files and .polypod/recipes/ for YAML recipes.
func (r *Registry) LoadFromDir(baseDir string) error {
	// Load slash commands
	cmdDir := filepath.Join(baseDir, "commands")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			cmd, err := loadCommand(filepath.Join(cmdDir, e.Name()))
			if err != nil {
				continue
			}
			r.Commands[cmd.Name] = cmd
		}
	}

	// Load recipes
	recipeDir := filepath.Join(baseDir, "recipes")
	if entries, err := os.ReadDir(recipeDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := filepath.Ext(e.Name())
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			recipe, err := loadRecipe(filepath.Join(recipeDir, e.Name()))
			if err != nil {
				continue
			}
			r.Recipes[recipe.Name] = recipe
		}
	}

	return nil
}

func loadCommand(path string) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	cmd := &Command{
		Name:     strings.TrimSuffix(filepath.Base(path), ".md"),
		FilePath: path,
	}

	// Parse YAML frontmatter (between --- delimiters)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content[3:], "---", 2)
		if len(parts) == 2 {
			// Parse frontmatter
			yaml.Unmarshal([]byte(parts[0]), cmd)
			content = strings.TrimSpace(parts[1])
		}
	}

	if cmd.Name == "" {
		cmd.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}

	cmd.Content = content
	return cmd, nil
}

func loadRecipe(path string) (*Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var recipe Recipe
	if err := yaml.Unmarshal(data, &recipe); err != nil {
		return nil, err
	}
	if recipe.Name == "" {
		recipe.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	recipe.FilePath = path
	return &recipe, nil
}

// GetCommand returns a command by name (with /prefix handling).
func (r *Registry) GetCommand(name string) (*Command, bool) {
	name = strings.TrimPrefix(name, "/")
	cmd, ok := r.Commands[name]
	return cmd, ok
}

// GetRecipe returns a recipe by name.
func (r *Registry) GetRecipe(name string) (*Recipe, bool) {
	recipe, ok := r.Recipes[name]
	return recipe, ok
}

// ApplyCommand replaces $ARGUMENTS in command content.
func ApplyCommand(cmd *Command, args string) string {
	content := cmd.Content
	content = strings.ReplaceAll(content, "$ARGUMENTS", args)
	content = strings.ReplaceAll(content, "{{args}}", args)
	return content
}

// ApplyRecipeParams replaces parameters in recipe steps.
func ApplyRecipeParams(recipe *Recipe, params map[string]string) []RecipeStep {
	steps := make([]RecipeStep, len(recipe.Steps))
	for i, step := range recipe.Steps {
		s := step
		for k, v := range params {
			placeholder := fmt.Sprintf("{{%s}}", k)
			s.Prompt = strings.ReplaceAll(s.Prompt, placeholder, v)
			s.Command = strings.ReplaceAll(s.Command, placeholder, v)
		}
		// Apply defaults for missing params
		for _, p := range recipe.Parameters {
			placeholder := fmt.Sprintf("{{%s}}", p.Name)
			if _, ok := params[p.Name]; !ok && p.Default != "" {
				s.Prompt = strings.ReplaceAll(s.Prompt, placeholder, p.Default)
				s.Command = strings.ReplaceAll(s.Command, placeholder, p.Default)
			}
		}
		steps[i] = s
	}
	return steps
}

// ListCommands returns all command names with descriptions.
func (r *Registry) ListCommands() []string {
	var result []string
	for _, cmd := range r.Commands {
		if cmd.Disabled {
			continue
		}
		desc := cmd.Description
		if desc == "" {
			desc = "(sem descricao)"
		}
		result = append(result, fmt.Sprintf("/%s - %s", cmd.Name, desc))
	}
	return result
}

// ListRecipes returns all recipe names with descriptions.
func (r *Registry) ListRecipes() []string {
	var result []string
	for _, r := range r.Recipes {
		desc := r.Description
		if desc == "" {
			desc = "(sem descricao)"
		}
		result = append(result, fmt.Sprintf("%s - %s (%d steps)", r.Name, desc, len(r.Steps)))
	}
	return result
}
