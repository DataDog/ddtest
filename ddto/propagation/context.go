// Package propagation exchanges trace identity without importing a tracing SDK.
package propagation

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Context is an immutable value identifying the active span. TraceID contains
// all 128 bits, including when the Datadog wire format uses a decimal low half.
// Priority is propagation metadata; this package makes no sampling decisions.
type Context struct {
	TraceID    [16]byte
	SpanID     uint64
	Sampled    bool
	Priority   int8
	Origin     string
	TraceState string
}

// Valid reports whether the identifiers meet the W3C nonzero requirements.
func (c Context) Valid() bool { return c.TraceID != [16]byte{} && c.SpanID != 0 }

// New creates a recorded root context with a unique trace and span identifier.
// Generated span IDs use 63 bits, like dd-trace-go, so consumers that read IDs
// as signed 64-bit integers stay positive. As in the SDK, the root span ID is
// the low half of the trace ID.
func New() (Context, error) {
	var c Context
	if _, err := rand.Read(c.TraceID[:]); err != nil {
		return c, err
	}
	c.TraceID[8] &= 0x7f
	c.SpanID = binary.BigEndian.Uint64(c.TraceID[8:])
	if !c.Valid() {
		return New()
	}
	c.Sampled = true
	c.Priority = 2
	c.Origin = "ciapp-test"
	return c, nil
}

// Child retains trace identity and replaces the active span identifier.
func (c Context) Child() (Context, error) {
	if !c.Valid() {
		return Context{}, errors.New("invalid parent trace context")
	}
	var id [8]byte
	for binary.BigEndian.Uint64(id[:]) == 0 || binary.BigEndian.Uint64(id[:]) == c.SpanID {
		if _, err := rand.Read(id[:]); err != nil {
			return Context{}, err
		}
		id[0] &= 0x7f // 63 bits, like dd-trace-go span IDs.
	}
	c.SpanID = binary.BigEndian.Uint64(id[:])
	return c, nil
}

type contextKey struct{}

// WithContext stores a value, preserving the caller's cancellation and values.
func WithContext(ctx context.Context, c Context) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

// FromContext returns a valid active trace context, if one has been installed.
func FromContext(ctx context.Context) (Context, bool) {
	if ctx == nil {
		return Context{}, false
	}
	c, ok := ctx.Value(contextKey{}).(Context)
	return c, ok && c.Valid()
}

// Reader and Writer accept http.Header and other user-owned carriers.
type Reader interface{ Get(string) string }
type Writer interface{ Set(string, string) }

// MapCarrier is a text carrier. Reads accept case-insensitive header names.
type MapCarrier map[string]string

func (c MapCarrier) Get(key string) string {
	if v, ok := c[key]; ok {
		return v
	}
	for k, v := range c {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
func (c MapCarrier) Set(key, value string) { c[key] = value }

// Format selects one unambiguous propagation protocol.
type Format uint8

const (
	W3C Format = iota
	Datadog
)

// Inject exports the active span as the parent seen by the receiving tracer.
// It rejects invalid input before writing any header.
func Inject(c Context, w Writer, format Format) error {
	if !c.Valid() {
		return errors.New("invalid trace context")
	}
	if c.Priority < -1 || c.Priority > 2 {
		return errors.New("invalid sampling priority")
	}
	if strings.ContainsAny(c.Origin, "\r\n;,") {
		return errors.New("invalid trace origin")
	}
	switch format {
	case W3C:
		state, err := datadogState(c)
		if err != nil {
			return err
		}
		flags := "00"
		if c.Sampled {
			flags = "01"
		}
		w.Set("traceparent", fmt.Sprintf("00-%032x-%016x-%s", c.TraceID, c.SpanID, flags))
		w.Set("tracestate", state)
	case Datadog:
		low := binary.BigEndian.Uint64(c.TraceID[8:])
		if low == 0 {
			return errors.New("Datadog trace ID low half is zero")
		}
		w.Set("x-datadog-trace-id", strconv.FormatUint(low, 10))
		w.Set("x-datadog-parent-id", strconv.FormatUint(c.SpanID, 10))
		w.Set("x-datadog-sampling-priority", strconv.Itoa(int(c.Priority)))
		w.Set("x-datadog-origin", c.Origin)
		tags := ""
		if high := binary.BigEndian.Uint64(c.TraceID[:8]); high != 0 {
			tags = fmt.Sprintf("_dd.p.tid=%016x", high)
		}
		w.Set("x-datadog-tags", tags)
	default:
		return errors.New("unknown propagation format")
	}
	return nil
}

// Extract parses one protocol. Missing, malformed or ambiguous identifiers are
// errors; callers decide whether an invalid remote parent should start a root.
func Extract(r Reader, format Format) (Context, error) {
	var c Context
	switch format {
	case W3C:
		parent := r.Get("traceparent")
		if len(parent) < 55 || parent[2] != '-' || parent[35] != '-' || parent[52] != '-' {
			return c, errors.New("invalid traceparent")
		}
		version, err := strconv.ParseUint(parent[:2], 16, 8)
		if err != nil || version == 255 || version == 0 && len(parent) != 55 || len(parent) > 55 && parent[55] != '-' {
			return c, errors.New("invalid traceparent version")
		}
		if !lowerHex(parent[:2] + parent[3:35] + parent[36:52] + parent[53:55]) {
			return c, errors.New("invalid traceparent encoding")
		}
		if _, err = hex.Decode(c.TraceID[:], []byte(parent[3:35])); err != nil {
			return c, err
		}
		c.SpanID, err = strconv.ParseUint(parent[36:52], 16, 64)
		if err != nil {
			return c, err
		}
		flags, _ := strconv.ParseUint(parent[53:55], 16, 8)
		c.Sampled = flags&1 != 0
		if c.Sampled {
			c.Priority = 1
		} else {
			c.Priority = 0
		}
		state := r.Get("tracestate")
		if validState(state) {
			c.TraceState = state
			for _, member := range strings.Split(state, ",") {
				if !strings.HasPrefix(strings.TrimSpace(member), "dd=") {
					continue
				}
				for _, field := range strings.Split(strings.TrimSpace(member)[3:], ";") {
					key, value, ok := strings.Cut(field, ":")
					if !ok {
						continue
					}
					switch key {
					case "s":
						if n, e := strconv.ParseInt(value, 10, 8); e == nil && n >= -1 && n <= 2 {
							c.Priority = int8(n)
						}
					case "o":
						c.Origin = strings.ReplaceAll(value, "~", "=")
					}
				}
			}
		}
	case Datadog:
		low, err := parseDecimalID(r.Get("x-datadog-trace-id"))
		if err != nil {
			return c, err
		}
		c.SpanID, err = parseDecimalID(r.Get("x-datadog-parent-id"))
		if err != nil {
			return c, err
		}
		binary.BigEndian.PutUint64(c.TraceID[8:], low)
		if priority := r.Get("x-datadog-sampling-priority"); priority != "" {
			n, e := strconv.ParseInt(priority, 10, 8)
			if e != nil || n < -1 || n > 2 {
				return Context{}, errors.New("invalid sampling priority")
			}
			c.Priority = int8(n)
		}
		c.Sampled = c.Priority > 0
		c.Origin = r.Get("x-datadog-origin")
		if strings.ContainsAny(c.Origin, "\r\n") {
			return Context{}, errors.New("invalid origin")
		}
		seen := false
		for _, tag := range strings.Split(r.Get("x-datadog-tags"), ",") {
			key, val, ok := strings.Cut(tag, "=")
			if !ok || key != "_dd.p.tid" {
				continue
			}
			if seen || len(val) != 16 || !lowerHex(val) {
				return Context{}, errors.New("invalid high trace ID")
			}
			seen = true
			if _, err = hex.Decode(c.TraceID[:8], []byte(val)); err != nil {
				return Context{}, err
			}
		}
	default:
		return c, errors.New("unknown propagation format")
	}
	if !c.Valid() {
		return Context{}, errors.New("zero trace or span identifier")
	}
	return c, nil
}
func parseDecimalID(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("missing Datadog identifier")
	}
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			return 0, errors.New("invalid Datadog identifier")
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, errors.New("invalid Datadog identifier")
	}
	return n, nil
}
func lowerHex(s string) bool {
	for _, b := range []byte(s) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}
