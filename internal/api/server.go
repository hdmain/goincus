package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/hdmain/goincus/internal/config"
	"github.com/hdmain/goincus/internal/models"
	"github.com/hdmain/goincus/internal/service"
)

var errUnauthorized = errors.New("unauthorized: provide Authorization: Bearer <api_key> or X-API-Key")

// Server exposes the goincus REST API.
type Server struct {
	cfg *config.Config
	svc *service.Service
}

// New constructs the HTTP API server.
func New(cfg *config.Config, svc *service.Service) *Server {
	return &Server{cfg: cfg, svc: svc}
}

// Router returns the chi router with all routes mounted.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))
	r.Use(APIKeyAuth(s.cfg))

	r.Get("/healthz", s.handleHealth)
	r.Get("/api/v1/health", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/instances", func(r chi.Router) {
			r.Get("/", s.handleListInstances)
			r.Post("/", s.handleCreateInstance)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetInstance)
				r.Delete("/", s.handleDeleteInstance)
				r.Post("/start", s.handleStartInstance)
				r.Post("/stop", s.handleStopInstance)
				r.Post("/restart", s.handleRestartInstance)
				r.Post("/repair", s.handleRepairInstance)
				r.Post("/ports", s.handleAddPort)
				r.Delete("/ports/{portID}", s.handleRemovePort)
			})
		})
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Health(r.Context()))
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListInstances(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if list == nil {
		list = []models.Instance{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var req models.CreateInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return
	}
	inst, err := s.svc.CreateInstance(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, inst)
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.svc.GetInstance(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.DeleteInstance(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStartInstance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.svc.StartInstance(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) handleStopInstance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.svc.StopInstance(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) handleRestartInstance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.svc.RestartInstance(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) handleRepairInstance(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.svc.RepairInstance(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) handleAddPort(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req models.AddPortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return
	}
	pm, err := s.svc.AddPortMapping(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, pm)
}

func (s *Server) handleRemovePort(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	portID, err := uuid.Parse(chi.URLParam(r, "portID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid port id"))
		return
	}
	if err := s.svc.RemovePortMapping(r.Context(), id, portID); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, errors.New("invalid instance id")
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, models.ErrorResponse{Error: err.Error()})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, service.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, service.ErrConflict):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}
