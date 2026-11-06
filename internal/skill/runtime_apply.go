package skill

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// RuntimePatch describes a set of overrides to apply to a named skill at
// runtime without touching the on-disk YAML/Markdown source.
type RuntimePatch struct {
	// Name is the skill name this patch targets.
	Name string

	// DescriptionOverride replaces the skill's description when non-empty.
	DescriptionOverride string

	// DisabledReason, when non-empty, marks the skill as disabled. The value
	// is used as the human-readable explanation. An empty string means the
	// skill is enabled.
	DisabledReason string

	// ParamOverrides maps parameter names to replacement description strings.
	// Only parameters listed in the map are overridden; others are unchanged.
	ParamOverrides map[string]string
}

// PatchRegistry is a concurrency-safe store of RuntimePatch values keyed by
// skill name.
type PatchRegistry struct {
	mu      sync.RWMutex
	patches map[string]RuntimePatch
}

// NewPatchRegistry returns an empty, ready-to-use PatchRegistry.
func NewPatchRegistry() *PatchRegistry {
	return &PatchRegistry{
		patches: make(map[string]RuntimePatch),
	}
}

// globalPatchRegistry is the process-wide default PatchRegistry.
var globalPatchRegistry = NewPatchRegistry()

// GetPatchRegistry returns the global PatchRegistry singleton.
func GetPatchRegistry() *PatchRegistry {
	return globalPatchRegistry
}

// Apply stores (or replaces) the patch for patch.Name.
func (pr *PatchRegistry) Apply(patch RuntimePatch) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.patches[patch.Name] = patch
}

// Remove deletes the patch for the given skill name. It is a no-op when no
// patch exists for that name.
func (pr *PatchRegistry) Remove(name string) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	delete(pr.patches, name)
}

// Get returns the patch for the given skill name and whether it was found.
func (pr *PatchRegistry) Get(name string) (RuntimePatch, bool) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	p, ok := pr.patches[name]
	return p, ok
}

// List returns all stored patches sorted by skill name.
func (pr *PatchRegistry) List() []RuntimePatch {
	pr.mu.RLock()
	defer pr.mu.RUnlock()

	out := make([]RuntimePatch, 0, len(pr.patches))
	for _, p := range pr.patches {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// disabledPrefix is prepended to a skill's name when it is disabled so that
// the LLM can see (and avoid calling) the skill without it being removed from
// the tool list entirely.
const disabledPrefix = "[disabled] "

// ApplyPatches returns a copy of s with any matching patch from pr applied.
// If no patch exists for s.Name the original Skill is returned unchanged.
//
// Patch semantics:
//   - DescriptionOverride — replaces Description when non-empty.
//   - DisabledReason — when non-empty, the skill's Name is prefixed with
//     "[disabled] " and the Description is replaced with a message explaining
//     why the skill is unavailable.
//   - ParamOverrides — for each entry the matching property's Description
//     field inside Parameters.Properties is replaced. Properties not listed in
//     the map are left unchanged. A nil Properties map is left as-is.
func ApplyPatches(s Skill, pr *PatchRegistry) Skill {
	patch, ok := pr.Get(s.Name)
	if !ok {
		return s
	}

	// Work on a copy so callers keep the original untouched.
	patched := s

	if patch.DescriptionOverride != "" {
		patched.Description = patch.DescriptionOverride
	}

	if patch.DisabledReason != "" {
		patched.Name = disabledPrefix + s.Name
		patched.Description = fmt.Sprintf(
			"[skill disabled: %s] %s",
			patch.DisabledReason,
			patched.Description,
		)
	}

	// Apply parameter description overrides via a JSON round-trip so that this
	// file does not need to import the jsonschema package directly.
	if len(patch.ParamOverrides) > 0 && patched.Parameters.Properties != nil {
		raw, err := json.Marshal(patched.Parameters)
		if err == nil {
			// Unmarshal into a generic map so we can patch descriptions without
			// knowing the concrete jsonschema type.
			var generic map[string]interface{}
			if err = json.Unmarshal(raw, &generic); err == nil {
				if props, ok2 := generic["properties"].(map[string]interface{}); ok2 {
					for paramName, newDesc := range patch.ParamOverrides {
						if prop, exists := props[paramName].(map[string]interface{}); exists {
							prop["description"] = newDesc
							props[paramName] = prop
						}
					}
					generic["properties"] = props
				}
				if patched2, err2 := json.Marshal(generic); err2 == nil {
					_ = json.Unmarshal(patched2, &patched.Parameters)
				}
			}
		}
	}

	return patched
}
