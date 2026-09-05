package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

var ErrNotFound = errors.New("job not found")

type Submission struct {
	RequestID      string         `json:"request_id"`
	ServerID       string         `json:"server_id"`
	Workflow       string         `json:"workflow"`
	WorkflowDigest string         `json:"workflow_digest,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	Inputs         []Input        `json:"inputs,omitempty"`
	SubmittedAt    time.Time      `json:"submitted_at,omitempty"`
}

type Input struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type Upload struct {
	ServerID   string    `json:"server_id"`
	SHA256     string    `json:"sha256"`
	RemoteName string    `json:"remote_name"`
	Size       int64     `json:"size"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Output struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder,omitempty"`
	Type      string `json:"type,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Path      string `json:"path,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

type Record struct {
	Schema         string         `json:"schema"`
	ID             string         `json:"id"`
	RequestID      string         `json:"request_id"`
	PromptID       string         `json:"prompt_id,omitempty"`
	ServerID       string         `json:"server_id"`
	Workflow       string         `json:"workflow"`
	WorkflowDigest string         `json:"workflow_digest,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	Inputs         []Input        `json:"inputs,omitempty"`
	Status         string         `json:"status"`
	SubmittedAt    time.Time      `json:"submitted_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Outputs        []Output       `json:"outputs,omitempty"`
	Error          string         `json:"error,omitempty"`
	Revision       int64          `json:"revision"`
}

type Update struct {
	Status    string
	Outputs   []Output
	Error     string
	UpdatedAt time.Time
}

type ListOptions struct {
	Limit    int
	Cursor   string
	Status   string
	ServerID string
}

type Page struct {
	Schema     string   `json:"schema"`
	Jobs       []Record `json:"jobs"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type Store struct {
	db *sql.DB
}

func DefaultPath() (string, error) {
	root := strings.TrimSpace(os.Getenv("CMFY_STATE_DIR"))
	if root == "" {
		root = strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
		if root != "" {
			root = filepath.Join(root, "cmfy")
		}
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, ".local", "state", "cmfy")
	}
	return filepath.Join(root, "history.sqlite3"), nil
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve state path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	store := &Store{db: db}
	if err := store.initialize(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure state database: %w", err)
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL)`,
		`INSERT INTO schema_meta(version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_meta)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL UNIQUE,
			prompt_id TEXT UNIQUE,
			server_id TEXT NOT NULL,
			workflow TEXT NOT NULL,
			workflow_digest TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL DEFAULT '',
			parameters_json TEXT NOT NULL DEFAULT '{}',
			inputs_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL,
			submitted_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			outputs_json TEXT NOT NULL DEFAULT '[]',
			error TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE INDEX IF NOT EXISTS jobs_submitted_idx ON jobs(submitted_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs(status, submitted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS jobs_server_idx ON jobs(server_id, submitted_at DESC)`,
		`CREATE TABLE IF NOT EXISTS upload_cache (
			server_id TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			remote_name TEXT NOT NULL,
			size INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(server_id, sha256)
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize state database: %w", err)
		}
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_meta LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("read state schema version: %w", err)
	}
	if version != schemaVersion {
		return fmt.Errorf("unsupported state schema version %d (supported %d)", version, schemaVersion)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) GetUpload(ctx context.Context, serverID, sha256 string) (Upload, bool, error) {
	var upload Upload
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT server_id, sha256, remote_name, size, updated_at FROM upload_cache WHERE server_id = ? AND sha256 = ?`, serverID, sha256).Scan(&upload.ServerID, &upload.SHA256, &upload.RemoteName, &upload.Size, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, false, nil
	}
	if err != nil {
		return Upload{}, false, fmt.Errorf("read upload cache: %w", err)
	}
	upload.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Upload{}, false, err
	}
	return upload, true, nil
}

