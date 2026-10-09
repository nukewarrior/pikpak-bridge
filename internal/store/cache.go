package store

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/nukewarrior/pikpak-bridge/internal/domain"
)

var (
    ErrTaskNotTerminal = errors.New("only historical (terminal) tasks can be deleted")
    ErrUnsafeDeletion = errors.New("task still owns unverified remote resources; delete is blocked")
    ErrCleanupScheduled = errors.New("remote cleanup scheduled before deleting history")
)

type CacheEntry struct {
    AccountID string `json:"account_id"`
    RootFileID string `json:"root_file_id"`
    SourceKey string `json:"source_key"`
    TaskID string `json:"task_id"`
    SizeBytes int64 `json:"size_bytes"`
    CachedAt time.Time `json:"cached_at"`
    State string `json:"state"`
    LastError string `json:"last_error,omitempty"`
}

func sourceIdentity(sourceKey string) string {
    return strings.SplitN(sourceKey, "#", 2)[0]
}

func terminalTask(status domain.TaskStatus) bool {
    switch status {
    case domain.TaskCompleted, domain.TaskCancelled, domain.TaskPikPakFailed,
        domain.TaskAria2Failed, domain.TaskVerifyFailed, domain.TaskCleanupFailed:
        return true
    }
    return false
}

// CreateTaskFromSource serializes the duplicate decision and task insertion.
// force is only meaningful for historical tasks, never for an active transfer.
func (s *SQLite) CreateTaskFromSource(ctx context.Context, task *domain.Task, force bool) (*domain.Task, error) {
    s.sourceMu.Lock()
    defer s.sourceMu.Unlock()
    base := sourceIdentity(task.SourceKey)
    // Check all attempts: a newer finished attempt must not hide an older
    // still-active attempt of this exact BTIH/source identity.
    var active int
    err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks
       WHERE (source_key=? OR substr(source_key,1,length(?)+1)=? || '#')
         AND status NOT IN ('COMPLETED','CANCELLED','PIKPAK_FAILED','ARIA2_FAILED','VERIFY_FAILED','CLEANUP_FAILED')`,
         base,base,base).Scan(&active)
    if err!=nil {return nil,err}
    existing, err := s.GetTaskBySourceKey(ctx, base)
    if err != nil && !errors.Is(err, sql.ErrNoRows) { return nil, err }
    if active>0 {
        if err==nil {return &existing,nil}
        return nil,ErrDuplicate
    }
    if err == nil {
        if !terminalTask(existing.Status) || !force {
            return &existing, nil
        }
        task.SourceKey = base + "#" + task.ID
        // Reuse verified cloud cache when it belongs to the same exact source.
        cache, err := s.LatestRetainedCache(ctx, base)
        if err != nil { return nil, err }
        if cache != nil {
            task.PikPakAccountID = cache.AccountID
            task.PikPakRootFileID = cache.RootFileID
            task.Status = domain.TaskPikPakComplete
        }
    } else {
        task.SourceKey = base
    }
    if err := s.CreateTask(ctx, *task); err != nil {
        if errors.Is(err, ErrDuplicate) {
            current, lookupErr := s.GetTaskBySourceKey(ctx, base)
            if lookupErr == nil { return &current, nil }
        }
        return nil, err
    }
    return nil, nil
}

func (s *SQLite) LatestRetainedCache(ctx context.Context, base string) (*CacheEntry, error) {
    row := s.db.QueryRowContext(ctx, `SELECT account_id, root_file_id, source_key, task_id,
        size_bytes, cached_at, state, last_error FROM pikpak_cache_entries
        WHERE source_key = ? AND state = 'RETAINED' ORDER BY cached_at DESC LIMIT 1`, base)
    return scanCache(row)
}

func scanCache(row *sql.Row) (*CacheEntry, error) {
    c := &CacheEntry{}
    var at string
    err := row.Scan(&c.AccountID, &c.RootFileID, &c.SourceKey, &c.TaskID, &c.SizeBytes, &at, &c.State, &c.LastError)
    if errors.Is(err, sql.ErrNoRows) { return nil, nil }
    if err != nil { return nil, err }
    c.CachedAt, err = time.Parse(time.RFC3339Nano, at)
    return c, err
}

func (s *SQLite) RecordRetainedCache(ctx context.Context, task *domain.Task, bytes int64) error {
    if task.PikPakAccountID == "" || task.PikPakRootFileID == "" {
        return errors.New("cannot retain PikPak cache without account and root IDs")
    }
    _, err := s.db.ExecContext(ctx, `INSERT INTO pikpak_cache_entries
       (account_id, root_file_id, source_key, task_id, size_bytes, cached_at, state)
       VALUES (?, ?, ?, ?, ?, ?, 'RETAINED')
       ON CONFLICT(account_id, root_file_id) DO NOTHING`,
       task.PikPakAccountID, task.PikPakRootFileID, sourceIdentity(task.SourceKey),
       task.ID, bytes, time.Now().UTC().Format(time.RFC3339Nano))
    return err
}

func (s *SQLite) HasRetainedCache(ctx context.Context, accountID, rootID string) (bool, error) {
    var value int
    err := s.db.QueryRowContext(ctx, `SELECT 1 FROM pikpak_cache_entries
        WHERE account_id=? AND root_file_id=? AND state='RETAINED'`,
        accountID, rootID).Scan(&value)
    if errors.Is(err, sql.ErrNoRows) { return false, nil }
    return value == 1, err
}

// ClaimOldestCache excludes roots used by any running job, even if the parent
// history entry has already been deleted. A DELETING claim survives restarts.
func (s *SQLite) ClaimOldestCache(ctx context.Context, accountID string) (*CacheEntry, error) {
    s.sourceMu.Lock()
    defer s.sourceMu.Unlock()
    row := s.db.QueryRowContext(ctx, `SELECT c.account_id, c.root_file_id,
        c.source_key, c.task_id, c.size_bytes, c.cached_at, c.state, c.last_error
        FROM pikpak_cache_entries c
        WHERE c.account_id = ? AND c.state IN ('RETAINED', 'DELETING', 'ERROR')
          AND (c.retry_at IS NULL OR c.retry_at <= ?)
          AND NOT EXISTS (
            SELECT 1 FROM tasks t
            WHERE t.pikpak_account_id = c.account_id AND t.pikpak_root_file_id = c.root_file_id
              AND t.status NOT IN ('COMPLETED','CANCELLED','PIKPAK_FAILED',
                                   'ARIA2_FAILED','VERIFY_FAILED','CLEANUP_FAILED'))
        ORDER BY CASE WHEN c.state='DELETING' THEN 0 ELSE 1 END, c.cached_at, c.root_file_id LIMIT 1`,
        accountID, time.Now().UTC().Format(time.RFC3339Nano))
    cache, err := scanCache(row)
    if err != nil || cache == nil { return cache, err }
    _, err = s.db.ExecContext(ctx, `UPDATE pikpak_cache_entries SET state='DELETING', last_error='', retry_at=NULL
        WHERE account_id=? AND root_file_id=?`, cache.AccountID, cache.RootFileID)
    return cache, err
}

func (s *SQLite) FinishCacheReclaim(ctx context.Context, cache CacheEntry, reclaimErr error) error {
    if reclaimErr == nil {
        _, err := s.db.ExecContext(ctx, `UPDATE pikpak_cache_entries SET state='RECLAIMED',
            last_error='', retry_at=NULL WHERE account_id=? AND root_file_id=?`,
            cache.AccountID, cache.RootFileID)
        return err
    }
    retryAt := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
    _, err := s.db.ExecContext(ctx, `UPDATE pikpak_cache_entries SET state='ERROR',
        last_error=?, retry_at=? WHERE account_id=? AND root_file_id=?`,
        reclaimErr.Error(), retryAt, cache.AccountID, cache.RootFileID)
    return err
}

func (s *SQLite) DeleteHistoryTask(ctx context.Context, id string) error {
    s.sourceMu.Lock()
    defer s.sourceMu.Unlock()
    task, err := s.GetTask(ctx,id)
    if err!=nil {return err}
    if !terminalTask(task.Status) {return ErrTaskNotTerminal}
    if task.CancelPendingSubmission {return ErrUnsafeDeletion}
    if task.Status!=domain.TaskCompleted && task.Status!=domain.TaskCancelled {
        _,err=s.db.ExecContext(ctx, `UPDATE tasks SET status='CANCELLING',
            pending_delete=1, retry_count=0, error='', next_attempt_at=NULL
            WHERE id=?`,id)
        if err!=nil {return err}
        return ErrCleanupScheduled
    }
    _,err=s.db.ExecContext(ctx,"DELETE FROM tasks WHERE id=?",id)
    return err
}

// Once the cancellation worker has removed owned remote resources, this
// removes the history row (and cascaded downloads/events) if requested.
func (s *SQLite) DeleteCompletedPendingHistory(ctx context.Context, id string) error {
    _,err:=s.db.ExecContext(ctx,`DELETE FROM tasks WHERE id=? AND status='CANCELLED' AND pending_delete=1`,id)
    return err
}
func (s *SQLite) ListPendingHistoryDeletes(ctx context.Context) ([]string,error) {
    rows,err:=s.db.QueryContext(ctx,`SELECT id FROM tasks WHERE status='CANCELLED' AND pending_delete=1 LIMIT 100`)
    if err!=nil {return nil,err}
    defer rows.Close()
    var ids []string
    for rows.Next() {
        var id string
        if err:=rows.Scan(&id);err!=nil{return nil,err}
        ids=append(ids,id)
    }
    return ids,rows.Err()
}

func (s *SQLite) HasPendingCacheReclaim(ctx context.Context, accountID string) (bool, error) {
    var v int
    err := s.db.QueryRowContext(ctx, `SELECT 1 FROM pikpak_cache_entries WHERE account_id=? AND state='DELETING' LIMIT 1`,accountID).Scan(&v)
    if errors.Is(err,sql.ErrNoRows) {return false,nil}
    return v==1,err
}
