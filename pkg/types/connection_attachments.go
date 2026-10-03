// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "iter"

// ConnectionAttachments enumerates the attachments the component's own
// connections carry, each with the connection it was written on: the sockets of
// the component's `uses:` block in author order, then each socket's aliases, then
// the slots each connection fills. A socket written in the compact form names
// targets only, so it carries none.
//
// It is the one statement of which attachments a component's connections carry
// in the Aggregate. The lowering that makes each an attachment of the consuming
// component reads it, and so does the lock-time walk that pins the Feature each
// one names, so an attachment the build lowers from a connection is one the
// lockfile pins: the two read the same set rather than each walking the block in
// its own way.
func (ci ComponentInstance) ConnectionAttachments() iter.Seq2[AttachmentConnection, ConnectionAttachment] {
	return func(yield func(AttachmentConnection, ConnectionAttachment) bool) {
		for socket, sv := range ci.Uses.All() {
			for alias, entry := range sv.Verbose.All() {
				for slot, att := range entry.Features.All() {
					if !yield(AttachmentConnection{Socket: socket, Alias: alias, Attachment: slot}, att) {
						return
					}
				}
			}
		}
	}
}

// ConnectionAttachments enumerates the attachments a Release component entry
// puts on the component's connections, each with the connection it was written
// on, in the order [ComponentInstance.ConnectionAttachments] uses: socket, alias,
// slot, each in author order.
//
// It is the Release's counterpart of that statement, read by the same two
// consumers for the same reason: the collection that folds the Release's
// connection attachments into the consuming component, and the lock-time walk.
// An entry may carry no type — the sizing for an attachment the Aggregate
// declares on the same connection — and is enumerated all the same; what a
// consumer does with an empty type is its own.
func (bc BoundaryComponent) ConnectionAttachments() iter.Seq2[AttachmentConnection, ReleaseConnectionAttachment] {
	return func(yield func(AttachmentConnection, ReleaseConnectionAttachment) bool) {
		for socket, aliases := range bc.Uses.All() {
			for alias, entry := range aliases.All() {
				for slot, att := range entry.Features.All() {
					if !yield(AttachmentConnection{Socket: socket, Alias: alias, Attachment: slot}, att) {
						return
					}
				}
			}
		}
	}
}
