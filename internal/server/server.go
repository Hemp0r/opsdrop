package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"opsdrop/internal/auth"
	"opsdrop/internal/config"
	"opsdrop/internal/db"
)

const (
	authHeaderPrefix   = "Bearer "
	machineHeader      = "X-Client-Machine"
	maxUploadSizeBytes = 1 << 30 // 1 GiB
	maxFormFieldSize   = 1 << 20 // 1 MiB for auxiliary form fields
	publicFileTTL      = 48 * time.Hour
	minPrivateDays     = 1
	maxPrivateDays     = 14
	defaultPrivateDays = 14
	cleanupInterval    = time.Hour
	headerEncrypted    = "X-Opsdrop-Encrypted"
	headerSalt         = "X-Opsdrop-Salt"
	headerNonce        = "X-Opsdrop-Nonce"
	headerChecksum     = "X-Opsdrop-Checksum"
	transferTimeoutDur = 10 * time.Minute
)

// Server wires HTTP routes to application logic.
type Server struct {
	cfg           config.Config
	db            *db.Database
	router        chi.Router
	jwtSecret     []byte
	storageDir    string
	authLimiter   *rateLimiter
	uploadLimiter *rateLimiter
}

// New builds a server instance ready to serve requests.
func New(cfg config.Config, database *db.Database) *Server {
	s := &Server{
		cfg:           cfg,
		db:            database,
		jwtSecret:     cfg.JWTSigningKey,
		storageDir:    cfg.StorageDir,
		authLimiter:   newRateLimiter(10, time.Minute),
		uploadLimiter: newRateLimiter(5, time.Minute),
	}
	s.router = s.buildRouter()
	go s.startCleanupLoop()
	return s
}

// Router exposes the configured router for embedding in an http.Server.
func (s *Server) Router() http.Handler {
	return s.router
}

type uploadData struct {
	Filename   string
	StoredPath string
	Size       int64
	Checksum   string
	Fields     map[string]string
}

func storageSubdir(userID *int64) string {
	if userID != nil {
		return fmt.Sprintf("user_%d", *userID)
	}
	return "public"
}

func (s *Server) consumeUpload(w http.ResponseWriter, r *http.Request, subdir string) (*uploadData, error) {
	limitedBody := http.MaxBytesReader(w, r.Body, maxUploadSizeBytes+1<<20)
	defer limitedBody.Close()
	r.Body = limitedBody

	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("multipart reader: %w", err)
	}

	fields := make(map[string]string)
	storagePath := filepath.Join(s.storageDir, subdir)
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return nil, fmt.Errorf("prepare storage: %w", err)
	}

	var (
		storedFilePath string
		size           int64
		filename       string
		checksum       string
	)

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read part: %w", err)
		}
		name := part.FormName()
		if part.FileName() != "" && filename == "" {
			filename = part.FileName()
			storedFilePath = filepath.Join(storagePath, uuid.New().String())
			out, err := os.Create(storedFilePath)
			if err != nil {
				part.Close()
				return nil, fmt.Errorf("create file: %w", err)
			}
			hasher := sha256.New()
			written, copyErr := io.Copy(io.MultiWriter(out, hasher), part)
			closeErr := out.Close()
			if copyErr != nil {
				part.Close()
				os.Remove(storedFilePath)
				return nil, fmt.Errorf("write file: %w", copyErr)
			}
			if closeErr != nil {
				part.Close()
				os.Remove(storedFilePath)
				return nil, fmt.Errorf("flush file: %w", closeErr)
			}
			size = written
			checksum = hex.EncodeToString(hasher.Sum(nil))
		} else if part.FileName() != "" {
			// Discard additional file parts to avoid duplicate uploads.
			io.Copy(io.Discard, part)
		} else {
			data, err := io.ReadAll(io.LimitReader(part, maxFormFieldSize))
			if err != nil {
				part.Close()
				return nil, fmt.Errorf("read form field: %w", err)
			}
			fields[name] = string(data)
		}
		part.Close()
	}

	if filename == "" {
		return nil, errors.New("missing file field")
	}

	return &uploadData{
		Filename:   filename,
		StoredPath: storedFilePath,
		Size:       size,
		Checksum:   checksum,
		Fields:     fields,
	}, nil
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func isRequestTooLarge(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "http: request body too large")
}

