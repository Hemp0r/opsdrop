// Package client provides a reusable API client for the OpsDrop server.
// It is used by the Cobra CLI commands and the interactive TUI.
package client

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/scrypt"
)

const (
	ConfigDirName  = ".opsdrop"
	ConfigFileName = "config.json"
	MachineHeader  = "X-Client-Machine"
	ServerURLEnv   = "SERVER_URL"

	DefaultRemoteURL = "https://opsdrop.hemp0r.dev"

	DefaultRetentionDays = 14
	MinRetentionDays     = 1
	MaxRetentionDays     = 14

	EncryptionSaltSize  = 16
	EncryptionNonceSize = 12
	EncryptionMACSize   = 32
	scryptN             = 1 << 15
	scryptR             = 8
	scryptP             = 1

	DownloadHeaderEncrypted = "X-Opsdrop-Encrypted"
	DownloadHeaderSalt      = "X-Opsdrop-Salt"
	DownloadHeaderNonce     = "X-Opsdrop-Nonce"
	DownloadHeaderChecksum  = "X-Opsdrop-Checksum"
)

// ServerCapabilities describes features advertised by the configured remote.
type ServerCapabilities struct {
	AuthEnabled                    bool   `json:"auth_enabled"`
	AnonymousUploads               bool   `json:"anonymous_uploads"`
	PrivateUploads                 bool   `json:"private_uploads"`
	PublicShares                   bool   `json:"public_shares"`
	SelfServiceRegistration        bool   `json:"self_service_registration"`
	DefaultVisibilityAuthenticated string `json:"default_visibility_authenticated"`
	MaxUploadSizeBytes             int64  `json:"max_upload_size_bytes"`
	DefaultTTLSeconds              int64  `json:"default_ttl_seconds"`
	MaxTTLSeconds                  int64  `json:"max_ttl_seconds"`
}

// Config holds the CLI configuration persisted to disk.
type Config struct {
	RemoteURL             string              `json:"remote_url,omitempty"`
	Token                 string              `json:"token,omitempty"`
	MachineID             string              `json:"machine_id"`
	SkipTLSVerify         bool                `json:"skip_tls_verify,omitempty"`
	LastLoginUser         string              `json:"last_login_user,omitempty"`
	LastConfigured        string              `json:"last_configured"`
	Capabilities          *ServerCapabilities `json:"capabilities,omitempty"`
	CapabilitiesFetchedAt string              `json:"capabilities_fetched_at,omitempty"`
}

// FileInfo represents a file returned by the server.
type FileInfo struct {
	ID              int64   `json:"id"`
	EntryType       string  `json:"entry_type"`
	Filename        string  `json:"filename"`
	Size            int64   `json:"size"`
	IsPublic        bool    `json:"is_public"`
	PublicLink      *string `json:"public_link"`
	UploadedAt      string  `json:"uploaded_at"`
	ExpiresAt       string  `json:"expires_at"`
	DownloadLink    string  `json:"download_link"`
	IsEncrypted     bool    `json:"is_encrypted"`
	EncryptionSalt  *string `json:"encryption_salt"`
	EncryptionNonce *string `json:"encryption_nonce"`
	Checksum        *string `json:"checksum,omitempty"`
}

// ClipboardEntry represents a clipboard entry returned by the server.
type ClipboardEntry struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// EncryptionOptions holds derived encryption parameters for upload.
type EncryptionOptions struct {
	Password string
	Salt     []byte
	Nonce    []byte
	SaltB64  string
	NonceB64 string
}

// DownloadResult holds a successful download response before it is written to disk.
type DownloadResult struct {
	Filename  string
	EntryType string
	Content   string // populated for clipboard entries
	Encrypted bool
	SaltB64   string
	NonceB64  string
	Checksum  string
	Body      io.ReadCloser
}

// Client wraps the OpsDrop HTTP API.
type Client struct {
	Cfg        *Config
	httpClient *http.Client
}

// ConfigPathOverride allows commands to specify a non-default config file.
var ConfigPathOverride string

// SkipChecksum disables checksum computation and verification when true.
var SkipChecksum bool

// Insecure is set by the global --insecure flag to skip TLS verification at runtime.
var Insecure bool

// New creates a Client from the given Config.
func New(cfg *Config) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.SkipTLSVerify || Insecure {
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // explicit user opt-in
	}
	return &Client{
		Cfg: cfg,
		httpClient: &http.Client{
			Transport: tr,
		},
	}
}

