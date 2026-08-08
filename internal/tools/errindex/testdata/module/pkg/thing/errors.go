package thing

import cerrors "example.test/fixture/pkg/errors"

const ErrPlain cerrors.Code = "thing/plain"

var _ = cerrors.Register(ErrPlain, cerrors.Meta{
	Message: "a plain condition",
})

// A message quoting authoring syntax, so the rendering is exercised against a
// backtick it has to fence around rather than only against prose.
const ErrQuoted cerrors.Code = "thing/quoted"

var _ = cerrors.Register(ErrQuoted, cerrors.Meta{
	Message: "the `uses:` block is malformed",
})

// A name that is not an identity. It must not reach the index.
const NotAnIdentity cerrors.Code = "thing/not-an-identity"

var _ = cerrors.Register(NotAnIdentity, cerrors.Meta{
	Message: "not an identity",
})