func (s *Server) startCleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		s.cleanupExpiredFiles()
		s.cleanupExpiredRevocations()
		<-ticker.C
	}
}

func (s *Server) cleanupExpiredFiles() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	files, err := s.db.RemoveExpiredFiles(ctx, time.Now())
	if err != nil {
		log.Printf("opsdrop cleanup: failed to remove expired files: %v", err)
		return
	}
	if len(files) == 0 {
		return
	}
	for _, rec := range files {
		if rec.StoredPath != "" {
			if err := os.Remove(rec.StoredPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("opsdrop cleanup: failed to remove file %s: %v", rec.StoredPath, err)
			}
		}
	}
	log.Printf("opsdrop cleanup: removed %d expired entry(ies)", len(files))
}

func (s *Server) cleanupExpiredRevocations() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := s.db.CleanExpiredRevocations(ctx)
	if err != nil {
		log.Printf("opsdrop cleanup: failed to clean expired revocations: %v", err)
		return
	}
	if n > 0 {
		log.Printf("opsdrop cleanup: removed %d expired token revocation(s)", n)
	}
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(s.securityHeaders)

	apiTimeout := middleware.Timeout(60 * time.Second)
	xferTimeout := transferTimeout(transferTimeoutDur)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Get("/.well-known/opsdrop-capabilities", s.handleCapabilities)

	r.Route("/api/v1", func(r chi.Router) {
		r.With(s.authLimiter.middleware, apiTimeout).Post("/auth/register", s.handleRegister)
		r.With(s.authLimiter.middleware, apiTimeout).Post("/auth/login", s.handleLogin)
		r.With(s.authMiddleware, apiTimeout).Post("/auth/logout", s.handleLogout)
		r.With(s.uploadLimiter.middleware, xferTimeout).Post("/public/files", s.handlePublicUpload)

		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.With(apiTimeout).Get("/files", s.handleListFiles)
			r.With(xferTimeout).Post("/files", s.handleUploadFile)
			r.With(xferTimeout).Get("/files/{fileID}", s.handleDownloadFile)
			r.With(apiTimeout).Delete("/files/{fileID}", s.handleDeleteFile)

			r.With(apiTimeout).Post("/clipboard", s.handleCreateClipboard)
			r.With(apiTimeout).Get("/clipboard", s.handleListClipboard)
			r.With(apiTimeout).Get("/clipboard/latest", s.handleGetLatestClipboard)
			r.With(apiTimeout).Get("/clipboard/{entryID}", s.handleGetClipboard)
		})
	})

	r.With(xferTimeout).Get("/public/{token}", s.handlePublicDownload)
	return r
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	caps := map[string]any{
		"auth_enabled":                     true,
		"anonymous_uploads":                true,
		"private_uploads":                  true,
		"public_shares":                    true,
		"self_service_registration":        s.cfg.RegistrationEnabled,
		"default_visibility_authenticated": "private",
		"max_upload_size_bytes":            s.cfg.MaxUploadSizeBytes,
		"default_ttl_seconds":              defaultPrivateDays * 86400,
		"max_ttl_seconds":                  maxPrivateDays * 86400,
	}
	writeJSON(w, http.StatusOK, caps)
}

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Token   string `json:"token"`
	Expires string `json:"expires"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.RegistrationEnabled {
		writeError(w, http.StatusForbidden, "registration is disabled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hashed, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	ctx := r.Context()
	user, err := s.db.CreateUser(ctx, req.Username, hashed)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	s.appendAudit(ctx, &user.ID, clientMachine(r), "register", "user:"+req.Username, "")
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       user.ID,
		"username": user.Username,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	ctx := r.Context()
	user, err := s.db.GetUserByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, db.ErrUserNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	if err := auth.CheckPassword(user.PasswordHash, req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	tokenTTL := 24 * time.Hour
	token, err := auth.GenerateToken(user.ID, user.Username, s.jwtSecret, tokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	s.appendAudit(ctx, &user.ID, clientMachine(r), "login", "auth", "")
	writeJSON(w, http.StatusOK, authResponse{
		Token:   token,
		Expires: time.Now().UTC().Add(tokenTTL).Format(time.RFC3339),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	// Revoke the token so it cannot be reused.
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), authHeaderPrefix)
	if claims, err := auth.ParseToken(raw, s.jwtSecret); err == nil {
		_ = s.db.RevokeToken(r.Context(), raw, claims.ExpiresAt.Time)
	}

	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "logout", "auth", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// AuthenticatedUser is stored on the request context for authorized handlers.
type AuthenticatedUser struct {
	ID       int64
	Username string
}

type contextKey string

const userContextKey contextKey = "auth.user"

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, authHeaderPrefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		token := strings.TrimPrefix(authHeader, authHeaderPrefix)
		if revoked, err := s.db.IsTokenRevoked(r.Context(), token); err != nil {
			writeError(w, http.StatusInternalServerError, "authentication check failed")
			return
		} else if revoked {
			writeError(w, http.StatusUnauthorized, "token revoked")
			return
		}
		claims, err := auth.ParseToken(token, s.jwtSecret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		user, err := s.db.GetUserByID(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, &AuthenticatedUser{
			ID:       user.ID,
			Username: user.Username,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func currentUser(ctx context.Context) *AuthenticatedUser {
	user, _ := ctx.Value(userContextKey).(*AuthenticatedUser)
	return user
}

func clientMachine(r *http.Request) string {
	machine := strings.TrimSpace(r.Header.Get(machineHeader))
	if machine == "" {
		return "unknown"
	}
	if len(machine) > 128 {
		machine = machine[:128]
	}
	var b strings.Builder
	for _, c := range machine {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		}
	}
	result := b.String()
	if result == "" {
		return "unknown"
	}
	return result
}

type fileResponse struct {
	ID              int64   `json:"id"`
	EntryType       string  `json:"entry_type"`
	Filename        string  `json:"filename"`
	Size            int64   `json:"size"`
	IsPublic        bool    `json:"is_public"`
	PublicLink      *string `json:"public_link,omitempty"`
	UploadedAt      string  `json:"uploaded_at"`
	ExpiresAt       string  `json:"expires_at"`
	DownloadLink    string  `json:"download_link"`
	IsEncrypted     bool    `json:"is_encrypted"`
	EncryptionSalt  *string `json:"encryption_salt,omitempty"`
	EncryptionNonce *string `json:"encryption_nonce,omitempty"`
	Checksum        *string `json:"checksum,omitempty"`
}

func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	upload, err := s.consumeUpload(w, r, storageSubdir(&user.ID))
	if err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds 1GB limit")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(upload.StoredPath)
		}
	}()

	isPublic := strings.ToLower(strings.TrimSpace(upload.Fields["public"])) == "true"
	retentionDays := defaultPrivateDays
	if v := strings.TrimSpace(upload.Fields["retention_days"]); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			retentionDays = clampInt(parsed, minPrivateDays, maxPrivateDays)
		}
	}

	expiresAt := time.Now().UTC().Add(time.Duration(retentionDays) * 24 * time.Hour)
	var publicToken *string
	if isPublic {
		expiresAt = time.Now().UTC().Add(publicFileTTL)
		token := uuid.New().String()
		publicToken = &token
	}

	saltValue := strings.TrimSpace(upload.Fields["encryption_salt"])
	nonceValue := strings.TrimSpace(upload.Fields["encryption_nonce"])
	isEncrypted := strings.ToLower(strings.TrimSpace(upload.Fields["encrypted"])) == "true" || (saltValue != "" && nonceValue != "")
	var encryptionSalt, encryptionNonce *string
	if isEncrypted {
		if saltValue == "" || nonceValue == "" {
			writeError(w, http.StatusBadRequest, "missing encryption metadata")
			return
		}
		encryptionSalt = &saltValue
		encryptionNonce = &nonceValue
	}

	rec, err := s.db.CreateFile(r.Context(), &user.ID, upload.Filename, upload.StoredPath, upload.Size, isPublic, publicToken, expiresAt, isEncrypted, encryptionSalt, encryptionNonce, &upload.Checksum)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist file metadata")
		return
	}
	cleanup = false

	var publicLink *string
	if rec.PublicToken.Valid {
		link := fmt.Sprintf("/public/%s", rec.PublicToken.String)
		publicLink = &link
	}

	auditMeta, _ := json.Marshal(map[string]any{
		"filename":   upload.Filename,
		"size":       upload.Size,
		"public":     rec.IsPublic,
		"expires_at": rec.ExpiresAt.Format(time.RFC3339),
		"encrypted":  rec.IsEncrypted,
	})
	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "upload_file", fmt.Sprintf("file:%d", rec.ID), string(auditMeta))

	var encSalt, encNonce *string
	if rec.EncryptionSalt.Valid {
		s := rec.EncryptionSalt.String
		s = strings.TrimSpace(s)
		encSalt = &s
	}
	if rec.EncryptionNonce.Valid {
		s := rec.EncryptionNonce.String
		s = strings.TrimSpace(s)
		encNonce = &s
	}

	writeJSON(w, http.StatusCreated, fileResponse{
		ID:              rec.ID,
		EntryType:       rec.EntryType,
		Filename:        rec.Filename,
		Size:            rec.Size,
		IsPublic:        rec.IsPublic,
		PublicLink:      publicLink,
		UploadedAt:      rec.CreatedAt.Format(time.RFC3339),
		ExpiresAt:       rec.ExpiresAt.Format(time.RFC3339),
		DownloadLink:    fmt.Sprintf("/api/v1/files/%d", rec.ID),
		IsEncrypted:     rec.IsEncrypted,
		EncryptionSalt:  encSalt,
		EncryptionNonce: encNonce,
		Checksum:        &upload.Checksum,
	})
}

func (s *Server) handlePublicUpload(w http.ResponseWriter, r *http.Request) {
	upload, err := s.consumeUpload(w, r, storageSubdir(nil))
	if err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds 1GB limit")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(upload.StoredPath)
		}
	}()

	expiresAt := time.Now().UTC().Add(publicFileTTL)
	token := uuid.New().String()
	saltValue := strings.TrimSpace(upload.Fields["encryption_salt"])
	nonceValue := strings.TrimSpace(upload.Fields["encryption_nonce"])
	isEncrypted := strings.ToLower(strings.TrimSpace(upload.Fields["encrypted"])) == "true" || (saltValue != "" && nonceValue != "")
	var encryptionSalt, encryptionNonce *string
	if isEncrypted {
		if saltValue == "" || nonceValue == "" {
			writeError(w, http.StatusBadRequest, "missing encryption metadata")
			return
		}
		encryptionSalt = &saltValue
		encryptionNonce = &nonceValue
	}

	rec, err := s.db.CreateFile(r.Context(), nil, upload.Filename, upload.StoredPath, upload.Size, true, &token, expiresAt, isEncrypted, encryptionSalt, encryptionNonce, &upload.Checksum)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist file metadata")
		return
	}
	cleanup = false

	link := fmt.Sprintf("/public/%s", rec.PublicToken.String)
	pubAuditMeta, _ := json.Marshal(map[string]any{
		"filename": upload.Filename,
		"size":     upload.Size,
	})
	s.appendAudit(r.Context(), nil, clientMachine(r), "upload_file_public", fmt.Sprintf("file:%d", rec.ID), string(pubAuditMeta))

	var encSalt, encNonce *string
	if rec.EncryptionSalt.Valid {
		s := rec.EncryptionSalt.String
		s = strings.TrimSpace(s)
		encSalt = &s
	}
	if rec.EncryptionNonce.Valid {
		s := rec.EncryptionNonce.String
		s = strings.TrimSpace(s)
		encNonce = &s
	}

	writeJSON(w, http.StatusCreated, fileResponse{
		ID:              rec.ID,
		EntryType:       rec.EntryType,
		Filename:        rec.Filename,
		Size:            rec.Size,
		IsPublic:        true,
		PublicLink:      &link,
		UploadedAt:      rec.CreatedAt.Format(time.RFC3339),
		ExpiresAt:       rec.ExpiresAt.Format(time.RFC3339),
		DownloadLink:    link,
		IsEncrypted:     rec.IsEncrypted,
		EncryptionSalt:  encSalt,
		EncryptionNonce: encNonce,
		Checksum:        &upload.Checksum,
	})
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	files, err := s.db.ListFilesByUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list files")
		return
	}

	resp := make([]fileResponse, 0, len(files))
	for _, f := range files {
		var publicLink *string
		if f.IsPublic && f.PublicToken.Valid {
			link := fmt.Sprintf("/public/%s", f.PublicToken.String)
			publicLink = &link
		}
		var encSalt, encNonce, chk *string
		if f.EncryptionSalt.Valid {
			s := strings.TrimSpace(f.EncryptionSalt.String)
			encSalt = &s
		}
		if f.EncryptionNonce.Valid {
			s := strings.TrimSpace(f.EncryptionNonce.String)
			encNonce = &s
		}
		if f.Checksum.Valid {
			s := f.Checksum.String
			chk = &s
		}
		resp = append(resp, fileResponse{
			ID:              f.ID,
			EntryType:       f.EntryType,
			Filename:        f.Filename,
			Size:            f.Size,
			IsPublic:        f.IsPublic,
			PublicLink:      publicLink,
			UploadedAt:      f.CreatedAt.Format(time.RFC3339),
			ExpiresAt:       f.ExpiresAt.Format(time.RFC3339),
			DownloadLink:    fmt.Sprintf("/api/v1/files/%d", f.ID),
			IsEncrypted:     f.IsEncrypted,
			EncryptionSalt:  encSalt,
			EncryptionNonce: encNonce,
			Checksum:        chk,
		})
	}

	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "list_files", "files", fmt.Sprintf(`{"count":%d}`, len(resp)))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	fileIDParam := chi.URLParam(r, "fileID")
	fileID, err := strconv.ParseInt(fileIDParam, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file id")
		return
	}

	rec, err := s.db.GetFileByID(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, db.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch file")
		return
	}

	if !rec.UserID.Valid || rec.UserID.Int64 != user.ID {
		writeError(w, http.StatusForbidden, "not allowed")
		return
	}
	if time.Now().UTC().After(rec.ExpiresAt) {
		writeError(w, http.StatusNotFound, "file expired")
		return
	}

	if rec.EntryType == db.EntryTypeClipboard {
		s.appendAudit(r.Context(), &user.ID, clientMachine(r), "get_entry", fmt.Sprintf("clipboard:%d", rec.ID), "")
		writeJSON(w, http.StatusOK, map[string]any{
			"id":         rec.ID,
			"entry_type": rec.EntryType,
			"content":    rec.Content.String,
			"created_at": rec.CreatedAt.Format(time.RFC3339),
		})
		return
	}

	f, err := os.Open(rec.StoredPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open file")
		return
	}
	defer f.Close()

	setDownloadHeaders(w, rec)
	http.ServeContent(w, r, rec.Filename, rec.CreatedAt, f)
	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "download_file", fmt.Sprintf("file:%d", rec.ID), "")
}

func (s *Server) handlePublicDownload(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	rec, err := s.db.GetFileByPublicToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, db.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch file")
		return
	}

	f, err := os.Open(rec.StoredPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open file")
		return
	}
	defer f.Close()

	setDownloadHeaders(w, rec)
	http.ServeContent(w, r, rec.Filename, rec.CreatedAt, f)
	s.appendAudit(r.Context(), nil, "public", "download_file_public", fmt.Sprintf("file:%d", rec.ID), "")
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	fileIDParam := chi.URLParam(r, "fileID")
	fileID, err := strconv.ParseInt(fileIDParam, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file id")
		return
	}

	rec, err := s.db.GetFileByID(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, db.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch file")
		return
	}

	if !rec.UserID.Valid || rec.UserID.Int64 != user.ID {
		writeError(w, http.StatusForbidden, "not allowed")
		return
	}

	if err := s.db.DeleteFile(r.Context(), rec.ID); err != nil {
		if errors.Is(err, db.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	if rec.StoredPath != "" {
		if err := os.Remove(rec.StoredPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("opsdrop delete: failed to remove file %s: %v", rec.StoredPath, err)
		}
	}

	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "delete_file", fmt.Sprintf("file:%d", rec.ID), "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type clipboardRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleCreateClipboard(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxFormFieldSize)
	var req clipboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content required")
		return
	}

	entry, err := s.db.CreateClipboardEntry(r.Context(), user.ID, req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store clipboard entry")
		return
	}

	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "clipboard_create", fmt.Sprintf("clipboard:%d", entry.ID), "")
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        entry.ID,
		"createdAt": entry.CreatedAt.Format(time.RFC3339),
	})
}

func (s *Server) handleListClipboard(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())

	limit := 20
	if param := r.URL.Query().Get("limit"); param != "" {
		if parsed, err := strconv.Atoi(param); err == nil && parsed > 0 {
			limit = clampInt(parsed, 1, 100)
		}
	}

	entries, err := s.db.ListClipboardByUser(r.Context(), user.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list clipboard entries")
		return
	}

	type entry struct {
		ID        int64  `json:"id"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}

	resp := make([]entry, 0, len(entries))
	for _, e := range entries {
		resp = append(resp, entry{
			ID:        e.ID,
			Content:   e.Content.String,
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		})
	}

	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "clipboard_list", "clipboard", fmt.Sprintf(`{"count":%d}`, len(resp)))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetClipboard(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	entryIDParam := chi.URLParam(r, "entryID")
	entryID, err := strconv.ParseInt(entryIDParam, 10, 64)
	if err != nil || entryID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid clipboard id")
		return
	}
	entry, err := s.db.GetFileByID(r.Context(), entryID)
	if err != nil {
		if errors.Is(err, db.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "clipboard entry not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch clipboard entry")
		return
	}
	if entry.EntryType != db.EntryTypeClipboard || !entry.UserID.Valid || entry.UserID.Int64 != user.ID {
		writeError(w, http.StatusNotFound, "clipboard entry not found")
		return
	}
	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "clipboard_get", fmt.Sprintf("clipboard:%d", entry.ID), "")
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         entry.ID,
		"content":    entry.Content.String,
		"created_at": entry.CreatedAt.Format(time.RFC3339),
	})
}

