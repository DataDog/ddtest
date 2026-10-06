package cidelivery

import (
	"context"
	"sync"
)

// contextMutex is a zero-value-ready admission token. Waiting callers can
// cancel without starting a goroutine that might acquire the token later.
type contextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *contextMutex) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.once.Do(func() { m.token = make(chan struct{}, 1) })
	select {
	case m.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *contextMutex) Lock()   { _ = m.lock(context.Background()) }
func (m *contextMutex) Unlock() { <-m.token }