// ---------- config helpers ----------

func ConfigPath() (string, error) {
	if strings.TrimSpace(ConfigPathOverride) != "" {
		resolved, err := ExpandPath(ConfigPathOverride)
		if err != nil {
			return "", err
		}
		dir := filepath.Dir(resolved)
		if dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
		}
		return resolved, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ConfigDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigFileName), nil
}

func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg.MachineID = uuid.New().String()
		cfg.LastConfigured = time.Now().UTC().Format(time.RFC3339)
		if err := SaveConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.MachineID == "" {
		cfg.MachineID = uuid.New().String()
	}
	return cfg, nil
}

func SaveConfig(cfg *Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	cfg.LastConfigured = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ExpandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		suffix := strings.TrimPrefix(path, "~")
		suffix = strings.TrimPrefix(suffix, string(os.PathSeparator))
		path = filepath.Join(home, suffix)
	}
	return filepath.Abs(path)
}

func ResolveServerURL(explicit string, cfg *Config) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return strings.TrimRight(v, "/")
	}
	if env := strings.TrimSpace(os.Getenv(ServerURLEnv)); env != "" {
		return strings.TrimRight(env, "/")
	}
	if cfg != nil {
		if v := strings.TrimSpace(cfg.RemoteURL); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return DefaultRemoteURL
}

func ClampRetentionDays(value int) int {
	if value < MinRetentionDays {
		return MinRetentionDays
	}
	if value > MaxRetentionDays {
		return MaxRetentionDays
	}
	return value
}

// ---------- auth ----------

func (c *Client) EnsureToken() error {
	if strings.TrimSpace(c.Cfg.Token) == "" {
		return errors.New("not logged in; run 'opsdrop auth login'")
	}
	return nil
}

func (c *Client) Login(ctx context.Context, username, password string) (token string, expires string, err error) {
	payload := map[string]string{"username": username, "password": password}
	body, _ := json.Marshal(payload)
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var data struct {
		Token   string `json:"token"`
		Expires string `json:"expires"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}
	return data.Token, data.Expires, nil
}

func (c *Client) Logout(ctx context.Context) error {
	if strings.TrimSpace(c.Cfg.Token) == "" {
		return nil
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/auth/logout", nil)
	if err != nil {
		return nil // best-effort
	}
	resp, err := c.do(req)
	if err != nil {
		return nil // best-effort
	}
	resp.Body.Close()
	return nil
}

func (c *Client) Register(ctx context.Context, username, password string) error {
	payload := map[string]string{"username": username, "password": password}
	body, _ := json.Marshal(payload)
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------- files ----------

func (c *Client) ListFiles(ctx context.Context) ([]FileInfo, error) {
	if err := c.EnsureToken(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/files", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var files []FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

func (c *Client) UploadFile(ctx context.Context, filePath string, public bool, retentionDays int, encOpts *EncryptionOptions) (*FileInfo, error) {
	loggedIn := strings.TrimSpace(c.Cfg.Token) != ""
	if !loggedIn {
		public = true
	}
	if encOpts != nil && public {
		return nil, errors.New("cannot encrypt a public upload")
	}

	var endpoint string
	fields := make(map[string]string)

	if public {
		endpoint = "/api/v1/public/files"
		fields["public"] = "true"
	} else {
		if err := c.EnsureToken(); err != nil {
			return nil, err
		}
		endpoint = "/api/v1/files"
		retention := ClampRetentionDays(retentionDays)
		fields["public"] = "false"
		fields["retention_days"] = strconv.Itoa(retention)
		if encOpts != nil {
			fields["encrypted"] = "true"
			fields["encryption_salt"] = encOpts.SaltB64
			fields["encryption_nonce"] = encOpts.NonceB64
		} else {
			fields["encrypted"] = "false"
		}
	}

	return c.performUpload(ctx, filePath, endpoint, fields, encOpts)
}

func (c *Client) DownloadFile(ctx context.Context, id int64) (*DownloadResult, error) {
	if err := c.EnsureToken(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/files/%d", id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}

	// If server returns JSON (clipboard entry), parse it.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		defer resp.Body.Close()
		var data struct {
			ID        int64  `json:"id"`
			EntryType string `json:"entry_type"`
			Content   string `json:"content"`
			CreatedAt string `json:"created_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return nil, fmt.Errorf("decode clipboard response: %w", err)
		}
		return &DownloadResult{
			EntryType: data.EntryType,
			Content:   data.Content,
		}, nil
	}

	filename := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if name := ParseFilename(cd); name != "" {
			filename = filepath.Base(name)
		}
	}
	if filename == "" {
		filename = fmt.Sprintf("file_%d", id)
	}

	encrypted := strings.EqualFold(resp.Header.Get(DownloadHeaderEncrypted), "true")
	return &DownloadResult{
		Filename:  filename,
		EntryType: "file",
		Encrypted: encrypted,
		SaltB64:   strings.TrimSpace(resp.Header.Get(DownloadHeaderSalt)),
		NonceB64:  strings.TrimSpace(resp.Header.Get(DownloadHeaderNonce)),
		Checksum:  strings.TrimSpace(resp.Header.Get(DownloadHeaderChecksum)),
		Body:      resp.Body,
	}, nil
}

