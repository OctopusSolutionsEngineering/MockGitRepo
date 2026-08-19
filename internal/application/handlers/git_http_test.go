package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIsReadRequest checks how the git HTTP requests are classified, because only the
// requests that leave the repository untouched can be served from a local copy while
// the remote copy is still being made.
func TestIsReadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		method string
		path   string
		query  string
		want   bool
	}{
		{"clone advertisement", http.MethodGet, "/platformhubrepo/info/refs", "service=git-upload-pack", true},
		{"fetch", http.MethodPost, "/platformhubrepo/git-upload-pack", "", true},
		{"dumb protocol object", http.MethodGet, "/platformhubrepo/objects/info/packs", "", true},
		{"push advertisement", http.MethodGet, "/platformhubrepo/info/refs", "service=git-receive-pack", false},
		{"push", http.MethodPost, "/platformhubrepo/git-receive-pack", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			target := "/repo" + test.path
			if test.query != "" {
				target += "?" + test.query
			}
			c.Request = httptest.NewRequest(test.method, target, nil)
			c.Params = gin.Params{{Key: "path", Value: test.path}}

			if got := isReadRequest(c); got != test.want {
				t.Fatalf("isReadRequest = %v, want %v", got, test.want)
			}
		})
	}
}
