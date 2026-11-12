// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Command pluginguestfile prints the filename a Plugin entity's .wasm guest is
// written under, derived through the shipped mapping rather than spelled again
// here. It reads the entity, takes the artifact reference the entity resolves
// to, and prints pluginpath.PluginLeaf of it — so a caller staging or pairing a
// guest asks the same code the resolver reads by, instead of reproducing the
// rule in a second language where it would drift.
//
// The reference carries every part that tells two plugins apart: the socket and
// the bare name. Deriving the filename from only part of it is lossy — a bare
// name is unique only within its socket, and a name may itself carry the
// separator its coordinate is joined on, so a derivation that keeps one segment
// discards the rest of the plugin's location. Nothing here constrains what a
// plugin may be named: the encoding maps two distinct references to two
// distinct filenames as a property of the encoding, so two publishers shipping
// a same-named plugin land on separate files with no rule imposed on either.
//
// It is a build tool, not a shipped command: the plugin publishing and
// staging scripts invoke it to name the file they write or look for. It writes
// exactly one line to standard output — the filename, no directory — so a
// caller joins it to whichever directory it stages into.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

func main() {
	entity := flag.String("entity", "", "path to the Plugin entity YAML to derive the guest filename for")
	flag.Parse()

	if *entity == "" {
		fmt.Fprintln(os.Stderr, "pluginguestfile: -entity is required")
		os.Exit(2)
	}

	name, err := run(*entity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pluginguestfile: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(name)
}

// run reads the Plugin entity at path and returns its guest's filename.
//
// The entity is decoded into the shipped Plugin type, so the identity fields
// are read through the same struct the catalog loads, and the filename comes
// from the entity's own artifact reference. A kind other than Plugin, or an
// entity missing either identity field, is refused rather than reduced to a
// filename that would name a file nothing reads.
func run(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read entity: %w", err)
	}

	var p types.Plugin
	if err := yaml.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("unmarshal Plugin from %s: %w", path, err)
	}
	if p.Kind != "Plugin" {
		return "", fmt.Errorf("%s is kind %q, not Plugin", path, p.Kind)
	}
	if p.Metadata.Name == "" || p.Metadata.PluginType == "" {
		return "", fmt.Errorf(
			"%s has no plugin identity: metadata.name is %q and metadata.pluginType is %q, and both name the guest",
			path, p.Metadata.Name, p.Metadata.PluginType)
	}

	return pluginpath.PluginLeaf(p.ArtifactReference()), nil
}
