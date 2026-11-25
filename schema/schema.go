// SPDX-License-Identifier: MIT

package schema

import (
	_ "embed"
)

// EmbeddedSchema contains the raw bytes of the lmgoogle DSL schema.
//
//go:embed lmgoogle.dsl.json
var EmbeddedSchema []byte
