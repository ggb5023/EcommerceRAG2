package edge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRoutesAndHost(t *testing.T) {
	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, name) })
	}
	h := Routes(handler("go"), handler("oidc"), handler("frontend"), "merchant.test:8443")
	for path, want := range map[string]string{
		"/v1/customer/session": "go", "/admin/v1/session": "go", "/healthz": "go", "/setup": "go",
		"/identity/realms/merchant": "oidc", "/support": "frontend", "/admin/merchant/reviews": "frontend",
	} {
		r := httptest.NewRequest("GET", "https://merchant.test:8443"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != want || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("CSP missing")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://attacker.test/v1/auth/login", nil))
	if w.Code != 400 {
		t.Fatal("unapproved host accepted")
	}
}

func TestRoutesSkipsIdentityWhenAbsent(t *testing.T) {
	frontend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "frontend") })
	h := Routes(http.NotFoundHandler(), nil, frontend, "merchant.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://merchant.test/identity/realms/merchant", nil))
	if w.Body.String() != "frontend" {
		t.Fatal("identity path must fall through to the frontend when OIDC is off")
	}
}

func TestProxySanitizesForwardedHeaders(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") == "spoof" || r.Header.Get("X-Real-IP") != "192.0.2.1" || r.Header.Get("X-Forwarded-Proto") != "https" {
			t.Error("untrusted proxy headers")
		}
		io.WriteString(w, "data: checked\n\n")
	}))
	defer target.Close()
	r := httptest.NewRequest("GET", "https://merchant.test/v1/customer/events", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("Forwarded", "spoof")
	r.Header.Set("X-Forwarded-For", "spoof")
	r.Header.Set("X-Real-IP", "spoof")
	w := httptest.NewRecorder()
	Proxy(target.URL).ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "data: checked\n\n" {
		t.Fatal("stream proxy failed")
	}
}

func TestSPAAssetAndFallback(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0600)
	os.WriteFile(filepath.Join(root, "app.js"), []byte("asset"), 0600)
	for path, want := range map[string]string{"/app.js": "asset", "/admin/merchant/reviews": "index", "/support": "index"} {
		w := httptest.NewRecorder()
		SPA(root, "").ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Body.String() != want {
			t.Fatal(path, w.Body.String())
		}
	}
}
