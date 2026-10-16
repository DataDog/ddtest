// Package propctx holds the context key of package propagation, so that Mini
// can attach a span's identity to a context in one allocation.
package propctx

import "context"

// Key is the context key of the active trace identity.
type Key struct{}

// Carrier is a context that carries a value under Key, like
// context.WithValue. Value returns a pointer to the value, which boxes
// nothing: context.WithValue allocates the context and the boxed value.
type Carrier[T any] struct {
	context.Context
	Carried T
}

// Value returns &c.Carried for Key and asks the parent context otherwise.
func (c *Carrier[T]) Value(key any) any {
	if _, ok := key.(Key); ok {
		return &c.Carried
	}
	return c.Context.Value(key)
}
