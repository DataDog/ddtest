package bazeldoctor

import "sync"

// parallelFor runs work for each index below n on up to workers goroutines.
// work must not stop the run: a step that may fail, fall back or be
// interrupted recovers and leaves that index to the sequential code.
func parallelFor(workers, n int, work func(int)) {
	indexes := make(chan int)
	var group sync.WaitGroup
	for range min(max(1, workers), max(1, n)) {
		group.Go(func() {
			for index := range indexes {
				work(index)
			}
		})
	}
	for index := range n {
		indexes <- index
	}
	close(indexes)
	group.Wait()
}

// quietly runs a step on a worker goroutine and reports whether it finished:
// a failure, fallback or interruption is left for the sequential code to
// raise in its own place.
func quietly(step func()) (finished bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			switch recovered.(type) {
			case failure, fallback, interrupted:
				finished = false
			default:
				panic(recovered)
			}
		}
	}()
	step()
	return true
}

// resolveAll fills the run's cache of resolved paths in parallel.
func (d *doctor) resolveAll(paths []string) {
	var pending []string
	for _, path := range paths {
		if _, done := d.cache.resolved[path]; !done {
			pending = append(pending, path)
		}
	}
	resolved := make([]string, len(pending))
	ok := make([]bool, len(pending))
	parallelFor(d.workers, len(pending), func(i int) {
		ok[i] = quietly(func() { resolved[i] = resolvePath(pending[i]) })
	})
	for i, path := range pending {
		if ok[i] {
			d.cache.resolved[path] = resolved[i]
		}
	}
}
