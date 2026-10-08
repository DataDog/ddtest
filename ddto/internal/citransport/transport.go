// Package citransport sends native CI Visibility payloads without an APM SDK.
package citransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// TestCycleMaxPayloadBytes and TestCycleFlushBytes match the CI Visibility
// writer in dd-trace-go. The limit applies to uncompressed MessagePack, including
// the envelope; gzip does not make an oversized intake payload acceptable.
const (
	TestCycleMaxPayloadBytes = 5 * 1024 * 1024
	TestCycleFlushBytes      = TestCycleMaxPayloadBytes / 2
)

// Config contains only the destination and delivery policy. Endpoint is the
// complete test-cycle URL. Credentials never appear in returned errors.
type Config struct {
	Endpoint   string
	Agentless  bool
	APIKey     string
	Version    string
	HTTPClient *http.Client
	Attempts   int
	RetryDelay time.Duration
	// CloseIdleAfterSend releases delivery workers before the next test. Standard
	// HTTP transports are cloned so this cannot close the caller's connections.
	// A custom RoundTripper retains its own connection ownership policy.
	CloseIdleAfterSend bool
	// MaxIdleConnsPerHost raises a cloned standard transport's idle connection
	// limit (net/http keeps two by default) for concurrent senders. A larger
	// configured value is kept; a custom RoundTripper is unchanged.
	MaxIdleConnsPerHost int
}
type Transport struct {
	config  Config
	client  *http.Client
	secrets []string
}

// New validates configuration before any event can be accepted.
func New(c Config) (*Transport, error) {
	u, err := url.Parse(c.Endpoint)
	if !bazel.IsPayloadFilesModeEnabled() && (err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil) {
		return nil, errors.New("invalid CI Visibility endpoint")
	}
	if !bazel.IsPayloadFilesModeEnabled() && c.Agentless && c.APIKey == "" {
		return nil, errors.New("agentless CI Visibility requires an API key")
	}
	if c.Attempts == 0 {
		c.Attempts = 3
	}
	if c.Attempts < 1 || c.Attempts > 10 {
		return nil, errors.New("invalid delivery attempts")
	}
	if c.RetryDelay == 0 {
		c.RetryDelay = 100 * time.Millisecond
	}
	if c.RetryDelay < 0 {
		return nil, errors.New("invalid retry delay")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// A copy preserves the caller's client. Never forward API credentials through
	// redirects, including custom headers not protected by net/http's defaults.
	copyClient := *client
	// This client owns standard transports even in ordinary delivery mode.
	// A goleak checkpoint must not close a caller's unrelated HTTP connections.
	transport := copyClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		clone := standard.Clone()
		limit := clone.MaxIdleConnsPerHost
		if limit == 0 {
			limit = http.DefaultMaxIdleConnsPerHost
		}
		if c.MaxIdleConnsPerHost > limit {
			clone.MaxIdleConnsPerHost = c.MaxIdleConnsPerHost
		}
		copyClient.Transport = clone
	} else {
		copyClient.Transport = transport
	}
	if copyClient.Timeout <= 0 {
		copyClient.Timeout = 10 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t := &Transport{config: c, client: &copyClient}
	if c.APIKey != "" {
		t.secrets = append(t.secrets, c.APIKey)
	}
	if u != nil {
		for _, values := range u.Query() {
			for _, value := range values {
				if value != "" {
					t.secrets = append(t.secrets, value, url.QueryEscape(value))
				}
			}
		}
		if u.Fragment != "" {
			t.secrets = append(t.secrets, u.Fragment)
		}
	}
	return t, nil
}

// Send retries transient failures using the same immutable payload. Context
// cancellation bounds both requests and backoff; permanent 4xx responses fail.
func (t *Transport) Send(ctx context.Context, payload []byte) (sendErr error) {
	defer func() { sendErr = t.safeError(sendErr) }()
	attempts := 0
	mode := "agent"
	if t.config.Agentless {
		mode = "agentless"
	}
	if log.DebugEnabled() {
		started := time.Now()
		log.Debug("test-cycle: send started payload_bytes=%d", len(payload))
		// Registered first so the duration includes admission, retries, body
		// ownership release and any connection cleanup, even on early returns.
		defer func() {
			status := "ok"
			if sendErr != nil {
				status = "error"
				if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
					status = "canceled"
				}
			}
			log.Debug("test-cycle: send finished duration=%s mode=%s payload_bytes=%d attempts=%d status=%s", time.Since(started), mode, len(payload), attempts, status)
		}()
	}
	if err := cidelivery.BeginSendContext(ctx); err != nil {
		return err
	}
	defer cidelivery.EndSend()
	if t.config.CloseIdleAfterSend {
		defer t.client.CloseIdleConnections()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	compression := telemetry.UncompressedRequestCompressedType
	if t.config.Agentless {
		compression = telemetry.CompressedRequestCompressedType
	}
	telemetry.EndpointPayloadRequests(telemetry.TestCycleEndpointType, compression)
	if len(payload) > TestCycleMaxPayloadBytes {
		return errors.New("CI test-cycle payload exceeds the intake limit")
	}
	if bazel.IsPayloadFilesModeEnabled() {
		mode = "files"
		data, err := bazel.MsgpackToJSON(payload)
		if err != nil {
			return err
		}
		return bazel.WritePayloadFile(bazel.PayloadKindTests, data)
	}
	body := payload
	if t.config.Agentless {
		compressor := gzipCompressors.Get().(*gzipCompressor)
		defer releaseCompressor(compressor)
		compressor.buffer.Reset()
		compressor.writer.Reset(&compressor.buffer)
		if _, err := compressor.writer.Write(payload); err != nil {
			return err
		}
		if err := compressor.writer.Close(); err != nil {
			return err
		}
		body = compressor.buffer.Bytes()
	}
	// net/http may close request bodies asynchronously, even after Do returns.
	// Seal every reader before returning the payload or compressor to its owner.
	owner := &bodyOwner{}
	defer owner.seal()
	var last error
	for attempt := 0; attempt < t.config.Attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := t.newRequest(ctx, body, owner)
		if err != nil {
			return err
		}
		requestStart := time.Now()
		attempts++
		resp, err := t.client.Do(req)
		telemetry.EndpointPayloadRequestsMs(telemetry.TestCycleEndpointType, float64(time.Since(requestStart).Milliseconds()))
		retry := false
		if err != nil {
			telemetry.EndpointPayloadRequestsErrors(telemetry.TestCycleEndpointType, telemetry.NetworkErrorType)
			last = fmt.Errorf("CI Visibility request failed: %w", safeRequestError(err))
			retry = true
		} else {
			// Like the SDK, a failure reports up to 1000 bytes of the response.
			var detail []byte
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				detail, _ = io.ReadAll(io.LimitReader(resp.Body, 1000))
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				logTestCycleRequest(req, resp, false, requestStart, attempts, false, len(body))
				return nil
			}
			telemetry.EndpointPayloadRequestsErrors(telemetry.TestCycleEndpointType, telemetry.GetErrorTypeFromStatusCode(resp.StatusCode))
			last = statusError(resp.StatusCode, detail)
			retry = resp.StatusCode == 429 || resp.StatusCode >= 500
		}
		logTestCycleRequest(req, resp, err != nil, requestStart, attempts, retry && attempt+1 < t.config.Attempts, len(body))
		if err == nil && resp.StatusCode == 429 && attempt+1 < t.config.Attempts {
			if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds >= 0 && seconds <= 60 {
				if err = wait(ctx, time.Duration(seconds)*time.Second); err != nil {
					return err
				}
				continue
			}
		}
		if !retry || attempt+1 == t.config.Attempts {
			return last
		}
		if err = wait(ctx, t.config.RetryDelay*time.Duration(1<<attempt)); err != nil {
			return err
		}
	}
	return last
}

