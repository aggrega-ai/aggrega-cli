// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/cognitio/aggrega-cli/pkg/validator"
	"github.com/spf13/cobra"
)

var inspectCatalogDir string

var inspectCmd = &cobra.Command{
	Use:   "inspect <component-type>",
	Short: "Render the final merged CUE schema for a component (base + all active overlays)",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func init() {
	inspectCmd.Flags().StringVar(&inspectCatalogDir, "catalog", ".", "Directory containing catalog entities")
	rootCmd.AddCommand(inspectCmd)
}

func runInspect(cmd *cobra.Command, args []string) error {
	componentType := args[0]

	cat, err := catalog.Load(inspectCatalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	comp, found := cat.Components[componentType]
	if !found {
		return fmt.Errorf("component %q not found in catalog", componentType)
	}

	// If component has extends, resolve inheritance chain
	if comp.Spec.Extends != "" {
		entry := validator.CatalogEntry{
			URI:     comp.Metadata.Name,
			Extends: comp.Spec.Extends,
			CUE:     comp.Spec.CueSchema,
		}
		lookup := func(uri string) (validator.CatalogEntry, bool) {
			c, ok := cat.Components[uri]
			if !ok {
				return validator.CatalogEntry{}, false
			}
			return validator.CatalogEntry{
				URI:     c.Metadata.Name,
				Extends: c.Spec.Extends,
				CUE:     c.Spec.CueSchema,
			}, true
		}

		result := validator.ResolveInheritance(entry, lookup)
		if len(result.Errors) > 0 {
			return fmt.Errorf("resolve inheritance for %q: %s", componentType, result.Errors[0].Error())
		}

		fmt.Printf("// Component: %s (resolved from inheritance chain)\n", componentType)
		if result.UnifiedCUE != "" {
			fmt.Println(result.UnifiedCUE)
		}
		return nil
	}

	// No inheritance — print raw CUE schema
	fmt.Printf("// Component: %s\n", componentType)
	if comp.Spec.CueSchema != "" {
		fmt.Println(comp.Spec.CueSchema)
	} else if comp.Spec.CueSchemaRef != "" {
		fmt.Printf("// schema ref: %s\n", comp.Spec.CueSchemaRef)
	} else {
		fmt.Println("// (no CUE schema defined)")
	}

	// Print usesSchema if present
	if len(comp.Spec.UsesSchema) > 0 {
		fmt.Println("\n// usesSchema:")
		for socket, entry := range comp.Spec.UsesSchema {
			fmt.Printf("//   %s: selector=%q\n", socket, entry.Selector)
		}
	}

	return nil
}
