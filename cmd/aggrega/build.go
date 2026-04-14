// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var buildScope string

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Compile Aggregate + Manifest into IR and render artifacts for a release scope",
	Run:   runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&buildScope, "scope", "", "Release scope name to build (must match spec.scopes[].name, e.g. local or prod)")
	if err := buildCmd.MarkFlagRequired("scope"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) {
	_ = cmd
	_ = args

	fmt.Fprintln(os.Stderr, "aggrega build:  rewrite in progress — pipeline not yet wired")
	os.Exit(1)
}

func validateScopeSegment(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("--scope must not be empty")
	}
	if strings.Contains(s, "..") || strings.ContainsAny(s, `/\`) {
		return fmt.Errorf("--scope %q must not contain path separators or '..'", s)
	}
	return nil
}

func writeArtifacts(outRoot string, files map[string][]byte) error {
	outRoot = filepath.Clean(outRoot)
	for relPath, content := range files {
		if relPath == "" {
			return fmt.Errorf("empty artifact path")
		}
		relPath = filepath.Clean(relPath)
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid artifact path %q", relPath)
		}
		full := filepath.Clean(filepath.Join(outRoot, relPath))
		relToOut, err := filepath.Rel(outRoot, full)
		if err != nil {
			return fmt.Errorf("artifact path %q: %w", relPath, err)
		}
		if relToOut == ".." || strings.HasPrefix(relToOut, ".."+string(filepath.Separator)) {
			return fmt.Errorf("artifact path %q escapes output directory", relPath)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", full, err)
		}
	}
	return nil
}

