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
// THREE things express that pair: the component identity every resource the
// attachment emits is stamped with, the directory the attachment's files are
// written to, and the dispatch identity its group holds its contribution under.
// Three expressions of one fact drift, so all of them are derived from this
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

// attachmentNodePrefix marks a synthetic per-node key that carries a
// component-scope attachment's contribution rather than a component's own
// render. It is declared once so the key's construction and every reader of it
// agree, and it is spelled apart from the boundary-scope marker because the two
// name different kinds of contributor and a group can hold both.
const attachmentNodePrefix = "<attachment>:"

// dispatchNodeID is the identity the attachment's dispatch group holds its
// contribution under: a marker saying what kind of contributor it is, followed by
// both authored coordinates.
//
// Both coordinates are present because either alone identifies the wrong thing.
// The instance name alone is shared by every component the same feature is
// attached to, and those components can sit in one dispatch group — so an
// identity without the host arrives at that group as one contributor where
// several were declared, and all but one are lost. The host alone is shared by
// every attachment on that component.
//
// 🔴 It is a marker for the compiler's own dispatch and is NEVER a name a target
// accepts: it opens with an angle bracket and joins with a colon and an at sign,
// which a path refuses on some platforms and a name alphabet refuses outright.
// Nothing reads it back apart either — it joins its two parts with characters
// either part may itself contain, so cutting it cannot return what went in. What
// a target's own naming reads instead is the basis carried beside the
// contribution, and what a reader meets is the coordinates recorded beside it.
func (a attachmentIdentity) dispatchNodeID() string {
	return attachmentNodePrefix + a.host + "@" + a.instance
}
