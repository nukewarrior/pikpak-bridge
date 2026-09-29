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

	"github.com/nukewarrior/pikpak-bridge/internal/domain"
	"github.com/nukewarrior/pikpak-bridge/internal/store"
)

type taskStore interface {
	CreateTask(context.Context, domain.Task) error
	GetTask(context.Context, string) (domain.Task, error)
	GetTaskBySourceKey(context.Context, string) (domain.Task, error)
	ListTasks(context.Context, int) ([]domain.Task, error)
}

type Server struct {
	store taskStore
	mux   *http.ServeMux
}

func New(s taskStore) *Server {
	api := &Server{store: s, mux: http.NewServeMux()}
	api.routes()
	return api
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	s.mux.HandleFunc("POST /api/v1/tasks/text", s.createTaskText)
	s.mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.getTask)
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

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tasks, err := s.store.ListTasks(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tasks")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
