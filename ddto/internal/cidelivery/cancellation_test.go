package cidelivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdleFlushCancellationWhileAdmissionIsHeld(t *testing.T) {
	var c Coordinator
	c.admission.Lock()
	defer c.admission.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.runIfIdle(ctx, func() error { t.Error("canceled callback ran"); return nil }) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("flush ignored cancellation while waiting for admission")
	}
}

func TestSendCancellationWhileLeakCheckIsActive(t *testing.T) {
	resume := PrepareLeakCheck()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- BeginSendContext(ctx) }()
	select {
	case err := <-done:
		resume()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		resume()
		<-done
		EndSend()
		t.Fatal("send ignored cancellation")
	}
	// A canceled waiter must not acquire admission after the check finishes.
	if err := BeginSendContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	EndSend()
	PrepareLeakCheck()()
}
