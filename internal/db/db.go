package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // sqlite driver
	"golang.org/x/crypto/bcrypt"
)

// User represents an account.
type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// File holds metadata for stored content.
type File struct {
	ID         string
	Owner      string
	UploaderID string
	Filename   string
	Size       int64
	SHA256     string
	MIME       string
	StorageKey string
	CreatedAt  time.Time
}

// FileFilter narrows list results.
type FileFilter struct {
	Owner      string
	MimePrefix string
	Since      *time.Time
	Until      *time.Time
	Limit      int
}

// Store wraps a sqlite database.
type Store struct {
	db *sql.DB
}

// Open opens or initializes the sqlite database and seeds an admin user if none exists.
func Open(path, adminEmail, adminPassword string) (*Store, error) {
	if adminEmail == "" || adminPassword == "" {
		return nil, errors.New("admin email and password required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	dbh, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	dbh.SetMaxOpenConns(1)
	dbh.SetMaxIdleConns(1)

	s := &Store{db: dbh}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	if err := s.seedAdmin(adminEmail, adminPassword); err != nil {
		return nil, err
	}
	return s, nil
}

// Close closes the underlying db.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS users(
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			role TEXT NOT NULL,
			password_hash BLOB NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS files(
			id TEXT PRIMARY KEY,
			owner TEXT NOT NULL,
			uploader_id TEXT NOT NULL,
			filename TEXT NOT NULL,
			size INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			mime TEXT NOT NULL,
			storage_key TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS audits(
			id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL,
			action TEXT NOT NULL,
			target_id TEXT,
			metadata TEXT,
			created_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) seedAdmin(email, password string) error {
	ctx := context.Background()
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return s.CreateUser(ctx, User{
		Email:       strings.ToLower(email),
		DisplayName: "Admin",
		Role:        "admin",
	}, password)
}

// CreateUser inserts a new user.
func (s *Store) CreateUser(ctx context.Context, u User, password string) error {
	if u.Email == "" || password == "" {
		return errors.New("email and password required")
	}
	now := time.Now().UTC()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	id := u.ID
	if id == "" {
		id = newID()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users(id, email, display_name, role, password_hash, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, id, strings.ToLower(u.Email), u.DisplayName, u.Role, hash, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	return err
}

// Authenticate verifies email/password and returns the user.
func (s *Store) Authenticate(email, password string) (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, display_name, role, password_hash, created_at, updated_at
		FROM users WHERE lower(email)=lower(?)
	`, email)
	var u User
	var hash []byte
	var created, updated string
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &hash, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	u.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &u, nil
}

// UserByID returns the user.
func (s *Store) UserByID(id string) (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, email, display_name, role, password_hash, created_at, updated_at
		FROM users WHERE id=?
	`, id)
	var u User
	var hash []byte
	var created, updated string
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &hash, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	u.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &u, nil
}

// AddFile persists file metadata and returns populated record.
func (s *Store) AddFile(f File) (File, error) {
	if f.Owner == "" || f.UploaderID == "" || f.Filename == "" || f.StorageKey == "" || f.SHA256 == "" {
		return File{}, errors.New("missing file metadata")
	}
	now := time.Now().UTC()
	f.ID = newID()
	f.CreatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO files(id, owner, uploader_id, filename, size, sha256, mime, storage_key, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, f.ID, strings.ToLower(f.Owner), f.UploaderID, f.Filename, f.Size, strings.ToLower(f.SHA256), f.MIME, f.StorageKey, now.Format(time.RFC3339Nano))
	if err != nil {
		return File{}, err
	}
	return f, nil
}

// ListFiles returns files visible to the requester (admin sees all).
func (s *Store) ListFiles(role, requesterEmail string) ([]File, error) {
	return s.ListFilesFiltered(role, requesterEmail, FileFilter{})
}

// ListFilesFiltered returns files matching the filter and access scope.
func (s *Store) ListFilesFiltered(role, requesterEmail string, f FileFilter) ([]File, error) {
	clauses := []string{}
	args := []interface{}{}

	if role == "admin" {
		if f.Owner != "" {
			clauses = append(clauses, "owner=lower(?)")
			args = append(args, strings.ToLower(f.Owner))
		}
	} else {
		// non-admin restricted to own files
		clauses = append(clauses, "owner=lower(?)")
		args = append(args, strings.ToLower(requesterEmail))
	}

	if f.MimePrefix != "" {
		clauses = append(clauses, "mime LIKE ?")
		args = append(args, strings.ToLower(f.MimePrefix)+"%")
	}
	if f.Since != nil {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if f.Until != nil {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, f.Until.UTC().Format(time.RFC3339Nano))
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	query := "SELECT id, owner, uploader_id, filename, size, sha256, mime, storage_key, created_at FROM files"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []File{}
	for rows.Next() {
		var f File
		var created string
		if err := rows.Scan(&f.ID, &f.Owner, &f.UploaderID, &f.Filename, &f.Size, &f.SHA256, &f.MIME, &f.StorageKey, &created); err != nil {
			return nil, err
		}
		f.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, f)
	}
	return out, nil
}

// GetFile retrieves a file by id.
func (s *Store) GetFile(id string) (File, bool) {
	row := s.db.QueryRow(`SELECT id, owner, uploader_id, filename, size, sha256, mime, storage_key, created_at FROM files WHERE id=?`, id)
	var f File
	var created string
	if err := row.Scan(&f.ID, &f.Owner, &f.UploaderID, &f.Filename, &f.Size, &f.SHA256, &f.MIME, &f.StorageKey, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, false
		}
		return File{}, false
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return f, true
}

// AddAudit records an audit event.
func (s *Store) AddAudit(ctx context.Context, actorID, action, targetID, metadata string) {
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO audits(id, actor_id, action, target_id, metadata, created_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, newID(), actorID, action, targetID, metadata, time.Now().UTC().Format(time.RFC3339Nano))
}

func newID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
