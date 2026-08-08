// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package merger

// maxExtendsDepth bounds an extends chain. Mirrors the existing
// inheritance walker's bound at pkg/validator/inherit_engine.go's
// maxInheritanceDepth constant; the parallel discipline is documented
// in pkg/validator/inherit_engine.go's two-discipline rationale.
const maxExtendsDepth = 10

// ResolveChain walks the extends chain starting from child, using the
// convention that Schema.Extra["extends"] holds the parent URI as a
// string. Returns the chain in root-ancestor-first order so the
// caller can pass it directly to Merge.
//
// The walker runs two safeguards in parallel: a visited-set on URIs
// detects cycles (ErrCyclicExtension); a max-depth bound (10 levels)
// detects pathologically long linear chains (ErrMaxDepth). The two
// safeguards catch different pathologies — a 12-deep linear chain
// would slip past the visited-set; a 3-deep cycle would fire the
// visited-set check before the depth bound triggers.
//
// lookup resolves a parent URI to a *Schema. Returns (nil, false)
// when the URI cannot be resolved; ResolveChain returns a *MergeError
// wrapping ErrMergeInvalid for the missing parent case.
//
// A child with no Extra["extends"] entry (or whose entry is not a
// string) terminates the walk cleanly — the returned chain contains
// just the child.
func ResolveChain(child *Schema, lookup func(uri string) (*Schema, bool)) ([]*Schema, error) {
	if child == nil || child.Schema == nil {
		return nil, &MergeError{
			Detail: "ResolveChain: child schema is nil",
			Err:    ErrMergeInvalid(),
		}
	}

	// Build the chain child -> parent -> ... in walk order. The
	// returned slice will be reversed so the caller gets root-
	// ancestor first.
	chain := []*Schema{child}
	visited := map[string]bool{}
	if child.Source != "" {
		visited[child.Source] = true
	}

	current := child
	for {
		parentURI, ok := extendsURI(current)
		if !ok {
			break
		}

		if visited[parentURI] {
			return nil, &MergeError{
				Source: parentURI,
				Detail: "cyclic extends: " + parentURI + " already in chain",
				Err:    ErrCyclicExtension(),
			}
		}

		if len(chain) >= maxExtendsDepth {
			return nil, &MergeError{
				Source: parentURI,
				Detail: "extends chain exceeds maximum depth of " + itoa(maxExtendsDepth),
				Err:    ErrMaxDepth(),
			}
		}

		parent, found := lookup(parentURI)
		if !found {
			return nil, &MergeError{
				Source: parentURI,
				Detail: "parent " + parentURI + " not found via lookup",
				Err:    ErrMergeInvalid(),
			}
		}
		if parent == nil || parent.Schema == nil {
			return nil, &MergeError{
				Source: parentURI,
				Detail: "lookup returned a nil parent for " + parentURI,
				Err:    ErrMergeInvalid(),
			}
		}

		visited[parentURI] = true
		chain = append(chain, parent)
		current = parent
	}

	// Reverse so root-ancestor is first (the order Merge expects).
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// extendsURI reads the parent URI from Schema.Extra["extends"]. Returns
// (uri, true) on a string-typed entry; (_, false) when the key is
// absent, the value is nil, or the value is not a string.
func extendsURI(s *Schema) (string, bool) {
	if s == nil || s.Schema == nil || s.Extra == nil {
		return "", false
	}
	raw, ok := s.Extra["extends"]
	if !ok {
		return "", false
	}
	str, ok := raw.(string)
	if !ok || str == "" {
		return "", false
	}
	return str, true
}
