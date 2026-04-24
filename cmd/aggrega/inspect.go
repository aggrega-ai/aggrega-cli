// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/catalog"
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

	// Resolve extends chains so the printed cueSchema reflects inheritance.
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}

	comp, found := cat.Components[componentType]
	if !found {
		return fmt.Errorf("component %q not found in catalog", componentType)
	}

	header := fmt.Sprintf("// Component: %s", componentType)
	if comp.Spec.Extends != "" {
		header += " (resolved from inheritance chain)"
	}
	fmt.Println(header)

	if comp.Spec.CueSchema != "" {
		fmt.Println(comp.Spec.CueSchema)
	} else if comp.Spec.CueSchemaRef != "" {
		fmt.Printf("// schema ref: %s\n", comp.Spec.CueSchemaRef)
	} else {
		fmt.Println("// (no CUE schema defined)")
	}

	// Print usesSchema if present
	if comp.Spec.UsesSchema.Len() > 0 {
		fmt.Println("\n// usesSchema:")
		for socket, entry := range comp.Spec.UsesSchema.All() {
			fmt.Printf("//   %s: selector=%q\n", socket, entry.Selector)
		}
	}

	return nil
}