func validState(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 512 {
		return false
	}
	members := strings.Split(s, ",")
	if len(members) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, member := range members {
		key, value, ok := strings.Cut(strings.TrimSpace(member), "=")
		if !ok || key == "" || value == "" || len(key) > 256 || len(value) > 256 || seen[key] {
			return false
		}
		seen[key] = true
		if strings.ContainsAny(value, "=\r\n") || value[len(value)-1] == ' ' {
			return false
		}
		for _, b := range []byte(value) {
			if b < 32 || b > 126 {
				return false
			}
		}
		parts := strings.Split(key, "@")
		if len(parts) > 2 {
			return false
		}
		for i, part := range parts {
			if part == "" || len(parts) == 2 && (i == 0 && len(part) > 241 || i == 1 && len(part) > 14) {
				return false
			}
			if !(part[0] >= 'a' && part[0] <= 'z' || i == 0 && len(parts) == 2 && part[0] >= '0' && part[0] <= '9') {
				return false
			}
			for _, b := range []byte(part) {
				if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.ContainsRune("_-*/", rune(b))) {
					return false
				}
			}
		}
	}
	return true
}
func datadogState(c Context) (string, error) {
	if !validState(c.TraceState) {
		return "", errors.New("invalid tracestate")
	}
	if c.Priority < -1 || c.Priority > 2 {
		return "", errors.New("invalid sampling priority")
	}
	dd := "dd=s:" + strconv.Itoa(int(c.Priority))
	if c.Origin != "" {
		dd += ";o:" + strings.ReplaceAll(c.Origin, "=", "~")
	}
	// Preserve unknown Datadog fields from an extracted W3C context. Update
	// identity fields explicitly rather than dropping another tracer's metadata.
	for _, member := range strings.Split(c.TraceState, ",") {
		member = strings.TrimSpace(member)
		if !strings.HasPrefix(member, "dd=") {
			continue
		}
		for _, field := range strings.Split(member[3:], ";") {
			key, _, ok := strings.Cut(field, ":")
			if ok && key != "s" && key != "o" && key != "p" && key != "t.tid" {
				dd += ";" + field
			}
		}
	}
	members := []string{dd}
	for _, member := range strings.Split(c.TraceState, ",") {
		member = strings.TrimSpace(member)
		if member != "" && !strings.HasPrefix(member, "dd=") {
			members = append(members, member)
		}
	}
	if len(members) > 32 {
		members = members[:32]
	}
	for len(strings.Join(members, ",")) > 512 && len(members) > 1 {
		members = members[:len(members)-1]
	}
	out := strings.Join(members, ",")
	if !validState(out) {
		return "", errors.New("invalid Datadog tracestate")
	}
	return out, nil
}
