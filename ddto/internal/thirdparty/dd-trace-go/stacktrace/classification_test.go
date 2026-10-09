package stacktrace

import (
	"sync"
	"testing"
)

func TestConcurrentStackClassification(t *testing.T) {
	// Run concurrent first use, including raw capture that must not depend on
	// classification tables. The same rules govern stack filtering and redaction.
	cases := []struct {
		pkg  string
		want frameType
	}{
		{"github.com/tonyredondo/dd-ci-testing-poc/testopt", frameTypeDatadog},
		{"github.com/DataDog/dd-trace-go/v2/civisibility", frameTypeDatadog},
		{"runtime", frameTypeRuntime},
		{"net/http.test", frameTypeRuntime},
		{"github.com/stretchr/testify/suite", frameTypeThirdParty},
		{"golang.org/x/net/http2", frameTypeThirdParty},
		{"github.com/molecule-man/go-brrr", frameTypeThirdParty},
		{"github.com/opensearch-project/opensearch-go/v4/opensearchapi", frameTypeThirdParty},
		{"github.com/tidwall/sjson", frameTypeThirdParty},
		{"github.com/wI2L/jsondiff", frameTypeThirdParty},
		{"go.opentelemetry.io/otelc/pkg/inst", frameTypeThirdParty},
		{"github.com/opensearch-project/opensearch-go/v4-extra", frameTypeCustomer},
		{"github.com/stretchr/testify-extra", frameTypeCustomer},
		{"example.com/client", frameTypeCustomer},
		{"main", frameTypeCustomer},
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			<-start
			if len(CaptureRaw(0).PCs) == 0 {
				t.Error("raw capture is empty")
			}
			for _, tc := range cases {
				if got := classifySymbol(symbol{Package: tc.pkg}, internalSymbolPrefixes); got != tc.want {
					t.Errorf("classify %s = %s, want %s", tc.pkg, got, tc.want)
				}
			}
			if !internalPrefixTrie().HasPrefix("github.com/DataDog/dd-trace-go/v2/civisibility.Start") || internalPrefixTrie().HasPrefix("example.com/client.Test") {
				t.Error("internal stack filtering changed")
			}
		})
	}
	close(start)
	wg.Wait()
}