func (s *Store) PutUpload(ctx context.Context, upload Upload) error {
	if upload.ServerID == "" || upload.SHA256 == "" || upload.RemoteName == "" {
		return errors.New("server_id, sha256, and remote_name are required for upload cache")
	}
	if upload.UpdatedAt.IsZero() {
		upload.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO upload_cache(server_id, sha256, remote_name, size, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(server_id, sha256) DO UPDATE SET remote_name = excluded.remote_name, size = excluded.size, updated_at = excluded.updated_at`,
		upload.ServerID, upload.SHA256, upload.RemoteName, upload.Size, formatTime(upload.UpdatedAt))
	if err != nil {
		return fmt.Errorf("write upload cache: %w", err)
	}
	return nil
}

func (s *Store) CountPrunable(ctx context.Context, before time.Time, keepRecent int) (int64, error) {
	query := `SELECT COUNT(*) FROM jobs WHERE status IN ('completed','success','failed','error','cancelled','not_found') AND updated_at < ?`
	arguments := []any{formatTime(before.UTC())}
	if keepRecent > 0 {
		query += ` AND id NOT IN (SELECT id FROM jobs ORDER BY submitted_at DESC, id DESC LIMIT ?)`
		arguments = append(arguments, keepRecent)
	}
	var count int64
	if err := s.db.QueryRowContext(ctx, query, arguments...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count prunable jobs: %w", err)
	}
	return count, nil
}

func (s *Store) Prune(ctx context.Context, before time.Time, keepRecent int) (int64, error) {
	query := `DELETE FROM jobs WHERE status IN ('completed','success','failed','error','cancelled','not_found') AND updated_at < ?`
	arguments := []any{formatTime(before.UTC())}
	if keepRecent > 0 {
		query += ` AND id NOT IN (SELECT id FROM jobs ORDER BY submitted_at DESC, id DESC LIMIT ?)`
		arguments = append(arguments, keepRecent)
	}
	result, err := s.db.ExecContext(ctx, query, arguments...)
	if err != nil {
		return 0, fmt.Errorf("prune jobs: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) CountPrunableUploads(ctx context.Context, before time.Time) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM upload_cache WHERE updated_at < ?`, formatTime(before.UTC())).Scan(&count); err != nil {
		return 0, fmt.Errorf("count prunable uploads: %w", err)
	}
	return count, nil
}

func (s *Store) PruneUploads(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM upload_cache WHERE updated_at < ?`, formatTime(before.UTC()))
	if err != nil {
		return 0, fmt.Errorf("prune upload cache: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) Reserve(ctx context.Context, submission Submission) (Record, bool, error) {
	if strings.TrimSpace(submission.RequestID) == "" {
		return Record{}, false, errors.New("request_id is required")
	}
	if strings.TrimSpace(submission.ServerID) == "" {
		return Record{}, false, errors.New("server_id is required")
	}
	if strings.TrimSpace(submission.Workflow) == "" {
		return Record{}, false, errors.New("workflow is required")
	}
	existing, err := s.Get(ctx, submission.RequestID)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Record{}, false, err
	}
	id, err := newID()
	if err != nil {
		return Record{}, false, err
	}
	now := submission.SubmittedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	parameters, err := json.Marshal(nonNilMap(submission.Parameters))
	if err != nil {
		return Record{}, false, fmt.Errorf("encode parameters: %w", err)
	}
	inputs, err := json.Marshal(nonNilInputs(submission.Inputs))
	if err != nil {
		return Record{}, false, fmt.Errorf("encode inputs: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs
		(id, request_id, server_id, workflow, workflow_digest, prompt, parameters_json, inputs_json, status, submitted_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'submitting', ?, ?)`,
		id, submission.RequestID, submission.ServerID, submission.Workflow, submission.WorkflowDigest,
		submission.Prompt, string(parameters), string(inputs), formatTime(now), formatTime(now))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			record, getErr := s.Get(ctx, submission.RequestID)
			return record, false, getErr
		}
		return Record{}, false, fmt.Errorf("reserve job: %w", err)
	}
	record, err := s.Get(ctx, submission.RequestID)
	return record, true, err
}

