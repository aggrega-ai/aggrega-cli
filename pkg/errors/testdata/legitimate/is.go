// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package legitimate

import "errors"

func stdIs(err, target error) bool { return errors.Is(err, target) }
