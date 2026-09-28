package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type Job struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	SourceDir     string   `json:"source_dir"`
	OutputDir     string   `json:"output_dir"`
	Extensions    []string `json:"extensions"`
	DeleteMissing bool     `json:"delete_missing"`
	ScanMode      string   `json:"scan_mode"`
	Schedule      string   `json:"schedule"`
	BaseURL       string   `json:"base_url"`
}

var scanMu sync.Mutex

func newID() string { return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String() }
func validCloudPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.Contains(p, "\\") && !strings.ContainsRune(p, 0)
}
func validateJob(j *Job) error {
	j.Name = strings.TrimSpace(j.Name)
	if j.Name == "" || len(j.Name) > 100 {
		return errors.New("name must be 1–100 characters")
	}
	if !validCloudPath(j.SourceDir) {
		return errors.New("invalid CloudDrive2 source directory")
	}
	abs, err := filepath.Abs(j.OutputDir)
	if err != nil || j.OutputDir == "" || !filepath.IsAbs(j.OutputDir) {
		return errors.New("output directory must be absolute")
	}
	j.OutputDir = filepath.Clean(abs)
	if filepath.Dir(j.OutputDir) == j.OutputDir {
		return errors.New("filesystem root cannot be an output directory")
	}
	u, err := url.Parse(j.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("base_url must be an http(s) origin")
	}
	j.BaseURL = strings.TrimRight(j.BaseURL, "/")
	if j.ScanMode == "" {
		j.ScanMode = "manual"
	}
	if j.ScanMode != "manual" && j.ScanMode != "scheduled" {
		return errors.New("scan_mode must be manual or scheduled")
	}
	if j.ScanMode == "scheduled" {
		return errors.New("scheduled scanning is planned for phase 5; use manual")
	}
	if len(j.Extensions) == 0 {
		j.Extensions = []string{".mkv", ".mp4", ".avi", ".mov", ".m4v", ".ts"}
	}
	seen := map[string]bool{}
	exts := make([]string, 0, len(j.Extensions))
	for _, ext := range j.Extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if !strings.HasPrefix(ext, ".") || len(ext) < 2 || strings.ContainsAny(ext, "/\\ ") {
			return errors.New("invalid media extension")
		}
		if !seen[ext] {
			seen[ext] = true
			exts = append(exts, ext)
		}
	}
	sort.Strings(exts)
	j.Extensions = exts
	return nil
}
func (a *App) listJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id,name,enabled,source_dir,output_dir,extensions,delete_missing,scan_mode,schedule,base_url FROM strm_jobs ORDER BY created_at DESC`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		var enabled, del int
		var ext string
		if rows.Scan(&j.ID, &j.Name, &enabled, &j.SourceDir, &j.OutputDir, &ext, &del, &j.ScanMode, &j.Schedule, &j.BaseURL) != nil {
			fail(w, 500, "database error")
			return
		}
		j.Enabled = enabled != 0
		j.DeleteMissing = del != 0
		_ = json.Unmarshal([]byte(ext), &j.Extensions)
		out = append(out, j)
	}
	jsonOut(w, 200, out)
}
func (a *App) getJob(id string) (Job, error) {
	var j Job
	var enabled, del int
	var ext string
	err := a.db.QueryRow(`SELECT id,name,enabled,source_dir,output_dir,extensions,delete_missing,scan_mode,schedule,base_url FROM strm_jobs WHERE id=?`, id).Scan(&j.ID, &j.Name, &enabled, &j.SourceDir, &j.OutputDir, &ext, &del, &j.ScanMode, &j.Schedule, &j.BaseURL)
	j.Enabled = enabled != 0
	j.DeleteMissing = del != 0
	_ = json.Unmarshal([]byte(ext), &j.Extensions)
	return j, err
}
func (a *App) createJob(w http.ResponseWriter, r *http.Request) {
	scanMu.Lock()
	defer scanMu.Unlock()
	var j Job
	if decode(r, &j) != nil {
		fail(w, 400, "invalid request")
		return
	}
	if err := validateJob(&j); err != nil {
		fail(w, 400, err.Error())
		return
	}
	rows, err := a.db.Query(`SELECT output_dir FROM strm_jobs`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	for rows.Next() {
		var existing string
		if rows.Scan(&existing) == nil {
			rel1, e1 := filepath.Rel(existing, j.OutputDir)
			rel2, e2 := filepath.Rel(j.OutputDir, existing)
			if e1 == nil && e2 == nil && (rel1 == "." || (!strings.HasPrefix(rel1, "..") && !filepath.IsAbs(rel1)) || (!strings.HasPrefix(rel2, "..") && !filepath.IsAbs(rel2))) {
				rows.Close()
				fail(w, 409, "output directory overlaps another job")
				return
			}
		}
	}
	rows.Close()
	j.ID = newID()
	ext, _ := json.Marshal(j.Extensions)
	_, err = a.db.Exec(`INSERT INTO strm_jobs(id,name,enabled,source_dir,output_dir,extensions,delete_missing,scan_mode,schedule,base_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, j.ID, j.Name, j.Enabled, j.SourceDir, j.OutputDir, string(ext), j.DeleteMissing, j.ScanMode, j.Schedule, j.BaseURL, now(), now())
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	jsonOut(w, 201, j)
}
func (a *App) updateJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, err := a.getJob(id)
	if err != nil {
		fail(w, 404, "job not found")
		return
	}
	var j Job
	if decode(r, &j) != nil {
		fail(w, 400, "invalid request")
		return
	}
	if err = validateJob(&j); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if old.SourceDir != j.SourceDir || old.OutputDir != j.OutputDir || old.BaseURL != j.BaseURL {
		returnError := "source, output, and base URL cannot change after creation; create a new job"
		fail(w, 409, returnError)
		return
	}
	j.ID = id
	ext, _ := json.Marshal(j.Extensions)
	_, err = a.db.Exec(`UPDATE strm_jobs SET name=?,enabled=?,extensions=?,delete_missing=?,scan_mode=?,schedule=?,updated_at=? WHERE id=?`, j.Name, j.Enabled, string(ext), j.DeleteMissing, j.ScanMode, j.Schedule, now(), id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	jsonOut(w, 200, j)
}
func (a *App) deleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := a.getJob(id)
	if err != nil {
		fail(w, 404, "job not found")
		return
	}
	scanMu.Lock()
	defer scanMu.Unlock()
	rows, err := a.db.Query(`SELECT output_path FROM strm_mapping WHERE job_id=?`, id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	var paths []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			paths = append(paths, p)
		}
	}
	rows.Close()
	for _, p := range paths {
		if err = safeRemove(j.OutputDir, p); err != nil {
			fail(w, 500, "could not remove generated STRM file: "+err.Error())
			return
		}
	}
	_, err = a.db.Exec(`DELETE FROM strm_jobs WHERE id=?`, id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func safePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) || (filepath.Separator != '\\' && strings.Contains(relative, "\\")) {
		return "", errors.New("invalid relative path")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." {
		return "", errors.New("path traversal")
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes output root")
	}
	return target, nil
}
func checkNoSymlink(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes output root")
	}
	for p := root; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in output path")
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	parts := strings.Split(rel, string(filepath.Separator))
	p := root
	for _, part := range parts {
		p = filepath.Join(p, part)
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in output path")
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}
func safeWrite(root, target, content string) error {
	if err := checkNoSymlink(root, target); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if err := checkNoSymlink(root, target); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".strm-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
func safeRemove(root, target string) error {
	if err := checkNoSymlink(root, target); err != nil {
		return err
	}
	if filepath.Clean(root) == filepath.Clean(target) {
		return errors.New("refusing to remove output root")
	}
	err := os.Remove(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (a *App) scanJob(w http.ResponseWriter, r *http.Request) {
	if !scanMu.TryLock() {
		fail(w, 409, "a scan is already running")
		return
	}
	defer scanMu.Unlock()
	j, err := a.getJob(r.PathValue("id"))
	if err != nil {
		fail(w, 404, "job not found")
		return
	}
	if !j.Enabled {
		fail(w, 409, "job is disabled")
		return
	}
	run := newID()
	_, _ = a.db.Exec(`INSERT INTO scan_runs(id,job_id,status,started_at) VALUES(?,?,'running',?)`, run, j.ID, now())
	seen, written, err := a.scan(r.Context(), j)
	status := "completed"
	message := ""
	if err != nil {
		status = "failed"
		message = err.Error()
		a.event("error", "scan failed for job "+j.ID+": "+message)
	} else {
		a.event("info", fmt.Sprintf("scan completed for job %s: %d files", j.ID, seen))
	}
	_, _ = a.db.Exec(`UPDATE scan_runs SET status=?,files_seen=?,files_written=?,error=?,finished_at=? WHERE id=?`, status, seen, written, message, now(), run)
	if err != nil {
		fail(w, 502, message)
		return
	}
	jsonOut(w, 200, map[string]any{"run_id": run, "files_seen": seen, "files_written": written})
}
func (a *App) scan(ctx context.Context, j Job) (int, int, error) {
	c, err := a.configuredCloud()
	if err != nil {
		return 0, 0, err
	}
	defer c.close()
	queue := []string{j.SourceDir}
	seen := map[string]bool{}
	filesSeen, written := 0, 0
	exts := map[string]bool{}
	for _, e := range j.Extensions {
		exts[e] = true
	}
	for len(queue) > 0 {
		if ctx.Err() != nil {
			return filesSeen, written, ctx.Err()
		}
		dir := queue[0]
		queue = queue[1:]
		files, err := c.list(ctx, dir)
		if err != nil {
			return filesSeen, written, err
		}
		for _, f := range files {
			p := f.GetFullPathName()
			if !validCloudPath(p) || !strings.HasPrefix(p, j.SourceDir+"/") {
				if j.SourceDir == "/" && !validCloudPath(p) {
					continue
				}
				if j.SourceDir != "/" {
					continue
				}
			}
			if f.GetIsDirectory() {
				queue = append(queue, p)
				continue
			}
			if !exts[strings.ToLower(path.Ext(p))] {
				continue
			}
			relative := strings.TrimPrefix(p, strings.TrimRight(j.SourceDir, "/")+"/")
			if relative == p || relative == "" {
				continue
			}
			outputRel := strings.TrimSuffix(relative, path.Ext(relative)) + ".strm"
			target, err := safePath(j.OutputDir, filepath.FromSlash(outputRel))
			if err != nil {
				return filesSeen, written, err
			}
			id := newID()
			var existing string
			err = a.db.QueryRow(`SELECT id FROM strm_mapping WHERE job_id=? AND source_path=?`, j.ID, p).Scan(&existing)
			if err == nil {
				id = existing
			} else if err != sql.ErrNoRows {
				return filesSeen, written, err
			} else if _, statErr := os.Lstat(target); statErr == nil {
				return filesSeen, written, fmt.Errorf("output file already exists outside mapping: %s", target)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return filesSeen, written, statErr
			}
			content := j.BaseURL + "/r/" + id + "\n"
			if err = safeWrite(j.OutputDir, target, content); err != nil {
				return filesSeen, written, err
			}
			mtime := int64(0)
			if f.GetWriteTime() != nil {
				mtime = f.GetWriteTime().AsTime().Unix()
			}
			_, err = a.db.Exec(`INSERT INTO strm_mapping(id,job_id,source_path,relative_path,output_path,source_size,source_mtime,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,source_path) DO UPDATE SET relative_path=excluded.relative_path,output_path=excluded.output_path,source_size=excluded.source_size,source_mtime=excluded.source_mtime,updated_at=excluded.updated_at`, id, j.ID, p, relative, target, f.GetSize(), mtime, now(), now())
			if err != nil {
				return filesSeen, written, err
			}
			seen[p] = true
			filesSeen++
			written++
		}
	}
	if j.DeleteMissing {
		rows, err := a.db.Query(`SELECT id,source_path,output_path FROM strm_mapping WHERE job_id=?`, j.ID)
		if err != nil {
			return filesSeen, written, err
		}
		type obsolete struct{ id, path, out string }
		var old []obsolete
		for rows.Next() {
			var x obsolete
			if rows.Scan(&x.id, &x.path, &x.out) == nil && !seen[x.path] {
				old = append(old, x)
			}
		}
		rows.Close()
		for _, x := range old {
			if err = safeRemove(j.OutputDir, x.out); err != nil {
				return filesSeen, written, err
			}
			if _, err = a.db.Exec(`DELETE FROM strm_mapping WHERE id=?`, x.id); err != nil {
				return filesSeen, written, err
			}
		}
	}
	return filesSeen, written, nil
}
