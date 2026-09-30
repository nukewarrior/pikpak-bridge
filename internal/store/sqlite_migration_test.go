package store

import (
	"database/sql"
	"testing"
)

func TestOpenMigratesRetryColumns(t *testing.T) {
	path := t.TempDir() + "/legacy.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			source_type TEXT NOT NULL,
			source_key TEXT NOT NULL UNIQUE,
			target_id TEXT NOT NULL,
			target_name TEXT NOT NULL,
			aria2_instance_id TEXT NOT NULL,
			download_dir TEXT NOT NULL,
			status TEXT NOT NULL,
			pikpak_account_id TEXT NOT NULL DEFAULT '',
			pikpak_task_id TEXT NOT NULL DEFAULT '',
			pikpak_root_file_id TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			completed_at TEXT
		);
		CREATE TABLE downloads (
			task_id TEXT NOT NULL,
			pikpak_file_id TEXT NOT NULL,
			aria2_instance_id TEXT NOT NULL,
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
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for table, column := range map[string]string{
		"tasks":     "manual_retry_count",
		"downloads": "eof_retry_count",
	} {
		rows, err := store.db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if name == column {
				found = true
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatalf("%s missing migrated column %s", table, column)
		}
	}
}
