// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

// secretVolumeName returns the name of the pod volume a container attachment
// delivered `as: file` is mounted from: the workload's naming basis,
// "-secret-", and the slot the entry reads.
//
// A pod volume name is an RFC 1123 label — lowercase letters, digits and
// hyphens — and the API server refuses a pod declaring any other. So the slot
// is written through [releaseNameEncoding], the encoding this provider already
// writes a release name with, rather than appended as it arrives: a lowercase
// letter or a digit is carried through as itself, an authored hyphen is kept
// where the encoding can keep it, and every other character is escaped. The
// slot `server_certificate` reaches the volume as `server-zucertificate`, a slot
// holding a capital has the capital written out, and a slot read through one of
// the component's connections — the connection's alias, a dot, and the slot its
// socket declares — has its dot written out, so `gpt.credential` reaches the
// volume as `gpt-zdcredential`.
//
// The encoding is injective and it is applied to every slot, and that is what
// keeps two entries apart. Choosing by what a slot holds would carry an
// authored `a-zdb` through as itself, onto the volume name a connection's `a.b`
// is written to — and the pod declares one volume per name, so the second entry
// would mount the first one's Secret. Written through the encoding, the
// authored hyphen before `z` is escaped and the two reach two volumes.
//
// The length is not judged here. A basis, "-secret-" and a long slot can
// together pass the 63 characters a label allows, and how long a slot may be is
// the author's own budget against that limit.
//
// The volume mount and the pod volume both read the name here, so the two
// cannot spell it differently.
func secretVolumeName(basis, slot string) string {
	return basis + "-secret-" + releaseNameEncoding.Segment(slot)
}