// DownloadPublicFile fetches a publicly shared file by its token (no auth required).
func (c *Client) DownloadPublicFile(ctx context.Context, token string) (*DownloadResult, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/public/%s", token), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}

	filename := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if name := ParseFilename(cd); name != "" {
			filename = filepath.Base(name)
		}
	}
	if filename == "" {
		filename = "download"
	}

	encrypted := strings.EqualFold(resp.Header.Get(DownloadHeaderEncrypted), "true")
	return &DownloadResult{
		Filename:  filename,
		EntryType: "file",
		Encrypted: encrypted,
		SaltB64:   strings.TrimSpace(resp.Header.Get(DownloadHeaderSalt)),
		NonceB64:  strings.TrimSpace(resp.Header.Get(DownloadHeaderNonce)),
		Checksum:  strings.TrimSpace(resp.Header.Get(DownloadHeaderChecksum)),
		Body:      resp.Body,
	}, nil
}

func (c *Client) DeleteFile(ctx context.Context, id int64) error {
	if err := c.EnsureToken(); err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/files/%d", id), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ComputeFileChecksum computes the SHA-256 hash of a file and returns the hex string.
func ComputeFileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// UploadDirectory zips a directory into a temporary file and uploads it.
func (c *Client) UploadDirectory(ctx context.Context, dirPath string, public bool, retentionDays int, encOpts *EncryptionOptions) (*FileInfo, error) {
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dirPath)
	}

	zipName := filepath.Base(dirPath) + ".zip"
	tmp, err := os.CreateTemp("", "opsdrop-dir-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create temp zip: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	zw := zip.NewWriter(tmp)
	baseDir := dirPath

	err = filepath.Walk(baseDir, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		zw.Close()
		tmp.Close()
		return nil, fmt.Errorf("zip directory: %w", err)
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("finalize zip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close temp zip: %w", err)
	}

	// Rename temp to use the proper zip name in the same directory so upload picks it up
	namedPath := filepath.Join(filepath.Dir(tmpPath), zipName)
	if err := os.Rename(tmpPath, namedPath); err != nil {
		// Fall back to uploading from tmpPath if rename fails
		namedPath = tmpPath
	}
	defer os.Remove(namedPath)

	return c.UploadFile(ctx, namedPath, public, retentionDays, encOpts)
}

// ---------- clipboard ----------

func (c *Client) ClipboardSend(ctx context.Context, content string) (*ClipboardEntry, error) {
	if err := c.EnsureToken(); err != nil {
		return nil, err
	}
	payload := map[string]string{"content": content}
	body, _ := json.Marshal(payload)
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/clipboard", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var entry ClipboardEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, fmt.Errorf("decode clipboard response: %w", err)
	}
	return &entry, nil
}

