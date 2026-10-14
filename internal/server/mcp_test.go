package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"opsdrop/internal/auth"
	"opsdrop/internal/config"
	"opsdrop/internal/db"
)

func TestMCPRequestAuthentication(t *testing.T) {
	ctx := context.Background()
	database, err := db.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.EnsureMigrations(ctx, database.Conn()); err != nil {
		t.Fatal(err)
	}
	key := []byte("test-only-secret-with-at-least-32-bytes")
	cfg := config.Config{StorageDir: t.TempDir(), JWTSigningKey: key, MaxUploadSizeBytes: 1024,
		DefaultPrivateTTL: time.Hour, MaxPrivateTTL: time.Hour,
		DefaultPublicTTL: time.Hour, MaxPublicTTL: time.Hour}
	// Avoid production cleanup goroutines: only the handler and isolated DB are needed.
	s := &Server{cfg: cfg, db: database, jwtSecret: key, storageDir: cfg.StorageDir}
	h := httptest.NewServer(s.mcpHandler())
	t.Cleanup(h.Close)
	user, err := database.CreateUser(ctx, "alice", "unused-test-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser(ctx, "bob", "unused-test-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	makeToken := func(id int64, name string, ttl time.Duration) string {
		t.Helper()
		token, err := auth.GenerateToken(id, name, key, ttl)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	token := makeToken(user.ID, user.Username, time.Hour)
	otherToken := makeToken(other.ID, other.Username, time.Hour)
	expired := makeToken(user.ID, user.Username, -time.Hour)
	call := func(method, bearer, session string, params any) (int, http.Header, []byte) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		r, err := http.NewRequest(http.MethodPost, h.URL, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			r.Header.Set("Authorization", bearer)
		}
		if session != "" {
			r.Header.Set("Mcp-Session-Id", session)
		}
		res, err := h.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header, b
	}
	params := map[string]any{"name": "push", "arguments": map[string]any{"filename": "test.txt", "content": "test content"}}
	initialize := map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}
	code, headers, _ := call("initialize", "Bearer "+token, "", initialize)
	if code != 200 {
		t.Fatalf("initialize status %d", code)
	}
	legacySession := headers.Get("Mcp-Session-Id")
	if legacySession == "" {
		legacySession = "legacy-session-must-not-authorize"
	}

	t.Run("legacy session cannot retain identity", func(t *testing.T) {
		for _, bearer := range []string{"", "Bearer " + otherToken, "Bearer invalid", "Bearer " + expired} {
			code, _, _ := call("tools/call", bearer, legacySession, params)
			if code < 400 {
				t.Errorf("legacy session accepted request, status %d", code)
			}
		}
	})
	t.Run("fresh requests use current identity", func(t *testing.T) {
		for _, tc := range []struct {
			name, bearer string
			owner        *int64
			public       bool
		}{
			{"alice", "Bearer " + token, &user.ID, false},
			{"bob", "Bearer " + otherToken, &other.ID, false},
			{"anonymous", "", nil, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				code, _, body := call("tools/call", tc.bearer, "", params)
				if code != 200 {
					t.Fatalf("push status %d: %s", code, body)
				}
				payload := string(body)
				if strings.HasPrefix(payload, "event:") || strings.HasPrefix(payload, "data:") {
					for _, line := range strings.Split(payload, "\n") {
						if strings.HasPrefix(line, "data: ") {
							payload = strings.TrimPrefix(line, "data: ")
							break
						}
					}
				}
				var result struct {
					Result struct {
						IsError    bool       `json:"isError"`
						Structured pushResult `json:"structuredContent"`
					} `json:"result"`
				}
				if err := json.Unmarshal([]byte(payload), &result); err != nil {
					t.Fatal(err)
				}
				if result.Result.IsError || result.Result.Structured.ID == 0 {
					t.Fatalf("push failed: %s", payload)
				}
				var owner *int64
				var public bool
				if err := database.Conn().QueryRowContext(ctx, "SELECT user_id, is_public FROM files WHERE id = ?", result.Result.Structured.ID).Scan(&owner, &public); err != nil {
					t.Fatal(err)
				}
				if public != tc.public || (owner == nil) != (tc.owner == nil) || (owner != nil && *owner != *tc.owner) {
					t.Fatalf("wrong stored ownership/visibility")
				}
			})
		}
	})
	t.Run("SDK client can initialize and push", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: h.URL, HTTPClient: h.Client()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "push", Arguments: map[string]any{"filename": "sdk.txt", "content": "SDK test"}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("tool failed: %+v", result)
		}
	})
	t.Run("GET streaming is not supported", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, h.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", "text/event-stream")
		res, err := h.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET status %d; want 405", res.StatusCode)
		}
	})
	t.Run("invalid credentials rejected before dispatch", func(t *testing.T) {
		for _, bearer := range []string{"Bearer invalid", "Bearer " + expired, "Basic invalid", "bearer invalid"} {
			code, _, _ := call("tools/call", bearer, "", params)
			if code != http.StatusUnauthorized {
				t.Errorf("invalid auth status %d; want 401", code)
			}
		}
	})
	t.Run("logout invalidates next request", func(t *testing.T) {
		if err := database.RevokeToken(ctx, token, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		code, _, _ := call("tools/call", "Bearer "+token, "", params)
		if code != http.StatusUnauthorized {
			t.Errorf("revoked token status %d; want 401", code)
		}
		code, _, _ = call("tools/call", "", legacySession, params)
		if code < 400 {
			t.Errorf("session usable without auth after logout, status %d", code)
		}
	})
	t.Run("initialize does not allocate persistent session", func(t *testing.T) {
		code, h, _ := call("initialize", "Bearer "+otherToken, "", initialize)
		if code != 200 || h.Get("Mcp-Session-Id") != "" {
			t.Errorf("persistent session allocated: status %d", code)
		}
	})
}
