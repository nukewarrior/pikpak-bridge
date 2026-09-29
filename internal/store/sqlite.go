package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrDuplicate = errors.New("duplicate task")

type SQLite struct {
	db *sql.DB
}

func Open(path string) (*SQLite, error) {
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &SQLite{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) init(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			source_type TEXT NOT NULL,
			source_key TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL,
			pikpak_account TEXT NOT NULL DEFAULT '',
			pikpak_task_id TEXT NOT NULL DEFAULT '',
			pikpak_root_file_id TEXT NOT NULL DEFAULT '',
			aria2_instance TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			completed_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_events_task_id ON events(task_id, id);`,
		`CREATE TABLE IF NOT EXISTS remote_files (
			task_id TEXT NOT NULL,
			pikpak_file_id TEXT NOT NULL,
			parent_file_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			is_folder INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(task_id, pikpak_file_id),
			FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_remote_files_task_id ON remote_files(task_id);`,
		`CREATE TABLE IF NOT EXISTS downloads (
			task_id TEXT NOT NULL,
			pikpak_file_id TEXT NOT NULL,
			aria2_instance TEXT NOT NULL,
			aria2_gid TEXT NOT NULL,
			status TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			expected_size INTEGER NOT NULL DEFAULT 0,
			total_length INTEGER NOT NULL DEFAULT 0,
			completed_length INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(task_id, pikpak_file_id),
			FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_downloads_task_id ON downloads(task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status, next_attempt_at);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("database init: %w", err)
		}
	}

	if err := s.ensureColumn(ctx, "tasks", "retry_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "tasks", "next_attempt_at", "TEXT"); err != nil {
		return err
	}
	return nil
}

func (s *SQLite) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition)
	if err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

const taskColumns = `id, source, source_type, source_key, status,
	pikpak_account, pikpak_task_id, pikpak_root_file_id, aria2_instance,
	retry_count, next_attempt_at, error, created_at, updated_at, completed_at`

func (s *SQLite) CreateTask(ctx context.Context, task domain.Task) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id, source, source_type, source_key, status,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		task.ID, task.Source, task.SourceType, task.SourceKey, string(task.Status),
		task.CreatedAt.UTC().Format(time.RFC3339Nano), task.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

