package httpx

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"transola/internal/auth"
	"transola/internal/config"
	"transola/internal/db"
	"transola/internal/meta"
	"transola/internal/storage"
)

// Server exposes HTTP endpoints for CLI and web clients.
type Server struct {
	cfg       config.Config
	store     storage.Backend
	metaStore *db.Store
	logger    *log.Logger
	mux       *http.ServeMux
}

// NewServer wires dependencies and registers handlers.
func NewServer(cfg config.Config, store storage.Backend, metaStore *db.Store, logger *log.Logger) *Server {
	s := &Server{
		cfg:       cfg,
		store:     store,
		metaStore: metaStore,
		logger:    logger,
		mux:       http.NewServeMux(),
	}
	s.register()
	return s
}

// Handler returns the underlying mux for use with http.Server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) register() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/v1/auth/login", s.handleLogin)
	s.mux.Handle("/v1/files", s.requireAuth(http.HandlerFunc(s.handleFilesRoot)))
	s.mux.Handle("/v1/files/", s.requireAuth(http.HandlerFunc(s.handleFilesByID)))
	s.mux.HandleFunc("/download", s.handleDownloadToken)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"status":       "ok",
		"version":      meta.Version,
		"storage_root": s.store.Root(),
		"time":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Printf("health encode error: %v", err)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	user, err := s.metaStore.Authenticate(payload.Email, payload.Password)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := auth.Claims{
		Sub:   user.ID,
		Email: user.Email,
		Role:  user.Role,
		Exp:   time.Now().UTC().Add(24 * time.Hour).Unix(),
	}
	token, err := auth.Sign(claims, s.cfg.JWTSecret)
	if err != nil {
		http.Error(w, "token error", http.StatusInternalServerError)
		return
	}
	resp := map[string]interface{}{
		"access_token": token,
		"user": map[string]string{
			"id":    user.ID,
			"email": user.Email,
			"role":  user.Role,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
	s.metaStore.AddAudit(r.Context(), user.ID, "login", "", "")
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(authz), "bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(authz[len("bearer "):])
		claims, err := auth.Validate(token, s.cfg.JWTSecret)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		user, err := s.metaStore.UserByID(claims.Sub)
		if err != nil {
			http.Error(w, "user not found", http.StatusUnauthorized)
			return
		}
		ctx := auth.WithUser(r.Context(), auth.User{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleFilesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListFiles(w, r)
	case http.MethodPost:
		s.handleUpload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFilesByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(path, "/content") && r.Method == http.MethodGet {
		id := strings.TrimSuffix(path, "/content")
		s.handleDownload(w, r, id)
		return
	}
	if strings.HasSuffix(path, "/link") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(path, "/link")
		s.handleCreateDownloadLink(w, r, id)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	filter := db.FileFilter{}
	q := r.URL.Query()
	if owner := q.Get("owner"); owner != "" && user.Role == "admin" {
		filter.Owner = owner
	}
	if typ := q.Get("type"); typ != "" {
		filter.MimePrefix = mimePrefix(typ)
	} else if mime := q.Get("mime"); mime != "" {
		filter.MimePrefix = mime
	}
	if since := q.Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			filter.Since = &t
		}
	}
	if until := q.Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			filter.Until = &t
		}
	}
	if limitStr := q.Get("limit"); limitStr != "" {
		if lim, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = lim
		}
	}

	files, err := s.metaStore.ListFilesFiltered(user.Role, user.Email, filter)
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	filename := r.Header.Get("X-Filename")
	if filename == "" {
		http.Error(w, "missing X-Filename", http.StatusBadRequest)
		return
	}
	targetOwner := r.Header.Get("X-Owner")
	if targetOwner == "" {
		targetOwner = user.Email
	}
	expectedSHA := r.Header.Get("X-Sha256")
	mime := r.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}

	ref, err := s.store.Save(r.Context(), filename, r.Body, expectedSHA)
	if err != nil {
		http.Error(w, fmt.Sprintf("upload failed: %v", err), http.StatusBadRequest)
		return
	}

	meta := db.File{
		Owner:      targetOwner,
		UploaderID: user.ID,
		Filename:   filename,
		Size:       ref.Size,
		SHA256:     ref.SHA256,
		MIME:       mime,
		StorageKey: ref.Key,
	}
	meta, err = s.metaStore.AddFile(meta)
	if err != nil {
		http.Error(w, "metadata save failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"file": meta,
	})
	s.metaStore.AddAudit(r.Context(), user.ID, "upload", meta.ID, "")
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, id string) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	meta, found := s.metaStore.GetFile(id)
	if !found {
		http.NotFound(w, r)
		return
	}
	if user.Role != "admin" && !strings.EqualFold(user.Email, meta.Owner) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	reader, err := s.store.Open(r.Context(), meta.StorageKey)
	if err != nil {
		http.Error(w, "storage read error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", meta.MIME)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", meta.Size))
	w.Header().Set("X-Checksum-Sha256", meta.SHA256)
	if meta.Filename != "" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+meta.Filename+"\"")
	}
	if _, err := io.Copy(w, reader); err != nil {
		s.logger.Printf("download error for %s: %v", id, err)
	}
	s.metaStore.AddAudit(r.Context(), user.ID, "download", meta.ID, `{"via":"auth"}`)
}

func (s *Server) handleCreateDownloadLink(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	meta, found := s.metaStore.GetFile(id)
	if !found {
		http.NotFound(w, r)
		return
	}
	if user.Role != "admin" && !strings.EqualFold(user.Email, meta.Owner) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	exp := time.Now().UTC().Add(15 * time.Minute)
	claims := auth.Claims{
		Sub:    user.ID,
		Email:  user.Email,
		Role:   user.Role,
		Scope:  "download",
		FileID: meta.ID,
		Exp:    exp.Unix(),
	}
	token, err := auth.Sign(claims, s.cfg.JWTSecret)
	if err != nil {
		http.Error(w, "token error", http.StatusInternalServerError)
		return
	}
	url := requestScheme(r) + "://" + r.Host + "/download?token=" + token
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"token":      token,
		"url":        url,
		"expires_at": exp.Format(time.RFC3339),
	})
	s.metaStore.AddAudit(r.Context(), user.ID, "issue_download_link", meta.ID, "")
}

func (s *Server) handleDownloadToken(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}
	claims, err := auth.Validate(token, s.cfg.JWTSecret)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if claims.Scope != "download" || claims.FileID == "" {
		http.Error(w, "invalid scope", http.StatusUnauthorized)
		return
	}
	meta, found := s.metaStore.GetFile(claims.FileID)
	if !found {
		http.NotFound(w, r)
		return
	}
	reader, err := s.store.Open(r.Context(), meta.StorageKey)
	if err != nil {
		http.Error(w, "storage read error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", meta.MIME)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", meta.Size))
	w.Header().Set("X-Checksum-Sha256", meta.SHA256)
	if meta.Filename != "" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+meta.Filename+"\"")
	}
	if _, err := io.Copy(w, reader); err != nil {
		s.logger.Printf("download token error for %s: %v", claims.FileID, err)
	}
	s.metaStore.AddAudit(r.Context(), claims.Sub, "download", meta.ID, `{"via":"token"}`)
}

func mimePrefix(category string) string {
	switch strings.ToLower(category) {
	case "image", "images":
		return "image/"
	case "video", "videos":
		return "video/"
	case "audio":
		return "audio/"
	case "text":
		return "text/"
	case "doc", "document", "documents":
		return "application/"
	default:
		return category
	}
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return strings.ToLower(strings.Split(proto, ",")[0])
	}
	return "http"
}
