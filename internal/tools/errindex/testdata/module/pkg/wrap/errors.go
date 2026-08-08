package wrap

import "example.test/fixture/internal/hidden"

// A re-export a reader CAN import whose origin a reader CANNOT. The row belongs
// on the page — the name here is matchable — but the origin must not be named
// beside it, because doing so would advertise a package Go makes unreachable.
func ErrHidden() error { return hidden.ErrHidden() }
