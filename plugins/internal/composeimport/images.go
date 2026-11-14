// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composeimport

// The image table is generated from the Compose Recipes and Components of the
// aggrega-hub checkout beside this repository; a test in ./imagetable fails while
// the generated table differs from what a regeneration would write.
//go:generate go run ./imagetable -hub ../../../../aggrega-hub -out imagetable_gen.go

import (
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// imageRow is one row of the image table: an image repository, the catalog
// Component a Compose Recipe of the hub that runs an image of that repository
// implements, every label of that Component in the hub but its category, and
// what the hub's Compose Recipes of that Component say of how a service running
// the image is configured, each read from the Recipes as they are written:
//
//   - Tag, the infra key the image's tag is written from;
//   - Values, each variable of the service's environment the Recipes write as
//     one value of the Component, mapped to that value's key;
//   - Secrets, each variable the Recipes read an attached secret's value under
//     where the attachment declares no key of its own, mapped to the slot the
//     secret is attached on;
//   - Provides, each value of the Component a provides key publishes whole,
//     mapped to that key, and Host, the provides key publishing the instance's
//     own name, which a peer on the network reaches it by.
type imageRow struct {
	Repository string
	Component  string
	Traits     map[string]string
	Tag        string
	Values     map[string]string
	Secrets    map[string]string
	Provides   map[string]string
	Host       string
}

// Repository returns the repository an image reference names, without its tag
// and its digest, in the short form Docker reads a reference by: a reference on
// Docker Hub named with its registry, docker.io or index.docker.io, is written
// without it, and an official image named under library/ is written without
// that. It reports false for text that is not a reference — empty, holding a
// variable to interpolate or a space, or a path segment Docker would refuse. The
// image table is keyed by what it returns, read off a Recipe's image by the tool
// generating the table and off a service's image by the importer, so both read
// one repository from one reference.
func Repository(ref string) (string, bool) {
	repository, _, ok := Reference(ref)
	return repository, ok
}

// Reference reads an image reference as Repository does, returning beside the
// repository the tag the reference names: empty where it names none, and where
// it is pinned by a digest, which names the image whatever its tag says.
func Reference(ref string) (repository, tag string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.ContainsAny(ref, "${} \t\n\r\"'") {
		return "", "", false
	}
	digested := false
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		ref, digested = ref[:at], true
	}
	if colon := strings.LastIndexByte(ref, ':'); colon > strings.LastIndexByte(ref, '/') {
		if !digested {
			tag = ref[colon+1:]
		}
		ref = ref[:colon]
	}
	parts := strings.Split(ref, "/")
	start := 0
	if len(parts) > 1 && registryHost(parts[0]) {
		start = 1
	}
	for _, p := range parts[start:] {
		if !pathComponent(p) {
			return "", "", false
		}
	}
	if start == 1 && !hostComponent(parts[0]) {
		return "", "", false
	}
	if len(parts) > 1 && (parts[0] == "docker.io" || parts[0] == "index.docker.io") {
		parts = parts[1:]
	}
	if len(parts) > 1 && parts[0] == "library" {
		parts = parts[1:]
	}
	return strings.Join(parts, "/"), tag, true
}

// registryHost reports whether the first segment of a reference names a
// registry rather than a path: it holds a dot or a port, or is localhost.
func registryHost(segment string) bool {
	return strings.ContainsAny(segment, ".:") || segment == "localhost"
}

// hostComponent reports whether segment is a registry host, optionally with a
// port: letters, digits, dots and hyphens, then a colon and digits.
func hostComponent(segment string) bool {
	host, port, hasPort := strings.Cut(segment, ":")
	if host == "" || (hasPort && port == "") {
		return false
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pathComponent reports whether segment is a path segment of a repository as
// Docker reads one, loosely: lowercase letters, digits, dots, underscores and
// hyphens, beginning and ending with a letter or a digit.
func pathComponent(segment string) bool {
	if segment == "" {
		return false
	}
	alnum := func(r byte) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }
	if !alnum(segment[0]) || !alnum(segment[len(segment)-1]) {
		return false
	}
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if alnum(c) {
			continue
		}
		if c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// mappings is the image table read as the importer's mapping arm: each image
// repository maps to the Component its row names, under the category that
// Component's name places it in, with the row's traits — the Component's other
// labels in the hub — and with the last segment of its name as its vendor. A
// service of a mapped image therefore matches that Component, where the
// workspace's catalog carries it with the hub's labels, at the high tier. The
// mapping_overrides setting layers over it, keyed by repository, as it layers
// over every importer's arm.
func mappings() map[string]importer.Mapping {
	out := make(map[string]importer.Mapping, len(imageTable))
	for _, row := range imageTable {
		out[row.Repository] = importer.Mapping{
			Component: row.Component,
			Category:  categoryOf(row.Component),
			Traits:    row.Traits,
			Vendor:    row.Component[strings.LastIndexByte(row.Component, '/')+1:],
		}
	}
	return out
}

// categoryOf returns the category segment of a <namespace>/<category>/<name>
// Component name, empty for a name of another shape.
func categoryOf(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}