// Request duration ends after response consumption and close, before backoff.
// The request metric above still measures arrival of headers, like the SDK.
func logTestCycleRequest(req *http.Request, resp *http.Response, networkError bool, started time.Time, attempt int, retry bool, bodyBytes int) {
	if !log.DebugEnabled() {
		return
	}
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	// Omit query strings, headers, bodies and raw errors: they can contain
	// credentials. Host/path still distinguish Agent, intake and staging routes.
	log.Debug("test-cycle: request finished host=%s path=%s attempt=%d duration=%s status_code=%d network_error=%t retry=%t body_bytes=%d gzip=%t", req.URL.Host, req.URL.EscapedPath(), attempt, time.Since(started), statusCode, networkError, retry, bodyBytes, req.Header.Get("Content-Encoding") == "gzip")
}

func statusError(code int, body []byte) error {
	if text := strings.TrimSpace(string(body)); text != "" {
		return fmt.Errorf("CI Visibility endpoint returned HTTP %d: %s (Status: %s)", code, text, http.StatusText(code))
	}
	return fmt.Errorf("CI Visibility endpoint returned HTTP %d (Status: %s)", code, http.StatusText(code))
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// CloseIdleConnections releases this transport's idle connections after flush.
func (t *Transport) CloseIdleConnections() { t.client.CloseIdleConnections() }

// CloseOwnedIdleConnections is narrower than explicit client shutdown. A goleak
// checkpoint owns cloned standard transports, but must leave a caller's custom
// RoundTripper and its unrelated workers under that caller's control.
func (t *Transport) CloseOwnedIdleConnections() {
	if standard, ok := t.client.Transport.(*http.Transport); ok {
		standard.CloseIdleConnections()
	}
}