func (s *SQLite) GetTask(ctx context.Context, id string) (domain.Task, error) {
	return scan(s.db.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id = ?", id))
}

func (s *SQLite) GetTaskBySourceKey(ctx context.Context, key string) (domain.Task, error) {
	return scan(s.db.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE source_key = ?", key))
}

func (s *SQLite) ListTasks(ctx context.Context, limit int) ([]domain.Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM tasks ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (s *SQLite) ListPikPakWork(ctx context.Context, now time.Time, limit int) ([]domain.Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskColumns+`
		FROM tasks
		WHERE status IN (?, ?, ?, ?, ?, ?)
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY created_at ASC
		LIMIT ?`,
		string(domain.TaskQueued),
		string(domain.TaskWaitingPikPakAccount),
		string(domain.TaskPikPakSubmitting),
		string(domain.TaskPikPakRunning),
		string(domain.TaskPikPakComplete),
		string(domain.TaskResolvingFiles),
		now.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (s *SQLite) SaveTask(ctx context.Context, task *domain.Task, eventType, message string) error {
	task.UpdatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var nextAttempt any
	if task.NextAttemptAt != nil {
		nextAttempt = task.NextAttemptAt.UTC().Format(time.RFC3339Nano)
	}
	var completedAt any
	if task.CompletedAt != nil {
		completedAt = task.CompletedAt.UTC().Format(time.RFC3339Nano)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET
			status = ?,
			pikpak_account = ?,
			pikpak_task_id = ?,
			pikpak_root_file_id = ?,
			aria2_instance = ?,
			retry_count = ?,
			next_attempt_at = ?,
			error = ?,
			updated_at = ?,
			completed_at = ?
		WHERE id = ?
	`,
		string(task.Status),
		task.PikPakAccount,
		task.PikPakTaskID,
		task.PikPakRootFileID,
		task.Aria2Instance,
		task.RetryCount,
		nextAttempt,
		task.Error,
		task.UpdatedAt.Format(time.RFC3339Nano),
		completedAt,
		task.ID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}

	if eventType != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO events(task_id, type, message, created_at)
			VALUES (?, ?, ?, ?)
		`, task.ID, eventType, message, task.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) ReplaceRemoteFiles(ctx context.Context, taskID string, files []domain.RemoteFile) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM remote_files WHERE task_id = ?", taskID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO remote_files(
			task_id, pikpak_file_id, parent_file_id, name, relative_path, size, is_folder
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, file := range files {
		if _, err := stmt.ExecContext(ctx,
			taskID,
			file.PikPakFileID,
			file.ParentFileID,
			file.Name,
			file.RelativePath,
			file.Size,
			boolInt(file.IsFolder),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) ListRemoteFiles(ctx context.Context, taskID string) ([]domain.RemoteFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, pikpak_file_id, parent_file_id, name, relative_path, size, is_folder
		FROM remote_files
		WHERE task_id = ?
		ORDER BY relative_path ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RemoteFile
	for rows.Next() {
		var file domain.RemoteFile
		var isFolder int
		if err := rows.Scan(
			&file.TaskID,
			&file.PikPakFileID,
			&file.ParentFileID,
			&file.Name,
			&file.RelativePath,
			&file.Size,
			&isFolder,
		); err != nil {
			return nil, err
		}
		file.IsFolder = isFolder != 0
		out = append(out, file)
	}
	return out, rows.Err()
}


func (s *SQLite) ListAria2Work(ctx context.Context, now time.Time, limit int) ([]domain.Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskColumns+`
		FROM tasks
		WHERE status IN (?, ?)
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY created_at ASC
		LIMIT ?`,
		string(domain.TaskWaitingAria2),
		string(domain.TaskAria2Downloading),
		now.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (s *SQLite) EnsureDownloads(ctx context.Context, taskID, instance string, gidFor func(string, string) string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO downloads(
			task_id, pikpak_file_id, aria2_instance, aria2_gid, status,
			relative_path, expected_size, created_at, updated_at
		)
		SELECT task_id, pikpak_file_id, ?, '', ?, relative_path, size, ?, ?
		FROM remote_files
		WHERE task_id = ? AND is_folder = 0
	`, instance, string(domain.DownloadPending), now, now, taskID)
	if err != nil {
		return err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT pikpak_file_id
		FROM downloads
		WHERE task_id = ? AND aria2_gid = ''
	`, taskID)
	if err != nil {
		return err
	}
	var fileIDs []string
	for rows.Next() {
		var fileID string
		if err := rows.Scan(&fileID); err != nil {
			rows.Close()
			return err
		}
		fileIDs = append(fileIDs, fileID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, fileID := range fileIDs {
		gid := gidFor(taskID, fileID)
		if gid == "" {
			return fmt.Errorf("empty aria2 gid for task %s file %s", taskID, fileID)
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE downloads SET aria2_gid = ?, updated_at = ?
			WHERE task_id = ? AND pikpak_file_id = ? AND aria2_gid = ''
		`, gid, now, taskID, fileID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) ListDownloads(ctx context.Context, taskID string) ([]domain.Download, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, pikpak_file_id, aria2_instance, aria2_gid, status,
		       relative_path, expected_size, total_length, completed_length,
		       retry_count, next_attempt_at, last_error, created_at, updated_at
		FROM downloads
		WHERE task_id = ?
		ORDER BY relative_path ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Download
	for rows.Next() {
		var download domain.Download
		var status, createdAt, updatedAt string
		var nextAttempt sql.NullString
		if err := rows.Scan(
			&download.TaskID,
			&download.PikPakFileID,
			&download.Aria2Instance,
			&download.Aria2GID,
			&status,
			&download.RelativePath,
			&download.ExpectedSize,
			&download.TotalLength,
			&download.CompletedLength,
			&download.RetryCount,
			&nextAttempt,
			&download.LastError,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		download.Status = domain.DownloadStatus(status)
		var err error
		download.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		download.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, err
		}
		if nextAttempt.Valid {
			value, err := time.Parse(time.RFC3339Nano, nextAttempt.String)
			if err != nil {
				return nil, err
			}
			download.NextAttemptAt = &value
		}
		out = append(out, download)
	}
	return out, rows.Err()
}

func (s *SQLite) SaveDownload(ctx context.Context, download *domain.Download) error {
	download.UpdatedAt = time.Now().UTC()
	var nextAttempt any
	if download.NextAttemptAt != nil {
		nextAttempt = download.NextAttemptAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE downloads SET
			aria2_instance = ?,
			aria2_gid = ?,
			status = ?,
			relative_path = ?,
			expected_size = ?,
			total_length = ?,
			completed_length = ?,
			retry_count = ?,
			next_attempt_at = ?,
			last_error = ?,
			updated_at = ?
		WHERE task_id = ? AND pikpak_file_id = ?
	`,
		download.Aria2Instance,
		download.Aria2GID,
		string(download.Status),
		download.RelativePath,
		download.ExpectedSize,
		download.TotalLength,
		download.CompletedLength,
		download.RetryCount,
		nextAttempt,
		download.LastError,
		download.UpdatedAt.Format(time.RFC3339Nano),
		download.TaskID,
		download.PikPakFileID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}


func scanTasks(rows *sql.Rows) ([]domain.Task, error) {
	var out []domain.Task
	for rows.Next() {
		task, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (domain.Task, error) {
	var task domain.Task
	var status, createdAt, updatedAt string
	var nextAttemptAt, completedAt sql.NullString
	err := row.Scan(
		&task.ID,
		&task.Source,
		&task.SourceType,
		&task.SourceKey,
		&status,
		&task.PikPakAccount,
		&task.PikPakTaskID,
		&task.PikPakRootFileID,
		&task.Aria2Instance,
		&task.RetryCount,
		&nextAttemptAt,
		&task.Error,
		&createdAt,
		&updatedAt,
		&completedAt,
	)
	if err != nil {
		return domain.Task{}, err
	}
	task.Status = domain.TaskStatus(status)

	task.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return domain.Task{}, err
	}
	task.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return domain.Task{}, err
	}
	if nextAttemptAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, nextAttemptAt.String)
		if err != nil {
			return domain.Task{}, err
		}
		task.NextAttemptAt = &value
	}
	if completedAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, completedAt.String)
		if err != nil {
			return domain.Task{}, err
		}
		task.CompletedAt = &value
	}
	return task, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
