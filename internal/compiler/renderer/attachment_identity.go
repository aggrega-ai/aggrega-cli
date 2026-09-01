// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

// attachmentIdentity names the two authored parts a feature attached to ONE
// component is identified by: the identity of the component hosting it, and the
// name it is attached under.
//
// The host is a part rather than context a reader is expected to supply, because
// it is what separates one instance name attached to several components. Those
// attachments emit different resources and so need to be told apart, and an
// author who names them alike has written nothing wrong — a cascade that mints
// one instance name across many components by design gives an author no way to
// write anything else.
//
// # Why the parts are stated once, here
//
// TWO things express that pair: the component identity every resource the
// attachment emits is stamped with, and the directory the attachment's files are
// written to. Two expressions of one fact drift, so both are derived from this
// one value rather than each assembling the parts for itself. A caller building
// the directory by running the host and the instance together beside the
// identity composition would re-introduce exactly that drift — the identity and
// the path could then come to disagree about which attachment they name, with
// nothing comparing them.
//
// # The two derivations are written differently, and deliberately
//
// [attachmentIdentity.parts] feeds a composition that writes each part into the
// label alphabet, escaping whatever that alphabet refuses, because a label value
// admits a narrow character set and two parts differing at all must reach two
// values. [attachmentIdentity.outputPrefix] applies no such writing: a path
// segment is not a label value, every character an author may put in a component
// identity is legal in a directory name, and escaping one would leave the
// directory unreadable for no gain. So a host whose id carries a dot gives a
// directory carrying that dot as written.
//
// # The boundary-scope sibling keeps a different shape, by SCOPE
//
// A feature attached to a BOUNDARY rather than to a component writes its output
// under the instance name alone. That is not an inconsistency waiting to be
// unified: at boundary scope the enclosing directory already is the boundary, so
// naming a host would repeat what the location says — and such a feature has no
// host component to name in the first place. The difference is one of scope
// rather than of input kind, and the location is what disambiguates it.
type attachmentIdentity struct {
	host     string
	instance string
}

// named reports whether this value identifies an attachment at all.
//
// A render reached from a boundary-scope caller carries no host and no instance,
// because the identity its resources are stamped with is composed from the
// boundary path instead and is settled before that render begins.
func (a attachmentIdentity) named() bool { return a.instance != "" }

// parts returns the authored parts the attachment's composed component identity
// is assembled from, in the order the composition writes them.
func (a attachmentIdentity) parts() []string { return []string{a.host, a.instance} }

// outputPrefix returns the directory every file the attachment emits is written
// beneath: one segment naming the host, one naming the instance.
//
// Two segments rather than one composed token. They are collision-free for the
// same reason a composed identity is — the host is what separates one instance
// name attached to several components — while staying legible, which a composed
// token is not.
func (a attachmentIdentity) outputPrefix() string {
	return "features/" + a.host + "/" + a.instance + "/"
}
