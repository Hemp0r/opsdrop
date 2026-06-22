package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"opsdrop/internal/version"
)

// pushArgs is the input schema for the MCP "push" tool. Exactly one of Content
// or ContentBase64 must be provided.
type pushArgs struct {
	Content       string `json:"content,omitempty" jsonschema:"UTF-8 text to upload as the drop. Provide either this or content_base64, not both."`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"Base64-encoded bytes to upload; use for binary content. Provide either this or content, not both."`
	Filename      string `json:"filename" jsonschema:"Name for the drop, e.g. note.txt or report.pdf."`
	RetentionDays int    `json:"retention_days,omitempty" jsonschema:"Optional retention in days; clamped to the server maximum for the visibility used."`
}

// pushResult is the structured output of the MCP "push" tool.
type pushResult struct {
	ID        int64  `json:"id" jsonschema:"server-assigned drop id"`
	Filename  string `json:"filename" jsonschema:"stored filename"`
	Size      int64  `json:"size" jsonschema:"size in bytes"`
	Public    bool   `json:"public" jsonschema:"whether the drop is publicly accessible"`
	URL       string `json:"url" jsonschema:"absolute URL to retrieve the drop"`
	Token     string `json:"token,omitempty" jsonschema:"public token for anonymous retrieval, present only for public drops"`
	ExpiresAt string `json:"expires_at" jsonschema:"RFC3339 expiry timestamp"`
	Checksum  string `json:"checksum,omitempty" jsonschema:"sha256 checksum of the content"`
}

// mcpHandler returns the Streamable HTTP handler for the MCP endpoint. A fresh
// server is built per request so the push tool can be bound to the caller's
// resolved identity: a valid bearer token yields a private drop for that user,
// otherwise the drop is anonymous and public.
func (s *Server) mcpHandler() http.Handler {
	getServer := func(r *http.Request) *mcp.Server {
		user, authErr := s.resolveUser(r)
		machine := clientMachine(r)
		baseURL := requestBaseURL(r)

		srv := mcp.NewServer(&mcp.Implementation{
			Name:    "opsdrop",
			Title:   "OpsDrop",
			Version: version.Version,
		}, nil)
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "push",
			Title:       "Push a drop",
			Description: "Upload inline content to OpsDrop and return a shareable URL and token. With a valid bearer token the drop is private to that user; otherwise it is an anonymous public drop. Provide exactly one of content (text) or content_base64 (binary).",
		}, s.pushTool(user, authErr, machine, baseURL))
		return srv
	}
	return mcp.NewStreamableHTTPHandler(getServer, nil)
}

func (s *Server) pushTool(user *AuthenticatedUser, authErr error, machine, baseURL string) mcp.ToolHandlerFor[pushArgs, pushResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in pushArgs) (*mcp.CallToolResult, pushResult, error) {
		if authErr != nil {
			return nil, pushResult{}, fmt.Errorf("authentication failed: %w", authErr)
		}

		content := in.Content
		b64 := strings.TrimSpace(in.ContentBase64)
		if (content == "") == (b64 == "") {
			return nil, pushResult{}, errors.New("provide exactly one of content or content_base64")
		}

		var data []byte
		if b64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				return nil, pushResult{}, fmt.Errorf("invalid content_base64: %w", err)
			}
			data = decoded
		} else {
			data = []byte(content)
		}
		if int64(len(data)) > s.maxUploadSize() {
			return nil, pushResult{}, fmt.Errorf("content exceeds the %d byte upload limit", s.maxUploadSize())
		}

		isPublic := user == nil
		var userID *int64
		var ttl time.Duration
		if isPublic {
			ttl = clampTTL(in.RetentionDays, s.cfg.DefaultPublicTTL, s.cfg.MaxPublicTTL)
		} else {
			userID = &user.ID
			ttl = clampTTL(in.RetentionDays, s.cfg.DefaultPrivateTTL, s.cfg.MaxPrivateTTL)
		}

		resp, err := s.storeDrop(ctx, userID, in.Filename, data, isPublic, ttl, machine)
		if err != nil {
			return nil, pushResult{}, err
		}

		result := pushResult{
			ID:        resp.ID,
			Filename:  resp.Filename,
			Size:      resp.Size,
			Public:    resp.IsPublic,
			ExpiresAt: resp.ExpiresAt,
		}
		if resp.Checksum != nil {
			result.Checksum = *resp.Checksum
		}
		if resp.PublicLink != nil {
			result.URL = baseURL + *resp.PublicLink
			if idx := strings.LastIndex(*resp.PublicLink, "/"); idx >= 0 {
				result.Token = (*resp.PublicLink)[idx+1:]
			}
		} else {
			result.URL = baseURL + resp.DownloadLink
		}

		// Returning nil Content lets the SDK populate it from the structured
		// output automatically.
		return nil, result, nil
	}
}

// maxUploadSize returns the configured upload cap, falling back to the package
// default when unset.
func (s *Server) maxUploadSize() int64 {
	if s.cfg.MaxUploadSizeBytes > 0 {
		return s.cfg.MaxUploadSizeBytes
	}
	return maxUploadSizeBytes
}

// clampTTL converts an optional retention-in-days request into a duration,
// defaulting and clamping to the supplied bounds.
func clampTTL(retentionDays int, def, max time.Duration) time.Duration {
	if retentionDays <= 0 {
		return def
	}
	ttl := time.Duration(retentionDays) * 24 * time.Hour
	if ttl > max {
		return max
	}
	return ttl
}

// requestBaseURL reconstructs the externally visible base URL for a request,
// honoring the common reverse-proxy forwarding headers the server sits behind.
// Forwarded headers may be comma-separated proxy chains, so only the first entry
// is used and a forwarded scheme is accepted only if it is http or https —
// otherwise we fall back to the scheme derived from the connection.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); proto == "http" || proto == "https" {
		scheme = proto
	}
	host := r.Host
	if fwd := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host
}

// firstForwardedValue returns the trimmed, lower-cased first entry of a possibly
// comma-separated forwarding header (e.g. "https, http" -> "https").
func firstForwardedValue(header string) string {
	first := header
	if idx := strings.IndexByte(header, ','); idx >= 0 {
		first = header[:idx]
	}
	return strings.ToLower(strings.TrimSpace(first))
}
