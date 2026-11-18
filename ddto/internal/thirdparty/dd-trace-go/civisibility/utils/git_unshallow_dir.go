package utils

import (
	"sync"
)

// UnshallowGitRepositoryIn expands the shallow clone whose root is dir, as
// UnshallowGitRepository expands the working directory's repository. ddto's
// CLI calls it before a local TIA lookup, when go test's -C can name a
// checkout other than the CLI's working directory; the Git worker, which
// runs in that checkout, then finds the history and does not fetch again.
// The package's Git commands run in dir meanwhile, with dir as their safe
// directory: the caller runs no other Git command of this package then.
func UnshallowGitRepositoryIn(dir string) (bool, error) {
	// Resolve the working directory's values first, so that they are the
	// ones restored.
	getSafeDirectoryConfig()
	workingDirectory, safeDirectory := gitWorkingDirectory, safeDirectoryValue
	gitWorkingDirectory, safeDirectoryValue = dir, dir
	isAShallowCloneRepositoryOnce.Store(&sync.Once{})
	defer func() {
		gitWorkingDirectory, safeDirectoryValue = workingDirectory, safeDirectory
		isAShallowCloneRepositoryOnce.Store(&sync.Once{})
	}()
	return UnshallowGitRepository()
}
