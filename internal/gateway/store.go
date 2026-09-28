package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

type App struct {
	db      *sql.DB
	key     []byte
	dataDir string
	cache   *urlCache
}

func New(dataDir string) (*App, string, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(dataDir, "secret.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, "", err
		}
		f, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, "", e
		}
		_, err = f.Write(key)
		closeErr := f.Close()
		if err != nil {
			return nil, "", err
		}
		if closeErr != nil {
			return nil, "", closeErr
		}
	} else if err != nil {
		return nil, "", err
	}
	if len(key) != 32 {
		return nil, "", fmt.Errorf("invalid secret.key length")
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "gateway.db"))
	if err != nil {
		return nil, "", err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, "", err
		}
	}
	schema := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sessions(id_hash TEXT PRIMARY KEY,expires_at INTEGER NOT NULL,created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS strm_jobs(id TEXT PRIMARY KEY,name TEXT NOT NULL,enabled INTEGER NOT NULL,source_dir TEXT NOT NULL,output_dir TEXT NOT NULL,extensions TEXT NOT NULL,delete_missing INTEGER NOT NULL,scan_mode TEXT NOT NULL,schedule TEXT NOT NULL DEFAULT '',base_url TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS strm_mapping(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES strm_jobs(id) ON DELETE CASCADE,source_path TEXT NOT NULL,relative_path TEXT NOT NULL,output_path TEXT NOT NULL,source_size INTEGER NOT NULL,source_mtime INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,UNIQUE(job_id,source_path))`,
		`CREATE TABLE IF NOT EXISTS scan_runs(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES strm_jobs(id) ON DELETE CASCADE,status TEXT NOT NULL,files_seen INTEGER NOT NULL DEFAULT 0,files_written INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',started_at INTEGER NOT NULL,finished_at INTEGER)`,
		`CREATE TABLE IF NOT EXISTS event_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,level TEXT NOT NULL,message TEXT NOT NULL,created_at INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO schema_migrations(version) VALUES(1)`,
	}
	for _, q := range schema {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, "", err
		}
	}
	a := &App{db: db, key: key, dataDir: dataDir, cache: newURLCache()}
	var verifier string
	err = db.QueryRow(`SELECT value FROM settings WHERE key='admin_verifier'`).Scan(&verifier)
	if err == sql.ErrNoRows {
		token, e := randomToken(32)
		if e != nil {
			db.Close()
			return nil, "", e
		}
		_, e = db.Exec(`INSERT INTO settings(key,value) VALUES('admin_verifier',?)`, hash(token))
		if e != nil {
			db.Close()
			return nil, "", e
		}
		return a, token, nil
	}
	if err != nil {
		db.Close()
		return nil, "", err
	}
	return a, "", nil
}

func (a *App) Close() error { return a.db.Close() }
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (a *App) setting(key string) (string, error) {
	var v string
	err := a.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
func (a *App) setSetting(key, value string) error {
	_, err := a.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
func (a *App) encrypt(s string) (string, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(s), nil)), nil
}
func (a *App) decrypt(s string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < g.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	b, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	return string(b), err
}
func now() int64 { return time.Now().Unix() }
func (a *App) event(level, message string) {
	_, _ = a.db.Exec(`INSERT INTO event_logs(level,message,created_at) VALUES(?,?,?)`, level, message, now())
}