func (c *Client) ClipboardList(ctx context.Context, limit int) ([]ClipboardEntry, error) {
	if err := c.EnsureToken(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/clipboard?limit=%d", limit), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var entries []ClipboardEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Client) ClipboardGet(ctx context.Context, id int64) (*ClipboardEntry, error) {
	if err := c.EnsureToken(); err != nil {
		return nil, err
	}
	var path string
	if id > 0 {
		path = fmt.Sprintf("/api/v1/clipboard/%d", id)
	} else {
		path = "/api/v1/clipboard/latest"
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var entry ClipboardEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// ---------- encryption ----------

func NewEncryptionOptions(password string) (*EncryptionOptions, error) {
	if strings.TrimSpace(password) == "" {
		return nil, errors.New("encryption password cannot be empty")
	}
	salt := make([]byte, EncryptionSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	nonce := make([]byte, EncryptionNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return &EncryptionOptions{
		Password: password,
		Salt:     salt,
		Nonce:    nonce,
		SaltB64:  base64.StdEncoding.EncodeToString(salt),
		NonceB64: base64.StdEncoding.EncodeToString(nonce),
	}, nil
}

func DecryptFile(encPath, destPath, password string, salt, nonce []byte) error {
	encKey, macKey, err := DeriveEncryptionKeys(password, salt)
	if err != nil {
		return err
	}
	f, err := os.Open(encPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < EncryptionMACSize {
		return errors.New("encrypted payload too small")
	}

	dataLen := info.Size() - EncryptionMACSize
	expectedMAC := make([]byte, EncryptionMACSize)
	if _, err := f.ReadAt(expectedMAC, dataLen); err != nil {
		return fmt.Errorf("read mac: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	limit := io.LimitReader(f, dataLen)
	stream, err := chacha20.NewUnauthenticatedCipher(encKey, nonce)
	if err != nil {
		return fmt.Errorf("init cipher: %w", err)
	}
	h := hmac.New(sha256.New, macKey)

	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		if out != nil {
			out.Close()
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := limit.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			stream.XORKeyStream(buf[:n], buf[:n])
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	calculated := h.Sum(nil)
	if !hmac.Equal(calculated, expectedMAC) {
		out.Close()
		out = nil
		os.Remove(tmpPath)
		return errors.New("invalid password or corrupted encrypted data")
	}
	if err := out.Close(); err != nil {
		out = nil
		os.Remove(tmpPath)
		return err
	}
	out = nil
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func DeriveEncryptionKeys(password string, salt []byte) ([]byte, []byte, error) {
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("derive key: %w", err)
	}
	encKey := make([]byte, 32)
	macKey := make([]byte, 32)
	copy(encKey, key[:32])
	copy(macKey, key[32:])
	return encKey, macKey, nil
}

func EncryptAndCopy(dst io.Writer, src io.Reader, opts *EncryptionOptions) error {
	encKey, macKey, err := DeriveEncryptionKeys(opts.Password, opts.Salt)
	if err != nil {
		return err
	}
	stream, err := chacha20.NewUnauthenticatedCipher(encKey, opts.Nonce)
	if err != nil {
		return fmt.Errorf("init cipher: %w", err)
	}
	mac := hmac.New(sha256.New, macKey)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			stream.XORKeyStream(buf[:n], buf[:n])
			mac.Write(buf[:n])
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	sum := mac.Sum(nil)
	if _, err := dst.Write(sum); err != nil {
		return err
	}
	return nil
}

func ParseFilename(header string) string {
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename=") {
			value := strings.TrimPrefix(part, "filename=")
			return strings.Trim(value, `"`)
		}
	}
	return ""
}

// ---------- internal HTTP ----------

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	base := ResolveServerURL("", c.Cfg)
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(MachineHeader, c.Cfg.MachineID)
	if c.Cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Cfg.Token)
	}
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(resp.Body)
		if len(msg) > 0 {
			var eresp struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(msg, &eresp); err == nil && eresp.Error != "" {
				return nil, errors.New(eresp.Error)
			}
		}
		return nil, fmt.Errorf("server error: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) performUpload(ctx context.Context, filePath, endpoint string, fields map[string]string, encOpts *EncryptionOptions) (*FileInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	req, err := c.newRequest(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		file.Close()
		pw.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	go func() {
		defer file.Close()
		var innerErr error
		defer func() {
			if cerr := writer.Close(); cerr != nil && innerErr == nil {
				innerErr = cerr
			}
			if innerErr != nil {
				pw.CloseWithError(innerErr)
			} else {
				pw.Close()
			}
		}()

		var part io.Writer
		part, innerErr = writer.CreateFormFile("file", filepath.Base(filePath))
		if innerErr != nil {
			return
		}
		if encOpts != nil {
			innerErr = EncryptAndCopy(part, file, encOpts)
		} else {
			_, innerErr = io.Copy(part, file)
		}
		if innerErr != nil {
			return
		}
		for key, value := range fields {
			if innerErr = writer.WriteField(key, value); innerErr != nil {
				return
			}
		}
	}()

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}
