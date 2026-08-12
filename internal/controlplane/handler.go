package controlplane

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	store       *Store
	environment string
	logger      *slog.Logger
}

func NewHandler(store *Store, environment string, logger *slog.Logger) http.Handler {
	return &Handler{store: store, environment: environment, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segments := splitPath(r.URL.Path)
	if len(segments) < 3 || segments[0] != "admin" || segments[1] != "v1" {
		h.writeError(w, ErrNotFound)
		return
	}
	resource := segments[2]
	var result any
	var err error
	status := http.StatusOK

	switch resource {
	case "services":
		result, status, err = h.services(r, segments)
	case "service-versions":
		result, status, err = h.serviceVersions(r, segments)
	case "ports":
		if r.Method == http.MethodGet && len(segments) == 3 {
			result, err = h.store.ListPorts(r.Context(), h.environment, r.URL.Query().Get("status"))
			break
		}
		if r.Method == http.MethodPatch && len(segments) == 4 {
			port, parseErr := strconv.Atoi(segments[3])
			if parseErr != nil {
				err = ErrInvalid
				break
			}
			var input struct {
				Status string `json:"status"`
			}
			if decode(r, &input) != nil {
				err = ErrInvalid
				break
			}
			result, err = h.store.UpdatePortStatus(r.Context(), actor(r), h.environment, port, input.Status)
			break
		}
		err = ErrNotFound
	case "listeners":
		result, status, err = h.listeners(r, segments)
	case "permissions":
		result, status, err = h.permissions(r, segments)
	case "consumers":
		result, status, err = h.consumers(r, segments)
	case "keys":
		result, status, err = h.keys(r, segments)
	case "configurations":
		result, status, err = h.configurations(r, segments)
	case "audit-events", "audit-logs":
		if r.Method != http.MethodGet || len(segments) != 3 {
			err = ErrNotFound
			break
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err = h.store.ListAudit(r.Context(), h.environment, limit)
	case "gateway-instances":
		if r.Method != http.MethodGet || len(segments) != 3 {
			err = ErrNotFound
			break
		}
		result, err = h.store.ListGatewayInstances(r.Context(), h.environment)
	default:
		err = ErrNotFound
	}
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, status, result)
}

func (h *Handler) services(r *http.Request, segments []string) (any, int, error) {
	if len(segments) == 3 {
		switch r.Method {
		case http.MethodGet:
			items, err := h.store.ListServices(r.Context(), h.environment)
			return items, http.StatusOK, err
		case http.MethodPost:
			var input struct {
				Environment string `json:"environment"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
				Owner       string `json:"owner"`
			}
			if err := decode(r, &input); err != nil || !h.validEnvironment(input.Environment) {
				return nil, 0, ErrInvalid
			}
			item, err := h.store.CreateService(r.Context(), actor(r), h.environment, input.Name, input.DisplayName, input.Owner)
			return item, http.StatusCreated, err
		}
	}
	if len(segments) == 5 && segments[4] == "versions" && r.Method == http.MethodGet {
		items, err := h.store.ListServiceVersions(r.Context(), h.environment, segments[3])
		return items, http.StatusOK, err
	}
	if len(segments) == 4 && r.Method == http.MethodPatch {
		var input struct {
			DisplayName     string `json:"displayName"`
			Owner           string `json:"owner"`
			Enabled         bool   `json:"enabled"`
			ExpectedVersion int64  `json:"expectedVersion"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdateService(r.Context(), actor(r), h.environment, segments[3], input.DisplayName, input.Owner, input.Enabled, input.ExpectedVersion)
		return item, http.StatusOK, err
	}
	if len(segments) == 5 && segments[4] == "versions" && r.Method == http.MethodPost {
		var input struct {
			Environment      string `json:"environment"`
			Version          string `json:"version"`
			HealthCheckPath  string `json:"healthCheckPath"`
			RequestTimeoutMS int    `json:"requestTimeoutMs"`
			MaxRequestBytes  int64  `json:"maxRequestBytes"`
		}
		if err := decode(r, &input); err != nil || !h.validEnvironment(input.Environment) {
			return nil, 0, ErrInvalid
		}
		if input.HealthCheckPath == "" {
			input.HealthCheckPath = "/health"
		}
		if input.RequestTimeoutMS == 0 {
			input.RequestTimeoutMS = 30000
		}
		if input.MaxRequestBytes == 0 {
			input.MaxRequestBytes = 2097152
		}
		item, err := h.store.CreateServiceVersion(r.Context(), actor(r), h.environment, segments[3], input.Version, input.HealthCheckPath, input.RequestTimeoutMS, input.MaxRequestBytes)
		return item, http.StatusCreated, err
	}
	return nil, 0, ErrNotFound
}

func (h *Handler) serviceVersions(r *http.Request, segments []string) (any, int, error) {
	if len(segments) == 4 && r.Method == http.MethodPatch {
		var input struct {
			Version          string `json:"version"`
			HealthCheckPath  string `json:"healthCheckPath"`
			RequestTimeoutMS int    `json:"requestTimeoutMs"`
			MaxRequestBytes  int64  `json:"maxRequestBytes"`
			Enabled          bool   `json:"enabled"`
			ExpectedVersion  int64  `json:"expectedVersion"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdateServiceVersion(r.Context(), actor(r), h.environment, segments[3], input.Version, input.HealthCheckPath, input.RequestTimeoutMS, input.MaxRequestBytes, input.Enabled, input.ExpectedVersion)
		return item, http.StatusOK, err
	}
	if len(segments) == 6 && segments[4] == "upstreams" && r.Method == http.MethodPatch {
		var input struct {
			Scheme          string `json:"scheme"`
			Address         string `json:"address"`
			Port            int    `json:"port"`
			Weight          int    `json:"weight"`
			HealthCheckPath string `json:"healthCheckPath"`
			Enabled         bool   `json:"enabled"`
			ExpectedVersion int64  `json:"expectedVersion"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdateUpstream(r.Context(), actor(r), h.environment, segments[5], input.Scheme, input.Address, input.Port, input.Weight, input.HealthCheckPath, input.Enabled, input.ExpectedVersion)
		return item, http.StatusOK, err
	}
	if len(segments) != 5 || segments[4] != "upstreams" {
		return nil, 0, ErrNotFound
	}
	if r.Method == http.MethodGet {
		items, err := h.store.ListUpstreams(r.Context(), h.environment, segments[3])
		return items, http.StatusOK, err
	}
	if r.Method != http.MethodPost {
		return nil, 0, ErrNotFound
	}
	var input struct {
		Scheme          string `json:"scheme"`
		Address         string `json:"address"`
		Port            int    `json:"port"`
		Weight          int    `json:"weight"`
		HealthCheckPath string `json:"healthCheckPath"`
	}
	if err := decode(r, &input); err != nil {
		return nil, 0, ErrInvalid
	}
	if input.Weight == 0 {
		input.Weight = 100
	}
	if input.Scheme == "" {
		input.Scheme = "http"
	}
	if input.HealthCheckPath == "" {
		input.HealthCheckPath = "/health"
	}
	item, err := h.store.CreateUpstreamWithScheme(r.Context(), actor(r), h.environment, segments[3], input.Scheme, input.Address, input.Port, input.Weight, input.HealthCheckPath)
	return item, http.StatusCreated, err
}

func (h *Handler) listeners(r *http.Request, segments []string) (any, int, error) {
	if len(segments) == 3 && r.Method == http.MethodGet {
		items, err := h.store.ListListeners(r.Context(), h.environment)
		return items, http.StatusOK, err
	}
	if len(segments) == 3 && r.Method == http.MethodPost || len(segments) == 4 && segments[3] == "allocate-port" && r.Method == http.MethodPost {
		var input struct {
			Environment             string   `json:"environment"`
			Service                 string   `json:"service"`
			ServiceVersion          string   `json:"serviceVersion"`
			PreferredPort           *int     `json:"preferredPort"`
			RequiredPermission      string   `json:"requiredPermission"`
			AllowedMethods          []string `json:"allowedMethods"`
			AllowedContentTypes     []string `json:"allowedContentTypes"`
			UnprotectedRouteRegex   string   `json:"unprotectedRouteRegex"`
			AuthenticationPolicy    string   `json:"authenticationPolicy"`
			RateLimitPerSecond      int      `json:"rateLimitPerSecond"`
			RateLimitBurst          int      `json:"rateLimitBurst"`
			QuotaRequestsPerMinute  int      `json:"quotaRequestsPerMinute"`
			CircuitFailureThreshold int      `json:"circuitFailureThreshold"`
			CircuitOpenMS           int      `json:"circuitOpenMs"`
		}
		if err := decode(r, &input); err != nil || !h.validEnvironment(input.Environment) || input.AuthenticationPolicy != "" && input.AuthenticationPolicy != "api-key-required" {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.AllocateListener(r.Context(), actor(r), h.environment, AllocateListenerInput{Service: input.Service, ServiceVersion: input.ServiceVersion, PreferredPort: input.PreferredPort, RequiredPermission: input.RequiredPermission, AllowedMethods: input.AllowedMethods, AllowedContentTypes: input.AllowedContentTypes, UnprotectedRouteRegex: input.UnprotectedRouteRegex, RateLimitPerSecond: input.RateLimitPerSecond, RateLimitBurst: input.RateLimitBurst, QuotaRequestsPerMinute: input.QuotaRequestsPerMinute, CircuitFailureThreshold: input.CircuitFailureThreshold, CircuitOpenMS: input.CircuitOpenMS})
		return item, http.StatusCreated, err
	}
	if len(segments) == 4 && r.Method == http.MethodPatch {
		var input struct {
			Status          string `json:"status"`
			ExpectedVersion int64  `json:"expectedVersion"`
		}
		if err := decode(r, &input); err != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdateListener(r.Context(), actor(r), h.environment, segments[3], input.Status, input.ExpectedVersion)
		return item, http.StatusOK, err
	}
	if len(segments) == 5 && segments[4] == "policies" && r.Method == http.MethodPatch {
		var input struct {
			RateLimitPerSecond      int    `json:"rateLimitPerSecond"`
			RateLimitBurst          int    `json:"rateLimitBurst"`
			QuotaRequestsPerMinute  int    `json:"quotaRequestsPerMinute"`
			CircuitFailureThreshold int    `json:"circuitFailureThreshold"`
			CircuitOpenMS           int    `json:"circuitOpenMs"`
			UnprotectedRouteRegex   string `json:"unprotectedRouteRegex"`
			ExpectedVersion         int64  `json:"expectedVersion"`
		}
		if err := decode(r, &input); err != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdateListenerPolicies(r.Context(), actor(r), h.environment, segments[3], ListenerPolicyInput{RateLimitPerSecond: input.RateLimitPerSecond, RateLimitBurst: input.RateLimitBurst, QuotaRequestsPerMinute: input.QuotaRequestsPerMinute, CircuitFailureThreshold: input.CircuitFailureThreshold, CircuitOpenMS: input.CircuitOpenMS, UnprotectedRouteRegex: input.UnprotectedRouteRegex, ExpectedVersion: input.ExpectedVersion})
		return item, http.StatusOK, err
	}
	return nil, 0, ErrNotFound
}

func (h *Handler) permissions(r *http.Request, segments []string) (any, int, error) {
	if len(segments) == 3 && r.Method == http.MethodGet {
		items, err := h.store.ListPermissions(r.Context())
		return items, http.StatusOK, err
	}
	if len(segments) == 3 && r.Method == http.MethodPost {
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.CreatePermission(r.Context(), actor(r), h.environment, input.Name, input.Description)
		return item, http.StatusCreated, err
	}
	if len(segments) == 4 && r.Method == http.MethodPatch {
		var input struct {
			Description string `json:"description"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdatePermission(r.Context(), actor(r), h.environment, segments[3], input.Description)
		return item, http.StatusOK, err
	}
	if len(segments) == 4 && r.Method == http.MethodDelete {
		err := h.store.DeletePermission(r.Context(), actor(r), h.environment, segments[3])
		return map[string]string{"status": "deleted"}, http.StatusOK, err
	}
	return nil, 0, ErrNotFound
}

func (h *Handler) consumers(r *http.Request, segments []string) (any, int, error) {
	if len(segments) == 3 {
		if r.Method == http.MethodGet {
			items, err := h.store.ListConsumers(r.Context(), h.environment)
			return items, http.StatusOK, err
		}
		if r.Method == http.MethodPost {
			var input struct {
				Environment string   `json:"environment"`
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Permissions []string `json:"permissions"`
			}
			if decode(r, &input) != nil || !h.validEnvironment(input.Environment) {
				return nil, 0, ErrInvalid
			}
			item, err := h.store.CreateConsumer(r.Context(), actor(r), h.environment, input.Name, input.DisplayName, input.Permissions)
			return item, http.StatusCreated, err
		}
	}
	if len(segments) == 4 && r.Method == http.MethodPatch {
		var input struct {
			DisplayName     string   `json:"displayName"`
			Enabled         bool     `json:"enabled"`
			Permissions     []string `json:"permissions"`
			ExpectedVersion int64    `json:"expectedVersion"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.UpdateConsumer(r.Context(), actor(r), h.environment, segments[3], input.DisplayName, input.Enabled, input.Permissions, input.ExpectedVersion)
		return item, http.StatusOK, err
	}
	if len(segments) == 4 && r.Method == http.MethodDelete {
		err := h.store.DeleteConsumer(r.Context(), actor(r), h.environment, segments[3])
		return map[string]string{"status": "deleted"}, http.StatusOK, err
	}
	if len(segments) == 5 && segments[4] == "keys" && r.Method == http.MethodGet {
		items, err := h.store.ListKeys(r.Context(), h.environment, segments[3])
		return items, http.StatusOK, err
	}
	if len(segments) == 5 && segments[4] == "keys" && r.Method == http.MethodPost {
		var input struct {
			Name        string     `json:"name"`
			Permissions []string   `json:"permissions"`
			ExpiresAt   *time.Time `json:"expiresAt"`
		}
		if decode(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, err := h.store.IssueKey(r.Context(), actor(r), h.environment, segments[3], input.Name, input.Permissions, input.ExpiresAt, nil)
		return item, http.StatusCreated, err
	}
	return nil, 0, ErrNotFound
}

func (h *Handler) keys(r *http.Request, segments []string) (any, int, error) {
	if len(segments) != 5 || r.Method != http.MethodPost {
		return nil, 0, ErrNotFound
	}
	switch segments[4] {
	case "rotate":
		item, err := h.store.RotateKey(r.Context(), actor(r), h.environment, segments[3])
		return item, http.StatusCreated, err
	case "revoke":
		err := h.store.RevokeKey(r.Context(), actor(r), h.environment, segments[3])
		return map[string]string{"status": "revoked"}, http.StatusOK, err
	default:
		return nil, 0, ErrNotFound
	}
}

func (h *Handler) configurations(r *http.Request, segments []string) (any, int, error) {
	if len(segments) == 3 {
		if r.Method == http.MethodGet {
			items, err := h.store.ListConfigurations(r.Context(), h.environment)
			return items, http.StatusOK, err
		}
		if r.Method == http.MethodPost {
			item, err := h.store.CreateConfiguration(r.Context(), actor(r), h.environment)
			return item, http.StatusCreated, err
		}
	}
	if len(segments) != 5 || r.Method != http.MethodPost {
		return nil, 0, ErrNotFound
	}
	id, err := strconv.ParseInt(segments[3], 10, 64)
	if err != nil {
		return nil, 0, ErrInvalid
	}
	switch segments[4] {
	case "validate":
		item, storeErr := h.store.ValidateConfiguration(r.Context(), actor(r), h.environment, id)
		return item, http.StatusOK, storeErr
	case "activate":
		var input struct {
			ExpectedActiveVersion *int64 `json:"expectedActiveVersion"`
		}
		if decodeOptional(r, &input) != nil {
			return nil, 0, ErrInvalid
		}
		item, storeErr := h.store.ActivateConfiguration(r.Context(), actor(r), h.environment, id, input.ExpectedActiveVersion)
		return item, http.StatusOK, storeErr
	case "approve":
		item, storeErr := h.store.ApproveConfiguration(r.Context(), actor(r), h.environment, id)
		return item, http.StatusCreated, storeErr
	case "rollback":
		item, storeErr := h.store.RollbackConfiguration(r.Context(), actor(r), h.environment, id)
		return item, http.StatusCreated, storeErr
	default:
		return nil, 0, ErrNotFound
	}
}

func (h *Handler) validEnvironment(input string) bool { return input == "" || input == h.environment }
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
		message = "invalid request"
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
		message = "not found"
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
		message = "resource is in use or has changed"
	}
	if status == http.StatusInternalServerError {
		h.logger.Error("control-plane request failed", "error", err)
	}
	writeJSON(w, status, map[string]string{"error": message})
}
func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}
func decodeOptional(r *http.Request, target any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	return decode(r, target)
}
func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
func actor(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Admin-Actor"))
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n") {
		return "bootstrap-admin"
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
