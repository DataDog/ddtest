package citransport

import (
	"bytes"
	"io"
	"sync"
)

// bodyOwner seals all initial/replay readers before their backing buffer is
// reused. net/http can keep reading after Do returns on an asynchronous error;
// the shared lock makes that lifetime boundary independent of Close timing.
type bodyOwner struct {
	mu     sync.Mutex
	sealed bool
}
type ownedBody struct {
	owner  *bodyOwner
	source *bytes.Reader
	closed bool
}

func (owner *bodyOwner) reader(data []byte) *ownedBody {
	return &ownedBody{owner: owner, source: bytes.NewReader(data)}
}
func (owner *bodyOwner) seal() { owner.mu.Lock(); owner.sealed = true; owner.mu.Unlock() }
func (body *ownedBody) Read(destination []byte) (int, error) {
	body.owner.mu.Lock()
	defer body.owner.mu.Unlock()
	if body.closed || body.owner.sealed {
		return 0, io.EOF
	}
	return body.source.Read(destination)
}
func (body *ownedBody) Close() error {
	body.owner.mu.Lock()
	body.closed = true
	body.owner.mu.Unlock()
	return nil
}
