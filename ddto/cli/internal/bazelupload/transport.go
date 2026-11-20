package bazelupload

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type endpoints struct {
	test, coverage, telemetry string
	agentless                 bool
}

func resolveEndpoints(options Options) (endpoints, error) {
	site := strings.ToLower(strings.TrimSpace(options.Site))
	if site == "" {
		site = "datadoghq.com"
	}
	if _, host, ok := strings.Cut(site, "://"); ok {
		site = host
	}
	if end := strings.IndexAny(site, "/?#"); end >= 0 {
		site = site[:end]
	}
	site = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(site, "app."), "api."))
	if valid, _ := regexp.MatchString(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`, site); !valid {
		return endpoints{}, fmt.Errorf("DD_SITE must be a hostname")
	}
	if options.AgentURL == "" && options.AgentlessURL == "" {
		return endpoints{
			test:      "https://citestcycle-intake." + site + "/api/v2/citestcycle",
			coverage:  "https://citestcov-intake." + site + "/api/v2/citestcov",
			telemetry: "https://instrumentation-telemetry-intake." + site + "/api/v2/apmtelemetry",
			agentless: true,
		}, nil
	}
	name := "DD_TEST_OPTIMIZATION_AGENTLESS_URL"
	base := options.AgentlessURL
	agentless := options.AgentURL == ""
	if !agentless {
		name = "DD_TEST_OPTIMIZATION_AGENT_URL"
		base = options.AgentURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return endpoints{}, fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials, query or fragment", name)
	}
	base = strings.TrimRight(base, "/")
	evp, telemetry := "", ""
	if !agentless {
		evp, telemetry = "/evp_proxy/v2", "/telemetry/proxy"
	}
	return endpoints{
		test:      base + evp + "/api/v2/citestcycle",
		coverage:  base + evp + "/api/v2/citestcov",
		telemetry: base + telemetry + "/api/v2/apmtelemetry",
		agentless: agentless,
	}, nil
}

// The retry policy matches the rules uploader: four attempts, 408, 429 and
// 5xx responses retried, and Retry-After honored up to a minute.
const (
	maxAttempts    = 4
	requestTimeout = 60 * time.Second
	retryDelay     = 2 * time.Second
	maxRetryDelay  = 60 * time.Second
	// A rejection can echo the payload: read only enough to reuse the connection.
	responseDrainLimit = 64 << 10
)

type outbound struct {
	body    []byte
	url     string
	headers http.Header
	gzip    bool
}

// delivery is the outcome of one prepared request, retries included.
type delivery struct {
	Attempts int
	Status   int
	Bytes    int
	Success  bool
	Err      error
}

type sender struct {
	client   *http.Client
	attempts int
	dryRun   bool
}

func newSender(options Options, concurrency int) sender {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = concurrency
	transport.MaxIdleConnsPerHost = concurrency
	transport.DisableKeepAlives = !options.KeepAlive
	client := &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return sender{client: client, attempts: maxAttempts, dryRun: options.DryRun}
}

func (send sender) post(ctx context.Context, request outbound, compressor *gzip.Writer) delivery {
	body := request.body
	if request.gzip {
		var compressed bytes.Buffer
		compressor.Reset(&compressed)
		_, writeErr := compressor.Write(body)
		closeErr := compressor.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return delivery{Err: fmt.Errorf("compress request: %w", err)}
		}
		request.headers.Set("Content-Encoding", "gzip")
		body, request.body = compressed.Bytes(), nil
	}
	outcome := delivery{Bytes: len(body)}
	if send.dryRun {
		return outcome
	}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			outcome.Err = err
			return outcome
		}
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, request.url, bytes.NewReader(body))
		if err != nil {
			outcome.Err = errors.New("prepare HTTP request")
			return outcome
		}
		httpRequest.Header = request.headers
		outcome.Attempts = attempt
		response, err := send.client.Do(httpRequest)
		retryAfter := ""
		if err != nil {
			// The error can name the URL; the report keeps only its kind.
			outcome.Status, outcome.Err = 0, errors.New("HTTP transport failed")
			var certificate *tls.CertificateVerificationError
			if errors.As(err, &certificate) {
				return outcome
			}
		} else {
			outcome.Status = response.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseDrainLimit))
			_ = response.Body.Close()
			if outcome.Status >= 200 && outcome.Status < 300 {
				outcome.Success, outcome.Err = true, nil
				return outcome
			}
			outcome.Err = fmt.Errorf("HTTP %d", outcome.Status)
			if outcome.Status != 408 && outcome.Status != 429 && (outcome.Status < 500 || outcome.Status > 599) {
				return outcome
			}
			retryAfter = response.Header.Get("Retry-After")
		}
		if attempt == send.attempts {
			return outcome
		}
		timer := time.NewTimer(retryWait(retryAfter))
		select {
		case <-ctx.Done():
			timer.Stop()
			outcome.Err = ctx.Err()
			return outcome
		case <-timer.C:
		}
	}
}

// retryWait reads Retry-After as seconds or an HTTP date, bounded to a minute.
func retryWait(header string) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.ParseUint(header, 10, 64); err == nil {
		return time.Duration(min(seconds, uint64(maxRetryDelay/time.Second))) * time.Second
	}
	if retryAt, err := http.ParseTime(header); err == nil {
		return min(max(0, time.Until(retryAt)), maxRetryDelay)
	}
	return retryDelay
}

func requestHeaders(plan Plan, global Object, apiKey, evp string) http.Header {
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("Content-Type", "application/json")
	headers.Set("Datadog-Meta-Lang", firstString(stringValue(global, "language"), "bazel-starlark"))
	headers.Set("Datadog-Meta-Lang-Version", firstString(stringValue(global, "language_version"), stringValue(global, "runtime_version"), "n/a"))
	headers.Set("Datadog-Meta-Lang-Interpreter", firstString(stringValue(global, "language_interpreter"), stringValue(global, "runtime_name"), "bazel-run"))
	headers.Set("Datadog-Meta-Tracer-Version", firstString(stringValue(global, "library_version"), plan.RulesVersion))
	if apiKey != "" {
		headers.Set("DD-API-KEY", apiKey)
	}
	if evp != "" {
		headers.Set("X-Datadog-EVP-Subdomain", evp)
	}
	return headers
}
