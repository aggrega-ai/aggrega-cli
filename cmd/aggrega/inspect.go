// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var inspectCatalogDir string

var inspectCmd = &cobra.Command{
	Use:   "inspect <component-type>",
	Short: "Render the final merged schema for a component (base + all active overlays)",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func init() {
	inspectCmd.Flags().StringVar(&inspectCatalogDir, "catalog", ".", "Directory containing catalog entities")
	rootCmd.AddCommand(inspectCmd)
}

// runInspect implements the inspect subcommand: loads the catalog at
// --catalog, resolves extends chains so the printed schema reflects
// inheritance, looks up the requested component by name (the single
// positional argument), and prints to stdout:
//
//   - A header line naming the component (with "(resolved from
//     inheritance chain)" suffix when extends was resolved).
//   - The schema body if the component declares one, or a
//     "// (no schema defined)" placeholder otherwise.
//   - A "// usesSchema:" header followed by per-socket selector
//     entries if the component declares a usesSchema.
//
// Catalog load and extends resolution failures return wrapped errors
// (cobra prints them to stderr via the Execute error path and exits
// with status 1). The extends path returns only the first error
// rather than enumerating all of them, distinguishing inspect's
// fail-fast posture from build's enumerate-all-extends-errors posture.
func runInspect(cmd *cobra.Command, args []string) error {
	componentType := args[0]

	cat, err := catalog.Load(inspectCatalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	// Resolve extends chains so the printed schema reflects inheritance.
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

	if comp.Spec.Schema.Kind != 0 {
		out, err := yaml.Marshal(&comp.Spec.Schema)
		if err != nil {
			return fmt.Errorf("render schema: %w", err)
		}
		fmt.Print(string(out))
	} else {
		fmt.Println("// (no schema defined)")
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
