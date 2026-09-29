package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/aria2"
	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/pikpak"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type taskStore interface {
	CreateTask(context.Context, domain.Task) error
	GetTask(context.Context, string) (domain.Task, error)
	GetTaskBySourceKey(context.Context, string) (domain.Task, error)
	ListTasks(context.Context, int) ([]domain.Task, error)
	ListDownloads(context.Context, string) ([]domain.Download, error)
}

type accountStatusProvider interface {
	RefreshAccount(context.Context, string) (pikpak.AccountSnapshot, error)
}

type aria2StatusProvider interface {
	Snapshots(context.Context) []aria2.InstanceSnapshot
}

type Option func(*Server)

func WithRuntimeStatus(accountNames []string, accounts accountStatusProvider, aria2Pool aria2StatusProvider) Option {
	return func(s *Server) {
		s.accountNames = append([]string(nil), accountNames...)
		s.accounts = accounts
		s.aria2 = aria2Pool
	}
}

type Server struct {
	store        taskStore
	mux          *http.ServeMux
	accountNames []string
	accounts     accountStatusProvider
	aria2        aria2StatusProvider
}

func New(s taskStore, options ...Option) *Server {
	api := &Server{store: s, mux: http.NewServeMux()}
	for _, option := range options {
		option(api)
	}
	api.routes()
	return api
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v1/status", s.runtimeStatus)
	s.mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	s.mux.HandleFunc("POST /api/v1/tasks/text", s.createTaskText)
	s.mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.getTask)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}/downloads", s.getTaskDownloads)
	s.registerWebUI()
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type createTaskRequest struct {
	URL    string `json:"url"`
	Magnet string `json:"magnet"`
	Hash   string `json:"hash"`
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	input := req.URL
	if input == "" {
		input = req.Magnet
	}
	if input == "" {
		input = req.Hash
	}
	s.createFromSource(w, r, input)
}

func (s *Server) createTaskText(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.createFromSource(w, r, string(body))
}

func (s *Server) createFromSource(w http.ResponseWriter, r *http.Request, input string) {
	n, err := domain.NormalizeSource(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := domain.NewTaskID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate task id")
		return
	}
	now := time.Now().UTC()
	task := domain.Task{
		ID:         id,
		Source:     n.Source,
		SourceType: n.SourceType,
		SourceKey:  n.SourceKey,
		Status:     domain.TaskQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.store.CreateTask(r.Context(), task); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			existing, getErr := s.store.GetTaskBySourceKey(r.Context(), n.SourceKey)
			if getErr != nil {
				writeError(w, http.StatusConflict, "task already exists")
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":            "task already exists",
				"existing_task_id": existing.ID,
				"status":           existing.Status,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "create task")
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get task")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) getTaskDownloads(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.GetTask(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get task")
		return
	}
	downloads, err := s.store.ListDownloads(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list task downloads")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"downloads": downloads})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tasks, err := s.store.ListTasks(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tasks")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

type accountStatusView struct {
	Name           string    `json:"name"`
	Enabled        bool      `json:"enabled"`
	Healthy        bool      `json:"healthy"`
	QuotaRemaining int64     `json:"quota_remaining"`
	QuotaTotal     int64     `json:"quota_total"`
	StorageFree    int64     `json:"storage_free"`
	ActiveJobs     int       `json:"active_jobs"`
	MaxJobs        int       `json:"max_jobs"`
	State          string    `json:"state"`
	CooldownUntil  time.Time `json:"cooldown_until,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type aria2StatusView struct {
	Name      string  `json:"name"`
	Enabled   bool    `json:"enabled"`
	Healthy   bool    `json:"healthy"`
	Active    int     `json:"active"`
	Waiting   int     `json:"waiting"`
	MaxActive int     `json:"max_active"`
	Weight    float64 `json:"weight"`
	Error     string  `json:"error,omitempty"`
}

func (s *Server) runtimeStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	accounts := make([]accountStatusView, len(s.accountNames))
	if s.accounts != nil {
		type result struct {
			index int
			view  accountStatusView
		}
		results := make(chan result, len(s.accountNames))
		for i, name := range s.accountNames {
			go func(i int, name string) {
				snapshot, err := s.accounts.RefreshAccount(ctx, name)
				view := accountStatusView{
					Name:           name,
					Enabled:        snapshot.Enabled,
					Healthy:        snapshot.Healthy,
					QuotaRemaining: snapshot.QuotaRemaining,
					QuotaTotal:     snapshot.QuotaTotal,
					StorageFree:    snapshot.StorageFree,
					ActiveJobs:     snapshot.ActiveJobs,
					MaxJobs:        snapshot.MaxJobs,
					State:          snapshot.State,
					CooldownUntil:  snapshot.CooldownUntil,
				}
				if err != nil {
					view.Error = err.Error()
				}
				results <- result{index: i, view: view}
			}(i, name)
		}
		for remaining := len(s.accountNames); remaining > 0; remaining-- {
			select {
			case item := <-results:
				accounts[item.index] = item.view
			case <-ctx.Done():
				remaining = 0
			}
		}
		for i := range accounts {
			if accounts[i].Name == "" {
				accounts[i] = accountStatusView{
					Name:  s.accountNames[i],
					State: "TIMEOUT",
					Error: "status refresh timed out",
				}
			}
		}
	}

	var instances []aria2StatusView
	if s.aria2 != nil {
		for _, snapshot := range s.aria2.Snapshots(ctx) {
			instances = append(instances, aria2StatusView{
				Name:      snapshot.Name,
				Enabled:   snapshot.Enabled,
				Healthy:   snapshot.Healthy,
				Active:    snapshot.Active,
				Waiting:   snapshot.Waiting,
				MaxActive: snapshot.MaxActive,
				Weight:    snapshot.Weight,
				Error:     snapshot.Error,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pikpak_accounts": accounts,
		"aria2_instances": instances,
		"updated_at":      time.Now().UTC(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
