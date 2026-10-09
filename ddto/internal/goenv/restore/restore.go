// Package restore applies goenv.Restore while a test process initializes.
// Only the test runtime imports it; ddto's build-tool helpers must not.
//
// Go initializes ready packages in import-path order. This package and goenv
// import only syscall, and this path sorts before "os", so it initializes
// before os and therefore before any package that can start a go command,
// including client dependencies that do not import testing.
package restore

import "github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"

func init() { goenv.Restore() }
