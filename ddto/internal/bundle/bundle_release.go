//go:build ddto_release

package bundle

import _ "embed"

// scripts/release supplies both files through -overlay.

//go:embed runtime.zip
var runtime []byte

//go:embed toolwrap
var toolWrapper []byte
