package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// LoadComponentCatalogFromDir loads flat `*.yaml` ComponentDefinition / Component documents
// from dir into a Catalog indexed by component.aggrega.ai/type (or metadata.name).
func LoadComponentCatalogFromDir(dir string) (*types.Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cat := &types.Catalog{ComponentDefinitions: make(map[string]types.ComponentDefinition)}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("catalog %q: %w", e.Name(), err)
		}
		var def types.ComponentDefinition
		if err := yaml.Unmarshal(data, &def); err != nil {
			return nil, fmt.Errorf("catalog %q: parse yaml: %w", e.Name(), err)
		}
		key := ""
		if def.Metadata.Labels != nil {
			key = strings.TrimSpace(def.Metadata.Labels["component.aggrega.ai/type"])
		}
		if key == "" {
			key = strings.TrimSpace(def.Metadata.Name)
		}
		if key == "" {
			return nil, fmt.Errorf("catalog %q: missing component.aggrega.ai/type label and metadata.name", e.Name())
		}
		cat.ComponentDefinitions[key] = def
	}
	if len(cat.ComponentDefinitions) == 0 {
		return nil, nil
	}
	return cat, nil
}

// TryLoadComponentCatalog walks candidate directories and returns the first non-empty catalog.
func TryLoadComponentCatalog(candidates ...string) (*types.Catalog, error) {
	for _, dir := range candidates {
		cat, err := LoadComponentCatalogFromDir(dir)
		if err != nil {
			return nil, err
		}
		if cat != nil && len(cat.ComponentDefinitions) > 0 {
			return cat, nil
		}
	}
	return nil, nil
}
