// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import "github.com/aggrega-ai/aggrega-cli/pkg/labels"

// secretVolumeName returns the name of the pod volume a container attachment
// delivered `as: file` is mounted from: the workload's naming basis,
// "-secret-", and the slot the entry reads.
//
// The slot is written through [labels.EncodeNodeIdentity], the encoding a
// component's own identity takes on its way into the naming basis, never
// appended as it arrives. A slot read through one of the component's
// connections arrives as the name the compiler composes for it — the
// connection's alias, a dot, and the slot its socket declares — and the dot is
// the one character that encoding writes out, so the slot `gpt.credential`
// reaches the volume as `gpt-zdcredential`, the spelling the attachment's
// identity label and emitted file names carry. A slot the component declares
// for its own attachments comes back as written, apart from the authored hyphen
// forms the encoding escapes so that its output stays injective: a trailing
// hyphen, a doubled one, and one followed by `z`.
//
// The encoding is applied to every slot rather than only to a composed one,
// and that is what keeps two entries apart. Choosing by whether the slot holds
// a dot would carry an authored `a-zdb` through as itself, onto the volume name
// a connection's `a.b` is written to — and the pod declares one volume per
// name, so the second entry would mount the first one's Secret.
//
// It writes out the dot and nothing else. An underscore or an uppercase letter
// an authored slot carries reaches the volume name as written.
//
// The volume mount and the pod volume both read the name here, so the two
// cannot spell it differently.
func secretVolumeName(basis, slot string) string {
	return basis + "-secret-" + labels.EncodeNodeIdentity(slot)
}
