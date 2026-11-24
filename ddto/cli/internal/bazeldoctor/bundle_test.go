package bazeldoctor

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// The expected texts are create_support_bundle.redact_text's.
func TestRedactText(t *testing.T) {
	cases := [][2]string{
		{"plain text", "plain text"},
		{"DD_API_KEY=abc", "DD_API_KEY=<redacted>"},
		{"x DD_API_KEY=abc def OTHER=1", "x DD_API_KEY=<redacted> OTHER=1"},
		{"--test_env=MY_TOKEN=xyz --test_env=B=2", "--test_env=MY_TOKEN=<redacted> --test_env=B=2"},
		{"--TEST_ENV=SECRET_THING=1,2,3;NEXT=4", "--TEST_ENV=SECRET_THING=<redacted>;NEXT=4"},
		{"a,PASSWORD=p w;q=1", "a,PASSWORD=<redacted>;q=1"},
		{"TOKEN=abc\nfoo", "TOKEN=abc\nfoo"},
		{"TOKEN=abc\n", "TOKEN=<redacted>\n"},
		{"TOKEN=abc\nNEXT=1", "TOKEN=<redacted>\nNEXT=1"},
		{"line1\nTOKEN=abc def", "line1\nTOKEN=<redacted>"},
		{"api-key=1 apikey=2 my_api_key=3", "api-key=1 apikey=<redacted> my_api_key=<redacted>"},
		{"AUTHORIZATION=Bearer abc, credential=x", "AUTHORIZATION=<redacted> credential=<redacted>"},
		{"TOKEN==x", "TOKEN=<redacted>"},
		{"1TOKEN=x", "1TOKEN=x"},
		{"notoken TOKEN", "notoken TOKEN"},
		{"https://user:pw@Example.COM:0443/p/a?sig=1&x=2#f", "https://example.com:443/p/a"},
		{"HTTPS://Host/path?q", "https://host/path"},
		{"http:///nohost", "http://redacted-invalid-url"},
		{"http://host:99999/x", "http://host/x"},
		{"http://host:abc/x", "http://host/x"},
		{"see http://a.b/c\"quoted and https://d.e/'f'", "see http://a.b/c\"quoted and https://d.e/'f'"},
		{"http://host%Zone/x", "http://host%Zone/x"},
		{"url?X-Amz-Signature=abc&ok=1&expires=2", "url?X-Amz-Signature=<redacted>&ok=1&expires=<redacted>"},
		{"&token=1 ?sig=2 ?nosig", "&token=<redacted> ?sig=<redacted> ?nosig"},
		{"httpx://not", "httpx://not"},
		{"http://", "http://"},
		{"--client_env=GITHUB_TOKEN=ghp_1 --action_env=PATH=/bin", "--client_env=GITHUB_TOKEN=<redacted> --action_env=PATH=/bin"},
		{"a\u3000TOKEN=x\u3000B=1", "a\u3000TOKEN=<redacted>\u3000B=1"},
		{"path/with/TOKEN=x/y", "path/with/TOKEN=x/y"},
	}
	var none redaction
	for _, c := range cases {
		if got := none.text(c[0]); got != c[1] {
			t.Errorf("redact(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestRedactPathsLongestFirst(t *testing.T) {
	posixOnly(t)
	r := newRedaction("/work/space", "/work", "/tmp/run")
	got := r.text("/work/space/a /work/b /tmp/run/c")
	if want := "<workspace>/a <output_base>/b <tmp>/c"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedactJSONKeys(t *testing.T) {
	value := redaction{}.json(map[string]any{"Api-Key": "x", "nested": map[string]any{"my_token_value": 1},
		"list": []any{"DD_API_KEY=abc"}}).(map[string]any)
	if value["Api-Key"] != redacted || value["nested"].(map[string]any)["my_token_value"] != redacted ||
		value["list"].([]any)[0] != "DD_API_KEY=<redacted>" {
		t.Fatalf("got %v", value)
	}
}

func TestBoundJSON(t *testing.T) {
	list := make([]any, 102)
	for i := range list {
		list[i] = i
	}
	bounded := boundJSON(map[string]any{"list": list, "text": strings.Repeat("é", 20003)}).(map[string]any)
	items := bounded["list"].([]any)
	marker := items[100].(map[string]any)
	if len(items) != 101 || marker["total_items"] != 102 || marker["omitted_items"] != 2 || marker["truncated"] != true {
		t.Fatalf("list %v", items[99:])
	}
	if want := strings.Repeat("é", 20000) + "...<truncated 3 chars>"; bounded["text"] != want {
		t.Fatal("text not truncated by characters")
	}
}

func TestWriteBundleZip(t *testing.T) {
	data := writeBundleZip(map[string]string{"b.json": "{}\n", "a/x.md": "x"})
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range reader.File {
		names = append(names, file.Name)
		//nolint:staticcheck // zipfile's MS-DOS date fields.
		if file.Method != zip.Deflate || file.ModifiedDate != 1<<5|1 || file.ModifiedTime != 0 || len(file.Extra) != 0 {
			t.Errorf("%s: method %d date %d time %d extra %d", file.Name, file.Method, file.ModifiedDate, file.ModifiedTime,
				len(file.Extra))
		}
		in, _ := file.Open()
		content, _ := io.ReadAll(in)
		if file.Name == "b.json" && string(content) != "{}\n" {
			t.Errorf("content %q", content)
		}
	}
	if strings.Join(names, ",") != "a/x.md,b.json" {
		t.Fatalf("names %v", names)
	}
}
