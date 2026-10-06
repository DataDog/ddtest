package minitracer

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// The testing hooks use this process-wide client. Explicit clients created with
// New own their configuration and lifetime independently.
var active atomic.Pointer[Client]

type StartOption func(*Config)

func WithService(v string) StartOption { return func(c *Config) { c.Service = v } }

// Start selects the runtime used by the extracted testing hooks.
func Start(options ...StartOption) {
	agentless := infra.BoolEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED", false)
	endpoint := ""
	var clientConfig citransport.Config
	if agentless {
		base := env.Get("DD_CIVISIBILITY_AGENTLESS_URL")
		if base == "" {
			site := env.Get("DD_SITE")
			if site == "" {
				site = "datadoghq.com"
			}
			base = "https://citestcycle-intake." + site
		}
		endpoint = strings.TrimRight(base, "/") + "/api/v2/citestcycle"
		clientConfig.Agentless = true
		clientConfig.APIKey = env.Get("DD_API_KEY")
	} else {
		agent := infra.AgentURLFromEnv()
		if agent.Scheme == "unix" {
			clientConfig.HTTPClient = infra.UDSClient(agent.Path, 10*time.Second)
			agent = infra.UnixDataSocketURL(agent.Path)
		}
		endpoint = strings.TrimRight(agent.String(), "/") + "/evp_proxy/v2/api/v2/citestcycle"
	}
	clientConfig.Endpoint = endpoint
	config := Config{Service: env.Get("DD_SERVICE"), Env: env.Get("DD_ENV"), ServiceVersion: env.Get("DD_VERSION"), Transport: clientConfig, Tags: infra.ParseTagString(env.Get("DD_TAGS"))}
	config.DeferUntilIdle = cidelivery.Enabled()
	for _, option := range options {
		option(&config)
	}
	if session, ok := utils.GetCITags()[constants.TestSessionName]; ok {
		config.Metadata = map[string]map[string]string{}
		for _, kind := range []string{"test", "test_session_end", "test_module_end", "test_suite_end"} {
			config.Metadata[kind] = map[string]string{"test_session.name": session}
		}
	}
	if config.Service == "" {
		config.Service = "go.test"
	}
	client, err := New(config)
	if err != nil {
		log.Error("CI mini tracer could not start: %s", err.Error())
		return
	}
	active.Store(client)
}
func StartSpanFromContext(ctx context.Context, name string, options ...StartSpanOption) (*Span, context.Context) {
	return newSpan(active.Load(), ctx, name, options...)
}
func Flush() {
	if c := active.Load(); c != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()
		if err := c.Flush(ctx); err != nil {
			log.Error("CI event flush failed: %s", err.Error())
		}
	}
}

// Stop closes the process-wide client. Like dd-trace-go's tracer.Stop, it
// flushes aggregated error logs: log.Error otherwise prints only after a
// minute, so failures and lost events reported at exit would never appear.
func Stop() {
	if c := active.Swap(nil); c != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			log.Error("CI event close failed: %s", err.Error())
		}
		if dropped := c.DroppedEvents(); dropped != 0 {
			log.Error("CI mini tracer lost %d events", dropped)
		}
	}
	log.Flush()
}

// EndpointForAgent constructs the native EVP endpoint from an agent base URL.
func EndpointForAgent(agent *url.URL) string {
	return strings.TrimRight(agent.String(), "/") + "/evp_proxy/v2/api/v2/citestcycle"
}
