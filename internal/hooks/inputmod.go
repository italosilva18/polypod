// Package hooks — tool input/output modification helpers.
//
// These helpers implement the "updatedInput" / "updatedMCPToolOutput" semantics
// from the structured hook response contract (see structured.go). A hook may
// emit a new, fully-formed object to replace the original, or a partial object
// that should be shallow-merged on top of the original. We treat any object
// payload as a shallow-merge candidate — the caller decides whether the merge
// semantics are appropriate for their use case.
package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ApplyInputModification returns a new JSON object built by shallow-merging
// `updated` onto `original`. Behaviour:
//
//   - If `updated` is nil or empty, `original` is returned unchanged.
//   - If `original` is nil/empty and `updated` is a valid JSON object,
//     `updated` is returned as-is (after round-trip validation).
//   - If both are valid JSON objects, top-level keys from `updated` overwrite
//     keys in `original`. Nested values are NOT deep-merged — they are
//     replaced wholesale, matching the behaviour of Object.assign in the TS
//     reference.
//   - If `updated` is not a JSON object (array, scalar, invalid), returns
//     an error.
//
// The returned RawMessage is compact (no extra whitespace).
func ApplyInputModification(original, updated json.RawMessage) (json.RawMessage, error) {
	if len(updated) == 0 {
		return original, nil
	}

	updatedMap, err := decodeObject(updated)
	if err != nil {
		return nil, fmt.Errorf("hooks: updated input: %w", err)
	}

	if len(original) == 0 {
		return marshalObject(updatedMap)
	}

	originalMap, err := decodeObject(original)
	if err != nil {
		// Original wasn't an object — replace it wholesale. This matches the
		// permissive TS behaviour where a hook can rewrite a non-object input.
		return marshalObject(updatedMap)
	}

	// Shallow merge: copy original, then overwrite with updated keys.
	merged := make(map[string]json.RawMessage, len(originalMap)+len(updatedMap))
	for k, v := range originalMap {
		merged[k] = v
	}
	for k, v := range updatedMap {
		merged[k] = v
	}
	return marshalObject(merged)
}

// ApplyOutputModification is identical in semantics to ApplyInputModification
// but is named separately to make call sites self-documenting at the hook
// dispatch layer (PreToolUse modifies input, PostToolUse modifies output).
func ApplyOutputModification(original, updated json.RawMessage) (json.RawMessage, error) {
	return ApplyInputModification(original, updated)
}

// ValidateUpdatedInput validates the common JSON Schema subset used by tool
// inputs: type, required, properties, items and enum.
func ValidateUpdatedInput(schema json.RawMessage, updated json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var schemaValue map[string]any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return fmt.Errorf("hooks: invalid input schema: %w", err)
	}
	var value any
	if err := json.Unmarshal(updated, &value); err != nil {
		return fmt.Errorf("hooks: invalid updated input: %w", err)
	}
	return validateSchemaValue(schemaValue, value, "$")
}

func validateSchemaValue(schema map[string]any, value any, path string) error {
	if enumValues, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enumValues {
			if fmt.Sprint(candidate) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("hooks: %s is not an allowed enum value", path)
		}
	}

	typeName, _ := schema["type"].(string)
	switch typeName {
	case "", "any":
		return nil
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("hooks: %s must be an object", path)
		}
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				name, _ := item.(string)
				if name != "" {
					if _, exists := object[name]; !exists {
						return fmt.Errorf("hooks: %s.%s is required", path, name)
					}
				}
			}
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			for name, rawProperty := range properties {
				propertySchema, ok := rawProperty.(map[string]any)
				if propertyValue, exists := object[name]; ok && exists {
					if err := validateSchemaValue(propertySchema, propertyValue, path+"."+name); err != nil {
						return err
					}
				}
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("hooks: %s must be an array", path)
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range array {
				if err := validateSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("hooks: %s must be a string", path)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("hooks: %s must be a number", path)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("hooks: %s must be an integer", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("hooks: %s must be a boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("hooks: %s must be null", path)
		}
	default:
		return fmt.Errorf("hooks: unsupported schema type %q at %s", typeName, path)
	}
	return nil
}

// --- internal helpers ---

// decodeObject decodes `raw` as a JSON object into a map of RawMessage values.
// Returns an error if `raw` is not a JSON object.
func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty JSON")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		// Explicit JSON null.
		return nil, errors.New("not a JSON object (got null)")
	}
	return m, nil
}

// marshalObject encodes a map of RawMessage values back to a compact
// json.RawMessage.
func marshalObject(m map[string]json.RawMessage) (json.RawMessage, error) {
	buf, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("hooks: marshal merged object: %w", err)
	}
	return json.RawMessage(buf), nil
}