func (s *Store) MarkSubmitted(ctx context.Context, requestID, promptID string) error {
	if strings.TrimSpace(promptID) == "" {
		return errors.New("prompt_id is required")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET prompt_id = ?, status = 'queued', updated_at = ?, revision = revision + 1 WHERE request_id = ?`, promptID, formatTime(time.Now().UTC()), requestID)
	if err != nil {
		return fmt.Errorf("mark job submitted: %w", err)
	}
	return requireChanged(result)
}

func (s *Store) Update(ctx context.Context, id string, update Update) error {
	if strings.TrimSpace(update.Status) == "" {
		return errors.New("status is required")
	}
	var outputs []byte
	var err error
	if update.Outputs != nil {
		outputs, err = json.Marshal(update.Outputs)
		if err != nil {
			return fmt.Errorf("encode outputs: %w", err)
		}
	}
	when := update.UpdatedAt.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	var result sql.Result
	if outputs == nil {
		result, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ?, updated_at = ?, revision = revision + 1 WHERE prompt_id = ? OR request_id = ?`, update.Status, update.Error, formatTime(when), id, id)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, outputs_json = ?, error = ?, updated_at = ?, revision = revision + 1 WHERE prompt_id = ? OR request_id = ?`, update.Status, string(outputs), update.Error, formatTime(when), id, id)
	}
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return requireChanged(result)
}

func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE prompt_id = ? OR request_id = ? OR id = ? LIMIT 1`, id, id, id)
	return scanRecord(row)
}

func (s *Store) List(ctx context.Context, options ListOptions) (Page, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	where := []string{"1 = 1"}
	args := make([]any, 0, 8)
	if options.Status != "" {
		where = append(where, "status = ?")
		args = append(args, options.Status)
	}
	if options.ServerID != "" {
		where = append(where, "server_id = ?")
		args = append(args, options.ServerID)
	}
	if options.Cursor != "" {
		cursorTime, cursorID, err := decodeCursor(options.Cursor)
		if err != nil {
			return Page{}, err
		}
		where = append(where, "(submitted_at < ? OR (submitted_at = ? AND id < ?))")
		args = append(args, cursorTime, cursorTime, cursorID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, selectColumns+` WHERE `+strings.Join(where, " AND ")+` ORDER BY submitted_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return Page{}, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	page := Page{Schema: "cmfy/jobs-page-v1", Jobs: make([]Record, 0, limit)}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return Page{}, err
		}
		page.Jobs = append(page.Jobs, record)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list jobs: %w", err)
	}
	if len(page.Jobs) > limit {
		last := page.Jobs[limit-1]
		page.Jobs = page.Jobs[:limit]
		page.NextCursor = encodeCursor(last.SubmittedAt, last.ID)
	}
	return page, nil
}

const selectColumns = `SELECT id, request_id, COALESCE(prompt_id, ''), server_id, workflow, workflow_digest, prompt, parameters_json, inputs_json, status, submitted_at, updated_at, outputs_json, error, revision FROM jobs`

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(row scanner) (Record, error) {
	record := Record{Schema: "cmfy/job-v1"}
	var parameters, inputs, outputs, submittedAt, updatedAt string
	if err := row.Scan(&record.ID, &record.RequestID, &record.PromptID, &record.ServerID, &record.Workflow, &record.WorkflowDigest, &record.Prompt, &parameters, &inputs, &record.Status, &submittedAt, &updatedAt, &outputs, &record.Error, &record.Revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("read job: %w", err)
	}
	if err := json.Unmarshal([]byte(parameters), &record.Parameters); err != nil {
		return Record{}, fmt.Errorf("decode job parameters: %w", err)
	}
	if err := json.Unmarshal([]byte(inputs), &record.Inputs); err != nil {
		return Record{}, fmt.Errorf("decode job inputs: %w", err)
	}
	if err := json.Unmarshal([]byte(outputs), &record.Outputs); err != nil {
		return Record{}, fmt.Errorf("decode job outputs: %w", err)
	}
	var err error
	record.SubmittedAt, err = time.Parse(time.RFC3339Nano, submittedAt)
	if err != nil {
		return Record{}, fmt.Errorf("decode submitted_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Record{}, fmt.Errorf("decode updated_at: %w", err)
	}
	return record, nil
}

func requireChanged(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return "job-" + hex.EncodeToString(bytes[:]), nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func nonNilInputs(value []Input) []Input {
	if value == nil {
		return []Input{}
	}
	return value
}

func nonNilOutputs(value []Output) []Output {
	if value == nil {
		return []Output{}
	}
	return value
}

func encodeCursor(submittedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(formatTime(submittedAt) + "\x00" + id))
}

func decodeCursor(cursor string) (string, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", errors.New("invalid jobs cursor")
	}
	parts := strings.SplitN(string(decoded), "\x00", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("invalid jobs cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return "", "", errors.New("invalid jobs cursor")
	}
	return parts[0], parts[1], nil
}
