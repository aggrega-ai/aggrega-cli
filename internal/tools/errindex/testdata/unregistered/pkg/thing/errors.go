package thing

import cerrors "example.test/unregistered/pkg/errors"

// Declared with no registration beside it. A caller matching this identity would
// read text naming the unregistered code rather than a description, so the page
// has nothing truthful to render for it.
const ErrOrphan cerrors.Code = "thing/orphan"
