package gateway

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type cachedURL struct {
	url   string
	until time.Time
}
type urlCache struct {
	mu    sync.Mutex
	items map[string]cachedURL
}

func newURLCache() *urlCache { return &urlCache{items: map[string]cachedURL{}} }
func (c *urlCache) clear()   { c.mu.Lock(); defer c.mu.Unlock(); c.items = map[string]cachedURL{} }
func (c *urlCache) get(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.items[id]
	if time.Now().Before(v.until) {
		return v.url
	}
	delete(c.items, id)
	return ""
}
func (c *urlCache) put(id, url string, expires uint64) {
	if expires < 30 {
		return
	}
	if expires > 3600 {
		expires = 3600
	}
	ttl := time.Duration(expires)*time.Second - 15*time.Second
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[id] = cachedURL{url, time.Now().Add(ttl)}
}
func (a *App) redirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 26 {
		http.NotFound(w, r)
		return
	}
	var source string
	err := a.db.QueryRow(`SELECT m.source_path FROM strm_mapping m JOIN strm_jobs j ON j.id=m.job_id WHERE m.id=? AND j.enabled=1`, id).Scan(&source)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	direct := a.cache.get(id)
	if direct == "" {
		c, err := a.configuredCloud()
		if err != nil {
			http.Error(w, "CloudDrive2 unavailable", 502)
			return
		}
		defer c.close()
		var expires uint64
		direct, expires, err = c.directURL(r.Context(), source)
		if err != nil {
			http.Error(w, "direct URL unavailable: "+err.Error(), 502)
			return
		}
		a.cache.put(id, direct, expires)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, direct, http.StatusFound)
}
func (a *App) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	dist := os.Getenv("WEB_DIST")
	if dist == "" {
		dist = "web/dist"
	}
	name := filepath.Join(dist, "index.html")
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		candidate, err := safePath(dist, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		name = candidate
	}
	info, err := os.Stat(name)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, name)
}
