// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package tfaddr reads Terraform module source strings into their kind and parts, and maps public registry addresses
// to the GitHub repositories their modules are published from, and back.
package tfaddr

import (
	"net/url"
	"strings"
)

// Kind classifies a module source string by the way its module is fetched.
type Kind int

const (
	// Local is a directory relative to the calling module, written with a leading "./" or "../".
	Local Kind = iota
	// Registry is an address in the public module registry, written with or without the registry host.
	Registry
	// Private is a module address on a registry host other than the public registry.
	Private
	// Git is a module fetched from a git repository.
	Git
	// OCI is a module fetched from an OCI registry.
	OCI
	// Other is any other source form, such as an archive, an object-storage bucket or a Mercurial repository.
	Other
)

// String returns the kind's name.
func (k Kind) String() string {
	switch k {
	case Local:
		return "Local"
	case Registry:
		return "Registry"
	case Private:
		return "Private"
	case Git:
		return "Git"
	case OCI:
		return "OCI"
	case Other:
		return "Other"
	}
	return "Kind(unknown)"
}

// Address is a module source string read into its parts.
type Address struct {
	Kind                      Kind
	Raw                       string // the source as written
	Host                      string // the registry host as written; empty for the public registry written without one
	Namespace, Name, Provider string // Registry and Private: the namespace after Aliases applies, the name and the provider
	Subdir                    string // the path after "//", naming a directory inside the module
	URL                       string // Git and OCI: the location without the subdirectory and the query
	Ref                       string // Git: the ?ref= value; OCI: the ?tag= or ?digest= value
}

// Alias maps a registry module to the organisation and name its repository is published under. It names a whole
// module, not a namespace: a namespace can hold modules published under different organisations.
type Alias struct {
	From, To string // each is "<namespace>/<name>"
}

// Aliases lists the registry modules whose addresses read under another namespace. Parse applies the first match in
// order. The mapping reads one way: a repository under the To organisation keys as the To address, and a From address
// is read as its To address.
var Aliases = []Alias{{From: "GoogleCloudPlatform/sql-db", To: "terraform-google-modules/sql-db"}}

// Parse reads a module source string into its kind and parts. It never fails: a source it does not recognise is Other.
func Parse(source string) Address {
	a := Address{Kind: Other, Raw: source}
	switch {
	case strings.HasPrefix(source, "./"), strings.HasPrefix(source, "../"):
		a.Kind = Local
	case strings.HasPrefix(source, "git::"):
		base, subdir, query := splitLocator(strings.TrimPrefix(source, "git::"))
		if base == "" {
			return a
		}
		a.Kind, a.URL, a.Subdir, a.Ref = Git, base, subdir, query.Get("ref")
	case strings.HasPrefix(source, "github.com/"):
		base, subdir, query := splitLocator(source)
		repo := strings.Split(strings.TrimPrefix(base, "github.com/"), "/")
		if len(repo) != 2 || repo[0] == "" || repo[1] == "" {
			return a
		}
		a.Kind, a.URL, a.Subdir, a.Ref = Git, "https://github.com/"+repo[0]+"/"+strings.TrimSuffix(repo[1], ".git")+".git", subdir, query.Get("ref")
	case strings.HasPrefix(source, "oci://"):
		base, subdir, query := splitLocator(source)
		a.Kind, a.URL, a.Subdir, a.Ref = OCI, base, subdir, query.Get("tag")
		if a.Ref == "" {
			a.Ref = query.Get("digest")
		}
	case isForcedGetter(source), strings.Contains(source, "://"):
		a.Kind = Other
	default:
		parseRegistry(&a, source)
	}
	return a
}

