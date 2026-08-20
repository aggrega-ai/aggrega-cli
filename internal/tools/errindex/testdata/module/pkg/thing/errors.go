package thing

import cerrors "example.test/fixture/pkg/errors"

func ErrPlain() error { return errPlain }

var errPlain = cerrors.Register("thing/plain", cerrors.Meta{
	ShortCode: "E9001",
	Message:   "a plain condition",
})

// A message quoting authoring syntax, so the rendering is exercised against a
// backtick it has to fence around rather than only against prose.
func ErrQuoted() error { return errQuoted }

var errQuoted = cerrors.Register("thing/quoted", cerrors.Meta{
	ShortCode: "E9002",
	Message:   "the `uses:` block is malformed",
})

// A registered condition published under a name that is not an identity. The
// registration is real and the accessor is exported, so what keeps it out of the
// index is the naming convention alone — which is what this fixture exists to
// hold the scan to. It must not reach the index.
func NotAnIdentity() error { return notAnIdentity }

var notAnIdentity = cerrors.Register("thing/not-an-identity", cerrors.Meta{
	ShortCode: "E9003",
	Message:   "not an identity",
})