func (s *Server) handleGetLatestClipboard(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	entry, err := s.db.GetLatestClipboard(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, db.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "clipboard entry not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch clipboard entry")
		return
	}
	s.appendAudit(r.Context(), &user.ID, clientMachine(r), "clipboard_get_latest", fmt.Sprintf("clipboard:%d", entry.ID), "")
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         entry.ID,
		"content":    entry.Content.String,
		"created_at": entry.CreatedAt.Format(time.RFC3339),
	})
}

func (s *Server) appendAudit(ctx context.Context, userID *int64, machineID, action, resource, metadata string) {
	_ = s.db.CreateAuditRecord(ctx, userID, machineID, action, resource, metadata)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if s.cfg.TLSEnabled {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if result == "" {
		return "download"
	}
	return result
}

// transferTimeout sets a context deadline for long-running file transfers
// without buffering the response body (unlike middleware.Timeout).
func transferTimeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func setDownloadHeaders(w http.ResponseWriter, rec *db.FileRecord) {
	sanitized := sanitizeFilename(rec.Filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", sanitized, url.PathEscape(sanitized)))
	w.Header().Set(headerEncrypted, strconv.FormatBool(rec.IsEncrypted))
	if rec.IsEncrypted {
		if rec.EncryptionSalt.Valid {
			w.Header().Set(headerSalt, strings.TrimSpace(rec.EncryptionSalt.String))
		}
		if rec.EncryptionNonce.Valid {
			w.Header().Set(headerNonce, strings.TrimSpace(rec.EncryptionNonce.String))
		}
	} else {
		w.Header().Del(headerSalt)
		w.Header().Del(headerNonce)
	}
	if rec.Checksum.Valid {
		w.Header().Set(headerChecksum, rec.Checksum.String)
	}
}
