package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{name: "empty string", path: "", want: []string{}},
		{name: "single slash", path: "/", want: []string{""}},
		{name: "typical path", path: "/a/b/c", want: []string{"", "a", "b", "c"}},
		{name: "trailing slash consumed", path: "/a/b/c/", want: []string{"", "a", "b", "c"}},
		{name: "no leading slash", path: "a/b", want: []string{"a", "b"}},
		{name: "sandbox path", path: "/v1/sandboxes/abc123", want: []string{"", "v1", "sandboxes", "abc123"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitPath(tt.path)
			if len(tt.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseSandboxRoute(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantRoute string
		wantSID   string
	}{
		// Valid routes
		{name: "sandbox base", path: "/v1/sandboxes/abc", wantRoute: "sandbox", wantSID: "abc"},
		{name: "touch action", path: "/v1/sandboxes/abc/touch", wantRoute: "touch", wantSID: "abc"},
		{name: "exec action", path: "/v1/sandboxes/abc/exec", wantRoute: "exec", wantSID: "abc"},
		{name: "files upload", path: "/v1/sandboxes/abc/files/upload", wantRoute: "files/upload", wantSID: "abc"},
		{name: "files download", path: "/v1/sandboxes/abc/files/download", wantRoute: "files/download", wantSID: "abc"},

		// Invalid routes
		{name: "empty sessionId (trailing slash)", path: "/v1/sandboxes/", wantRoute: "", wantSID: ""},
		{name: "too few parts", path: "/v1/sandboxes", wantRoute: "", wantSID: ""},
		{name: "wrong version", path: "/v2/sandboxes/abc", wantRoute: "", wantSID: ""},
		{name: "wrong resource", path: "/v1/other/abc", wantRoute: "", wantSID: ""},
		{name: "unknown action", path: "/v1/sandboxes/abc/unknown", wantRoute: "", wantSID: ""},
		{name: "extra parts after touch", path: "/v1/sandboxes/abc/touch/extra", wantRoute: "", wantSID: ""},
		{name: "extra parts after exec", path: "/v1/sandboxes/abc/exec/extra", wantRoute: "", wantSID: ""},
		{name: "files without sub-action", path: "/v1/sandboxes/abc/files", wantRoute: "", wantSID: ""},
		{name: "unknown files action", path: "/v1/sandboxes/abc/files/unknown", wantRoute: "", wantSID: ""},
		{name: "extra parts after files/upload", path: "/v1/sandboxes/abc/files/upload/extra", wantRoute: "", wantSID: ""},
		{name: "empty string", path: "", wantRoute: "", wantSID: ""},
		{name: "root slash", path: "/", wantRoute: "", wantSID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, sessionId := parseSandboxRoute(tt.path)
			assert.Equal(t, tt.wantRoute, route, "route mismatch")
			assert.Equal(t, tt.wantSID, sessionId, "sessionId mismatch")
		})
	}
}
