// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraformcore

// SourceIgnorePatterns are the module ecosystem's own ignore patterns, scoped to
// the root of a module source: the version-control directories, and the working
// directory Terraform itself creates beside a module to hold the providers and
// modules it downloaded. The dependency lock file Terraform writes beside a module
// is not among them, because it is meant to travel with the module.
//
// It is data and nothing else: the host composes it with the author's own ignore
// file when an entity is pinned, records the result, and matches it.
func SourceIgnorePatterns() []string {
	return []string{".git/", ".hg/", ".svn/", ".bzr/", ".terraform/"}
}

// SourceIgnoreFiles names the module ecosystem's own ignore file, whose lines a
// module author writes to leave files out of what a remote run uploads. Wherever
// a source holds one, its lines refine the patterns above for the directory
// holding it.
func SourceIgnoreFiles() []string {
	return []string{".terraformignore"}
}
