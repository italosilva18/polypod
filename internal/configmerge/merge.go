package configmerge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MergeConfigDir loads all .yaml files from a directory and merges them
// into a single map, in alphabetical order (later files override earlier ones).
func MergeConfigDir(dir string) (map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Sort alphabetically for deterministic order
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	result := make(map[string]any)
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		var fragment map[string]any
		if err := yaml.Unmarshal(data, &fragment); err != nil {
			continue
		}
		deepMerge(result, fragment)
	}

	return result, nil
}

// MergeIntoConfig merges a config fragment map into an existing config YAML bytes.
func MergeIntoConfig(baseYAML []byte, overrides map[string]any) ([]byte, error) {
	if len(overrides) == 0 {
		return baseYAML, nil
	}

	var base map[string]any
	if err := yaml.Unmarshal(baseYAML, &base); err != nil {
		return nil, err
	}
	if base == nil {
		base = make(map[string]any)
	}

	deepMerge(base, overrides)
	return yaml.Marshal(base)
}

// deepMerge recursively merges src into dst. src values override dst values.
func deepMerge(dst, src map[string]any) {
	for key, srcVal := range src {
		dstVal, exists := dst[key]
		if !exists {
			dst[key] = srcVal
			continue
		}

		// Both are maps: recurse
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dstVal.(map[string]any)
		if srcIsMap && dstIsMap {
			deepMerge(dstMap, srcMap)
			continue
		}

		// Otherwise: src overrides dst
		dst[key] = srcVal
	}
}

// ListFragments returns the names of config fragments in a directory.
func ListFragments(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			names = append(names, strings.TrimSuffix(e.Name(), ext))
		}
	}
	sort.Strings(names)
	return names
}
