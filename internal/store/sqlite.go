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
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("database init: %w", err)
		}
	}
	return nil
}

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
	return s.scanTask(s.db.QueryRowContext(ctx, `
		SELECT id, source, source_type, source_key, status,
		       pikpak_account, pikpak_task_id, pikpak_root_file_id, aria2_instance,
		       error, created_at, updated_at, completed_at
		FROM tasks WHERE id = ?
	`, id))
}

func (s *SQLite) GetTaskBySourceKey(ctx context.Context, key string) (domain.Task, error) {
	return s.scanTask(s.db.QueryRowContext(ctx, `
		SELECT id, source, source_type, source_key, status,
		       pikpak_account, pikpak_task_id, pikpak_root_file_id, aria2_instance,
		       error, created_at, updated_at, completed_at
		FROM tasks WHERE source_key = ?
	`, key))
}

func (s *SQLite) ListTasks(ctx context.Context, limit int) ([]domain.Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, source_type, source_key, status,
		       pikpak_account, pikpak_task_id, pikpak_root_file_id, aria2_instance,
		       error, created_at, updated_at, completed_at
		FROM tasks
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Task
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *SQLite) scanTask(row scanner) (domain.Task, error) {
	return scan(row)
}

func scan(row scanner) (domain.Task, error) {
	var t domain.Task
	var status, createdAt, updatedAt string
	var completedAt sql.NullString
	err := row.Scan(
		&t.ID, &t.Source, &t.SourceType, &t.SourceKey, &status,
		&t.PikPakAccount, &t.PikPakTaskID, &t.PikPakRootFileID, &t.Aria2Instance,
		&t.Error, &createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		return domain.Task{}, err
	}
	t.Status = domain.TaskStatus(status)
	t.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return domain.Task{}, err
	}
	t.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return domain.Task{}, err
	}
	if completedAt.Valid {
		v, err := time.Parse(time.RFC3339Nano, completedAt.String)
		if err != nil {
			return domain.Task{}, err
		}
		t.CompletedAt = &v
	}
	return t, nil
}