// parseRegistry fills a with a registry or private address, or leaves it Other when the source has no such shape.
func parseRegistry(a *Address, source string) {
	addr, subdir, _ := strings.Cut(source, "//")
	parts := strings.Split(addr, "/")
	var host string
	switch {
	case len(parts) == 3 && !strings.Contains(parts[0], "."):
		a.Kind = Registry
	case len(parts) == 4 && strings.Contains(parts[0], "."):
		host, parts = parts[0], parts[1:]
		a.Kind = Private
		if publicRegistry(host) {
			a.Kind = Registry
		}
	default:
		a.Kind = Other
		return
	}
	namespace, name, provider := parts[0], parts[1], parts[2]
	if namespace == "" || name == "" || provider == "" {
		a.Kind = Other
		return
	}
	a.Host, a.Subdir, a.Provider = host, subdir, provider
	if a.Kind == Registry {
		a.Namespace, a.Name = applyAlias(namespace, name)
	} else {
		a.Namespace, a.Name = namespace, name
	}
}

// publicRegistry reports whether host names the public module registry, which an address may write as a prefix.
func publicRegistry(host string) bool {
	return host == "registry.terraform.io" || host == "registry.opentofu.org"
}

// applyAlias returns the namespace and name a registry module is published under, from the first matching Aliases
// entry, or the given ones when none matches.
func applyAlias(namespace, name string) (string, string) {
	for _, alias := range Aliases {
		if alias.From == namespace+"/"+name {
			organisation, module, _ := strings.Cut(alias.To, "/")
			return organisation, module
		}
	}
	return namespace, name
}

// isForcedGetter reports whether source starts with a getter prefix such as "s3::", which names a fetch method.
func isForcedGetter(source string) bool {
	i := strings.Index(source, "::")
	return i > 0 && !strings.Contains(source[:i], "/")
}

// splitLocator separates a fetchable location into its base, the subdirectory written after "//" past the scheme, and
// the query after "?".
func splitLocator(locator string) (base, subdir string, query url.Values) {
	locator, rawQuery, _ := strings.Cut(locator, "?")
	query, _ = url.ParseQuery(rawQuery)
	start := 0
	if i := strings.Index(locator, "://"); i >= 0 {
		start = i + len("://")
	}
	if i := strings.Index(locator[start:], "//"); i >= 0 {
		return locator[:start+i], locator[start+i+len("//"):], query
	}
	return locator, "", query
}

// Key returns the address as a registry key, "<namespace>/<name>/<provider>" with the subdirectory appended after
// "//" when there is one. For any other kind it returns the source as written.
func (a Address) Key() string {
	if a.Kind != Registry {
		return a.Raw
	}
	key := a.Namespace + "/" + a.Name + "/" + a.Provider
	if a.Subdir != "" {
		key += "//" + a.Subdir
	}
	return key
}

// FromGitURL returns the registry key of a module published in a GitHub repository named terraform-<provider>-<name>,
// with the given subdirectory, and false for any other repository. The organisation becomes the namespace as written.
func FromGitURL(repoURL, subdir string) (string, bool) {
	rest, ok := strings.CutPrefix(repoURL, "https://github.com/")
	if !ok {
		return "", false
	}
	organisation, repo, ok := strings.Cut(rest, "/")
	if !ok || organisation == "" || strings.Contains(repo, "/") {
		return "", false
	}
	repo = strings.TrimSuffix(repo, ".git")
	moduleName, ok := strings.CutPrefix(repo, "terraform-")
	if !ok {
		return "", false
	}
	provider, name, ok := strings.Cut(moduleName, "-")
	if !ok || provider == "" || name == "" {
		return "", false
	}
	key := organisation + "/" + name + "/" + provider
	if subdir != "" {
		key += "//" + subdir
	}
	return key, true
}

// GitURL returns the repository a public registry address is published from, as an https URL ending in ".git". It
// returns false for any address that is not a public registry address.
func GitURL(a Address) (string, bool) {
	if a.Kind != Registry {
		return "", false
	}
	return "https://github.com/" + a.Namespace + "/terraform-" + a.Provider + "-" + a.Name + ".git", true
}
