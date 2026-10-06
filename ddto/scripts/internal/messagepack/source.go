// Package messagepack owns the pinned generator version and runtime import relocation.
package messagepack

import "bytes"

const Version = "v1.6.4"
const FwdVersion = "v1.2.0"

const RuntimeImport = "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
const FwdImport = "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/fwd"

// RelocateImports binds copied/generated code to the internal runtime subsets.
func RelocateImports(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("github.com/tinylib/msgp/msgp"), []byte(RuntimeImport))
	return bytes.ReplaceAll(data, []byte("github.com/philhofer/fwd"), []byte(FwdImport))
}
