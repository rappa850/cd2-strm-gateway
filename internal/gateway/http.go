package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	jsonOut(w, status, map[string]string{"error": message})
}
func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("one JSON object expected")
	}
	return nil
}
func (a *App) authenticated(r *http.Request) bool {
	c, err := r.Cookie("gateway_session")
	if err != nil {
		return false
	}
	var expires int64
	err = a.db.QueryRow(`SELECT expires_at FROM sessions WHERE id_hash=?`, hash(c.Value)).Scan(&expires)
	return err == nil && expires > now()
}
func (a *App) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authenticated(r) {
			fail(w, 401, "login required")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host {
					fail(w, 403, "invalid origin")
					return
				}
			}
		}
		next(w, r)
	}
}
func sessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if decode(r, &body) != nil {
		fail(w, 400, "invalid request")
		return
	}
	verifier, err := a.setting("admin_verifier")
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if subtle.ConstantTimeCompare([]byte(hash(body.Token)), []byte(verifier)) != 1 {
		fail(w, 401, "invalid token")
		return
	}
	session, err := randomToken(32)
	if err != nil {
		fail(w, 500, "random generator error")
		return
	}
	_, err = a.db.Exec(`INSERT INTO sessions(id_hash,expires_at,created_at) VALUES(?,?,?)`, hash(session), now()+7*86400, now())
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	sessionCookie(w, r, session, 7*86400)
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("gateway_session")
	if c != nil {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE id_hash=?`, hash(c.Value))
	}
	sessionCookie(w, r, "", -1)
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) rotate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
	}
	if decode(r, &body) != nil {
		fail(w, 400, "invalid request")
		return
	}
	verifier, err := a.setting("admin_verifier")
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if subtle.ConstantTimeCompare([]byte(hash(body.Current)), []byte(verifier)) != 1 {
		fail(w, 403, "invalid current token")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		fail(w, 500, "random generator error")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE settings SET value=? WHERE key='admin_verifier'`, hash(token)); err != nil {
		fail(w, 500, "database error")
		return
	}
	if _, err = tx.Exec(`DELETE FROM sessions`); err != nil {
		fail(w, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	sessionCookie(w, r, "", -1)
	jsonOut(w, 200, map[string]string{"token": token})
}
func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	address, _ := a.setting("cloud_address")
	enc, _ := a.setting("cloud_token")
	masked := ""
	if enc != "" {
		masked = "••••••••"
	}
	jsonOut(w, 200, map[string]string{"address": address, "token_masked": masked})
}
func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if decode(r, &body) != nil {
		fail(w, 400, "invalid request")
		return
	}
	body.Address = strings.TrimSpace(body.Address)
	if body.Token == "" {
		enc, _ := a.setting("cloud_token")
		if enc != "" {
			body.Token, _ = a.decrypt(enc)
		}
	}
	if body.Token == "" {
		fail(w, 400, "API token is required")
		return
	}
	c, err := dialCloud(body.Address, body.Token)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	defer c.close()
	if err = c.validate(r.Context()); err != nil {
		fail(w, 400, err.Error())
		return
	}
	encrypted, err := a.encrypt(body.Token)
	if err != nil {
		fail(w, 500, "encryption error")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES('cloud_address',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, body.Address); err != nil {
		fail(w, 500, "database error")
		return
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES('cloud_token',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, encrypted); err != nil {
		fail(w, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	a.cache.clear()
	a.event("info", "CloudDrive2 settings validated and saved")
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) browse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	if !validCloudPath(p) {
		fail(w, 400, "invalid path")
		return
	}
	c, err := a.configuredCloud()
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	defer c.close()
	files, err := c.list(r.Context(), p)
	if err != nil {
		fail(w, 502, "CloudDrive2 listing failed: "+err.Error())
		return
	}
	type item struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Directory bool   `json:"directory"`
		Size      int64  `json:"size"`
	}
	out := make([]item, 0, len(files))
	for _, f := range files {
		path := f.GetFullPathName()
		if !validCloudPath(path) {
			continue
		}
		out = append(out, item{f.GetName(), path, f.GetIsDirectory(), f.GetSize()})
	}
	jsonOut(w, 200, out)
}
func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT level,message,created_at FROM event_logs ORDER BY id DESC LIMIT 100`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	type entry struct {
		Level   string `json:"level"`
		Message string `json:"message"`
		Created int64  `json:"created_at"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if rows.Scan(&e.Level, &e.Message, &e.Created) == nil {
			out = append(out, e)
		}
	}
	jsonOut(w, 200, out)
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("GET /api/me", a.require(func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]bool{"authenticated": true}) }))
	mux.HandleFunc("POST /api/logout", a.require(a.logout))
	mux.HandleFunc("POST /api/admin/rotate", a.require(a.rotate))
	mux.HandleFunc("GET /api/settings", a.require(a.getSettings))
	mux.HandleFunc("PUT /api/settings", a.require(a.saveSettings))
	mux.HandleFunc("GET /api/browse", a.require(a.browse))
	mux.HandleFunc("GET /api/logs", a.require(a.logs))
	mux.HandleFunc("GET /api/jobs", a.require(a.listJobs))
	mux.HandleFunc("POST /api/jobs", a.require(a.createJob))
	mux.HandleFunc("PUT /api/jobs/{id}", a.require(a.updateJob))
	mux.HandleFunc("DELETE /api/jobs/{id}", a.require(a.deleteJob))
	mux.HandleFunc("POST /api/jobs/{id}/scan", a.require(a.scanJob))
	mux.HandleFunc("GET /r/{id}", a.redirect)
	mux.HandleFunc("GET /", a.static)
	return mux
}
