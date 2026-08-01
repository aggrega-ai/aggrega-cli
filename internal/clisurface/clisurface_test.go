// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clisurface

import (
	"reflect"
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

// newTree builds a tree that exercises every axis the catalog records: nesting
// deeper than one level, a hidden command, commands named like cobra's
// generated ones, a persistent flag inherited by descendants, a local flag, and
// a hidden flag.
func newTree() *cobra.Command {
	root := &cobra.Command{Use: "aggrega", Short: "root", Long: "root long"}
	root.PersistentFlags().String("workspace-root", "", "workspace root path")

	build := &cobra.Command{Use: "build [aggregate]", Short: "compile", Run: func(*cobra.Command, []string) {}}
	build.Flags().StringP("release", "r", "prod", "release file")
	build.Flags().Bool("secret", false, "hidden flag")
	_ = build.Flags().MarkHidden("secret")

	auth := &cobra.Command{Use: "auth", Short: "identity"}
	auth.PersistentFlags().String("issuer", "", "issuer url")
	auth.AddCommand(&cobra.Command{Use: "token", Short: "get token", Run: func(*cobra.Command, []string) {}})

	hidden := &cobra.Command{Use: "internals", Short: "hidden cmd", Hidden: true, Run: func(*cobra.Command, []string) {}}
	help := &cobra.Command{Use: "help", Short: "help about any command", Run: func(*cobra.Command, []string) {}}
	completion := &cobra.Command{Use: "completion", Short: "generate completion script", Run: func(*cobra.Command, []string) {}}

	root.AddCommand(build, auth, hidden, help, completion)
	return root
}

// walkPaths collects every command path reachable from root by a walk written
// independently of Catalog, so the two cannot share a bug.
func walkPaths(root *cobra.Command) []string {
	var paths []string
	queue := []*cobra.Command{root}
	for len(queue) > 0 {
		cmd := queue[0]
		queue = queue[1:]
		paths = append(paths, cmd.CommandPath())
		queue = append(queue, cmd.Commands()...)
	}
	return paths
}

func catalogPaths(cat []Descriptor) []string {
	paths := make([]string, 0, len(cat))
	for _, d := range cat {
		paths = append(paths, d.Path)
	}
	return paths
}

// TestCatalogCoversEveryCommandInTree is the completeness guard: it compares
// paths, not counts, in both directions, so a count that happens to match while
// a path differs still fails.
func TestCatalogCoversEveryCommandInTree(t *testing.T) {
	root := newTree()

	want := walkPaths(root)
	got := catalogPaths(Catalog(root))

	sortedWant := append([]string(nil), want...)
	sortedGot := append([]string(nil), got...)
	sort.Strings(sortedWant)
	sort.Strings(sortedGot)

	if !reflect.DeepEqual(sortedWant, sortedGot) {
		t.Fatalf("catalog does not cover the tree:\n reachable %v\n catalog   %v", sortedWant, sortedGot)
	}
	if len(got) != len(want) {
		t.Errorf("catalog has %d entries for %d reachable commands", len(got), len(want))
	}

	// The axes the catalog deliberately does not filter.
	for _, path := range []string{"aggrega internals", "aggrega help", "aggrega completion", "aggrega auth token"} {
		if !containsPath(got, path) {
			t.Errorf("expected %q in the catalog, got %v", path, got)
		}
	}
}

func TestCatalogIsPreOrderWithRootFirstAndDeterministic(t *testing.T) {
	first := Catalog(newTree())
	second := Catalog(newTree())

	if !reflect.DeepEqual(first, second) {
		t.Error("two catalogs of identical trees differ")
	}
	if len(first) == 0 || first[0].Path != "aggrega" || first[0].Parent != "" {
		t.Fatalf("expected the root first with an empty parent, got %+v", first[0])
	}

	paths := catalogPaths(first)
	want := []string{
		"aggrega",
		"aggrega auth",
		"aggrega auth token",
		"aggrega build",
		"aggrega completion",
		"aggrega help",
		"aggrega internals",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("pre-order walk mismatch:\n got  %v\n want %v", paths, want)
	}
}

func TestDescriptorRecordsCobraFactsVerbatim(t *testing.T) {
	cat := Catalog(newTree())

	build := find(t, cat, "aggrega build")
	if build.Name != "build" {
		t.Errorf("Name = %q, want build", build.Name)
	}
	if build.Parent != "aggrega" {
		t.Errorf("Parent = %q, want aggrega", build.Parent)
	}
	if build.Use != "build [aggregate]" {
		t.Errorf("Use = %q, want the usage declaration verbatim", build.Use)
	}
	if build.Synopsis != "aggrega build [aggregate] [flags]" {
		t.Errorf("Synopsis = %q, want the full usage line", build.Synopsis)
	}
	if build.Short != "compile" {
		t.Errorf("Short = %q, want compile", build.Short)
	}
	if build.Hidden {
		t.Error("build is not hidden but Hidden is set")
	}

	release := findFlag(t, build.LocalFlags, "release")
	if release.Shorthand != "r" || release.Type != "string" || release.Default != "prod" || release.Usage != "release file" {
		t.Errorf("release flag recorded as %+v", release)
	}
	if secret := findFlag(t, build.LocalFlags, "secret"); !secret.Hidden || secret.Type != "bool" {
		t.Errorf("hidden bool flag recorded as %+v", secret)
	}

	if !find(t, cat, "aggrega internals").Hidden {
		t.Error("a hidden command must carry Hidden")
	}
	if root := find(t, cat, "aggrega"); root.Long != "root long" {
		t.Errorf("Long = %q, want root long", root.Long)
	}
}

// TestFlagViewsSeparateLocalFromInherited pins the split the catalog exists to
// make available: a descendant sees an ancestor's persistent flag as inherited
// and never as its own.
func TestFlagViewsSeparateLocalFromInherited(t *testing.T) {
	cat := Catalog(newTree())

	root := find(t, cat, "aggrega")
	if !hasFlag(root.PersistentFlags, "workspace-root") {
		t.Error("root must declare workspace-root persistently")
	}
	if len(root.InheritedFlags) != 0 {
		t.Errorf("the root inherits nothing, got %+v", root.InheritedFlags)
	}

	token := find(t, cat, "aggrega auth token")
	if hasFlag(token.LocalFlags, "workspace-root") || hasFlag(token.LocalFlags, "issuer") {
		t.Errorf("ancestor persistent flags leaked into LocalFlags: %+v", token.LocalFlags)
	}
	if !hasFlag(token.InheritedFlags, "workspace-root") {
		t.Error("the root's persistent flag must appear as inherited on a leaf")
	}
	if !hasFlag(token.InheritedFlags, "issuer") {
		t.Error("an intermediate command's persistent flag must appear as inherited on its child")
	}

	auth := find(t, cat, "aggrega auth")
	if !hasFlag(auth.PersistentFlags, "issuer") {
		t.Error("auth must declare issuer persistently")
	}
	if !hasFlag(auth.LocalFlags, "issuer") {
		t.Error("a command's own persistent flag is also local to it")
	}
	if hasFlag(auth.PersistentFlags, "workspace-root") {
		t.Error("an inherited flag is not declared persistently by the inheriting command")
	}
}

func TestChildrenResolvesTheParentRelation(t *testing.T) {
	cat := Catalog(newTree())

	top := catalogPaths(Children(cat, "aggrega"))
	want := []string{"aggrega auth", "aggrega build", "aggrega completion", "aggrega help", "aggrega internals"}
	if !reflect.DeepEqual(top, want) {
		t.Errorf("children of the root:\n got  %v\n want %v", top, want)
	}

	if leaves := Children(cat, "aggrega build"); len(leaves) != 0 {
		t.Errorf("a leaf has no children, got %v", catalogPaths(leaves))
	}
	if nested := catalogPaths(Children(cat, "aggrega auth")); !reflect.DeepEqual(nested, []string{"aggrega auth token"}) {
		t.Errorf("children of auth = %v", nested)
	}
	if rootOnly := catalogPaths(Children(cat, "")); !reflect.DeepEqual(rootOnly, []string{"aggrega"}) {
		t.Errorf("the empty parent selects the root, got %v", rootOnly)
	}
}

func TestCatalogOfNilRootIsEmpty(t *testing.T) {
	if cat := Catalog(nil); cat != nil {
		t.Errorf("expected a nil catalog, got %v", catalogPaths(cat))
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func find(t *testing.T, cat []Descriptor, path string) Descriptor {
	t.Helper()
	for _, d := range cat {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("no descriptor for %q in %v", path, catalogPaths(cat))
	return Descriptor{}
}

func findFlag(t *testing.T, flags []Flag, name string) Flag {
	t.Helper()
	for _, f := range flags {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no flag %q in %+v", name, flags)
	return Flag{}
}

func hasFlag(flags []Flag, name string) bool {
	for _, f := range flags {
		if f.Name == name {
			return true
		}
	}
	return false
}
