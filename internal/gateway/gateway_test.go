package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminSessionAndSecret(t *testing.T) {
	dir := t.TempDir()
	a, token, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("initial token missing")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("admin token stored in database")
	}
	h := a.Handler()
	post := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post("/api/login", `{"token":"wrong"}`, nil); w.Code != 401 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	w := post("/api/login", string(body), nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly {
		t.Fatal("session cookie is not HttpOnly")
	}
	r := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("authenticated settings: %d", w.Code)
	}
	w = post("/api/logout", "", cookie)
	if w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("revoked session: %d", w.Code)
	}
	a.Close()
	a, token2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if token2 != "" {
		t.Fatal("token regenerated on restart")
	}
}

func TestSafeOutputPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "strm")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../outside.strm", "..\\outside.strm", ""} {
		if _, err := safePath(root, p); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	target, err := safePath(root, filepath.Join("Movies", "film.strm"))
	if err != nil {
		t.Fatal(err)
	}
	if err := safeWrite(root, target, "https://example.test/r/id\n"); err != nil {
		t.Fatal(err)
	}
	if err := safeRemove(root, target); err != nil {
		t.Fatal(err)
	}
	if err := safeRemove(root, root); err == nil {
		t.Fatal("removed root")
	}
}

func TestEncryption(t *testing.T) {
	a, _, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	value := "cloud-secret"
	ciphertext, err := a.encrypt(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, value) {
		t.Fatal("plaintext leaked")
	}
	plain, err := a.decrypt(ciphertext)
	if err != nil || plain != value {
		t.Fatal("decrypt failed")
	}
}
