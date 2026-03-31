package main

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"opsdrop/internal/client"
	"opsdrop/internal/version"

	"github.com/atotto/clipboard"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func main() {
	rootCmd := newRootCommand()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "opsdrop",
		Short:        "OpsDrop — share files and clipboard snippets across devices",
		Version:      version.Version,
		SilenceUsage: true,
	}
	cmd.SetVersionTemplate(fmt.Sprintf("opsdrop version %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.Date))

	cmd.PersistentFlags().StringVar(&client.ConfigPathOverride, "config", "", "Path to configuration file (defaults to ~/.opsdrop/config.json)")
	cmd.PersistentFlags().BoolVar(&client.SkipChecksum, "no-checksum", false, "Skip checksum computation and verification")
	cmd.PersistentFlags().BoolVar(&client.Insecure, "insecure", false, "Skip TLS certificate verification")

	// Define command groups for cleaner help output.
	coreGroup := &cobra.Group{ID: "core", Title: "Core Commands:"}
	mgmtGroup := &cobra.Group{ID: "mgmt", Title: "Management:"}
	cfgGroup := &cobra.Group{ID: "cfg", Title: "Configuration:"}
	authGroup := &cobra.Group{ID: "auth", Title: "Authentication:"}
	uiGroup := &cobra.Group{ID: "ui", Title: "Interactive:"}
	cmd.AddGroup(coreGroup, mgmtGroup, cfgGroup, authGroup, uiGroup)

	push := newPushCommand()
	push.GroupID = "core"
	pull := newPullCommand()
	pull.GroupID = "core"

	list := newListCommand()
	list.GroupID = "mgmt"
	del := newDeleteCommand()
	del.GroupID = "mgmt"

	remote := newRemoteCommand()
	remote.GroupID = "cfg"

	auth := newAuthCommand()
	auth.GroupID = "auth"

	ui := newUICommand()
	ui.GroupID = "ui"

	cmd.AddCommand(push, pull, list, del, remote, auth, ui)

	return cmd
}

// ────────────────────────────────────────────────────────────
// remote command group
// ────────────────────────────────────────────────────────────

func newRemoteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage the configured OpsDrop remote server",
	}
	cmd.AddCommand(
		newRemoteSetCommand(),
		newRemoteShowCommand(),
		newRemoteRefreshCommand(),
		newRemoteResetCommand(),
	)
	return cmd
}

func newRemoteSetCommand() *cobra.Command {
	var insecure bool

	cmd := &cobra.Command{
		Use:   "set <url>",
		Short: "Configure a custom OpsDrop remote",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawURL := strings.TrimSpace(args[0])
			parsed, err := url.Parse(rawURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return fmt.Errorf("invalid remote URL: must start with http:// or https://")
			}

			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			cfg.RemoteURL = strings.TrimRight(rawURL, "/")
			if insecure || client.Insecure {
				cfg.SkipTLSVerify = true
			}
			if err := client.SaveConfig(cfg); err != nil {
				return err
			}

			// Try to fetch capabilities (best-effort).
			c := client.New(cfg)
			caps, capErr := c.FetchCapabilities(context.Background())
			if capErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: remote saved but capabilities could not be fetched: %v\n", capErr)
				return printJSON(map[string]any{
					"status":       "ok",
					"remote":       cfg.RemoteURL,
					"capabilities": "unavailable",
				})
			}
			if err := client.SaveCapabilities(cfg, caps); err != nil {
				return err
			}
			return printJSON(map[string]any{
				"status":       "ok",
				"remote":       cfg.RemoteURL,
				"capabilities": caps,
			})
		},
	}

	cmd.Flags().BoolVar(&insecure, "insecure", false, "Persist TLS skip for this remote")

	return cmd
}

func newRemoteShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Display the active remote and cached capabilities",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			remote := client.ResolveServerURL("", cfg)
			isDefault := strings.TrimSpace(cfg.RemoteURL) == ""

			out := map[string]any{
				"remote":  remote,
				"default": isDefault,
			}
			if cfg.SkipTLSVerify {
				out["insecure"] = true
			}
			if cfg.Capabilities != nil {
				out["capabilities"] = cfg.Capabilities
				out["capabilities_fetched_at"] = cfg.CapabilitiesFetchedAt
			} else {
				out["capabilities"] = "not cached — run 'opsdrop remote refresh'"
			}
			return printJSON(out)
		},
	}
}

func newRemoteRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Re-fetch and cache server capabilities",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			c := client.New(cfg)
			caps, err := c.FetchCapabilities(context.Background())
			if err != nil {
				return fmt.Errorf("capability fetch failed: %w", err)
			}
			if err := client.SaveCapabilities(cfg, caps); err != nil {
				return err
			}
			return printJSON(map[string]any{
				"status":       "ok",
				"remote":       client.ResolveServerURL("", cfg),
				"capabilities": caps,
			})
		},
	}
}

func newRemoteResetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Remove the configured remote and revert to the default",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			cfg.RemoteURL = ""
			cfg.Token = ""
			cfg.LastLoginUser = ""
			cfg.SkipTLSVerify = false
			cfg.Capabilities = nil
			cfg.CapabilitiesFetchedAt = ""
			if err := client.SaveConfig(cfg); err != nil {
				return err
			}
			return printJSON(map[string]any{
				"status":  "ok",
				"message": "Reset to default remote: " + client.DefaultRemoteURL,
			})
		},
	}
}

// ────────────────────────────────────────────────────────────
// auth command group
// ────────────────────────────────────────────────────────────

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with the configured OpsDrop remote",
	}
	cmd.AddCommand(
		newAuthLoginCommand(),
		newAuthLogoutCommand(),
		newAuthRegisterCommand(),
		newAuthWhoamiCommand(),
	)
	return cmd
}

func newAuthLoginCommand() *cobra.Command {
	var (
		username string
		password string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against the configured remote",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			username = strings.TrimSpace(username)
			if username == "" {
				return errors.New("--username is required")
			}
			if password != "" {
				fmt.Fprintln(os.Stderr, "Warning: --password exposes credentials in process listing and shell history. Use interactive prompt instead.")
			}

			pass := password
			if pass == "" {
				fmt.Print("Password: ")
				raw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					return err
				}
				pass = string(raw)
			}

			c := client.New(cfg)
			token, expires, err := c.Login(context.Background(), username, pass)
			if err != nil {
				return err
			}

			cfg.Token = token
			cfg.LastLoginUser = username
			if err := client.SaveConfig(cfg); err != nil {
				return err
			}

			return printJSON(map[string]any{
				"status":        "ok",
				"message":       "Login successful",
				"token_expires": expires,
			})
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "Username")
	cmd.Flags().StringVar(&password, "password", "", "Password (if omitted, prompt)")

	return cmd
}

func newAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear the stored authentication token",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			c := client.New(cfg)
			_ = c.Logout(context.Background())

			cfg.Token = ""
			if err := client.SaveConfig(cfg); err != nil {
				return err
			}
			return printJSON(map[string]any{"status": "ok", "message": "Logged out"})
		},
	}
}

func newAuthRegisterCommand() *cobra.Command {
	var (
		username string
		password string
	)

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Create a new account on the configured remote",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}

			// Warn early if we know registration is disabled.
			if cfg.Capabilities != nil && !cfg.Capabilities.SelfServiceRegistration {
				return errors.New("self-service registration is not available on this server")
			}

			username = strings.TrimSpace(username)
			if username == "" {
				return errors.New("--username is required")
			}
			if password != "" {
				fmt.Fprintln(os.Stderr, "Warning: --password exposes credentials in process listing and shell history. Use interactive prompt instead.")
			}

			pass := password
			if pass == "" {
				fmt.Print("Password: ")
				raw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					return err
				}
				pass = string(raw)
			}

			c := client.New(cfg)
			if err := c.Register(context.Background(), username, pass); err != nil {
				return err
			}
			if err := client.SaveConfig(cfg); err != nil {
				return err
			}

			return printJSON(map[string]any{"status": "ok", "message": "Registration successful"})
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "Username")
	cmd.Flags().StringVar(&password, "password", "", "Password (if omitted, prompt)")

	return cmd
}

func newAuthWhoamiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Display the current authenticated identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			token := strings.TrimSpace(cfg.Token)
			if token == "" {
				return printJSON(map[string]any{
					"status":  "ok",
					"message": "Not logged in",
					"remote":  client.ResolveServerURL("", cfg),
				})
			}

			// Decode JWT payload (base64 middle segment) without verifying signature.
			parts := strings.Split(token, ".")
			if len(parts) != 3 {
				return errors.New("stored token is malformed")
			}
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				return fmt.Errorf("decode token payload: %w", err)
			}
			var claims struct {
				UserID   int64  `json:"user_id"`
				Username string `json:"username"`
				Exp      int64  `json:"exp"`
			}
			if err := json.Unmarshal(payload, &claims); err != nil {
				return fmt.Errorf("parse token claims: %w", err)
			}

			expTime := time.Unix(claims.Exp, 0).UTC()
			expired := time.Now().UTC().After(expTime)

			return printJSON(map[string]any{
				"status":        "ok",
				"user_id":       claims.UserID,
				"username":      claims.Username,
				"token_expires": expTime.Format(time.RFC3339),
				"expired":       expired,
				"remote":        client.ResolveServerURL("", cfg),
			})
		},
	}
}

// ────────────────────────────────────────────────────────────
// core commands: push  pull
// ────────────────────────────────────────────────────────────

func newPushCommand() *cobra.Command {
	var (
		clipboardMode   bool
		public          bool
		retentionDays   int
		encrypt         bool
		encryptPassword string
	)

	cmd := &cobra.Command{
		Use:   "push [path]",
		Short: "Push a file, directory, or clipboard content",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			c := client.New(cfg)

			if clipboardMode {
				bodyContent, err := resolveClipboardContent("", cmd.InOrStdin())
				if err != nil {
					return err
				}
				entry, err := c.ClipboardSend(context.Background(), bodyContent)
				if err != nil {
					return err
				}
				return printJSON(map[string]any{
					"status":     "ok",
					"id":         entry.ID,
					"entry_type": "clipboard",
					"created_at": entry.CreatedAt,
				})
			}

			// File/directory mode
			filePath := ""
			if len(args) == 1 {
				filePath = args[0]
			}
			filePath = strings.TrimSpace(filePath)
			if filePath == "" {
				return errors.New("path required; pass as argument or use --clipboard for text content")
			}

			loggedIn := strings.TrimSpace(cfg.Token) != ""
			if !loggedIn {
				if !public {
					fmt.Fprintln(os.Stderr, "No active login detected; uploading as a 48h public share.")
				}
				public = true
			}

			if encrypt && public {
				return errors.New("cannot encrypt a public upload")
			}

			var encOpts *client.EncryptionOptions
			if encrypt {
				if err := c.EnsureToken(); err != nil {
					return err
				}
				password := strings.TrimSpace(encryptPassword)
				if password == "" {
					password, err = promptPassword("Encryption password: ", true)
					if err != nil {
						return err
					}
				}
				encOpts, err = client.NewEncryptionOptions(password)
				if err != nil {
					return err
				}
			}

			info, err := os.Stat(filePath)
			if err != nil {
				return fmt.Errorf("stat path: %w", err)
			}

			var data *client.FileInfo
			if info.IsDir() {
				data, err = c.UploadDirectory(context.Background(), filePath, public, retentionDays, encOpts)
			} else {
				data, err = c.UploadFile(context.Background(), filePath, public, retentionDays, encOpts)
			}
			if err != nil {
				return err
			}
			return printUploadJSON(cfg, data)
		},
	}

	cmd.Flags().BoolVarP(&clipboardMode, "clipboard", "c", false, "Push clipboard/text content instead of a file")
	cmd.Flags().BoolVar(&public, "public", false, "Make the file publicly accessible")
	cmd.Flags().IntVar(&retentionDays, "retention-days", client.DefaultRetentionDays, "Retention period in days for private uploads (1-14)")
	cmd.Flags().BoolVar(&encrypt, "encrypt", false, "Encrypt the file locally before uploading (requires authentication)")
	cmd.Flags().StringVar(&encryptPassword, "encrypt-password", "", "Password to use for local encryption (prompted if omitted)")

	return cmd
}

func newPullCommand() *cobra.Command {
	var (
		outPath         string
		clipboardMode   bool
		decryptPassword string
	)

	cmd := &cobra.Command{
		Use:   "pull <id-or-token>",
		Short: "Pull a file or clipboard entry by numeric ID or public token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			c := client.New(cfg)

			ref := strings.TrimSpace(args[0])

			// Detect: numeric → private download by ID; otherwise → public download by token.
			if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
				return pullByID(c, id, outPath, clipboardMode, decryptPassword)
			}
			return pullByToken(c, ref, outPath, decryptPassword)
		},
	}

	cmd.Flags().StringVarP(&outPath, "output", "o", "", "Write output to file instead of stdout")
	cmd.Flags().BoolVar(&clipboardMode, "clipboard", false, "Copy content to system clipboard (clipboard entries only)")
	cmd.Flags().StringVar(&decryptPassword, "password", "", "Decryption password for encrypted files (prompted if omitted)")

	return cmd
}

func pullByID(c *client.Client, id int64, outPath string, clipboardMode bool, password string) error {
	result, err := c.DownloadFile(context.Background(), id)
	if err != nil {
		return err
	}

	if result.EntryType == "clipboard" {
		content := result.Content
		if clipboardMode {
			if err := clipboard.WriteAll(content); err != nil {
				return fmt.Errorf("copy to system clipboard: %w", err)
			}
			fmt.Fprintln(os.Stderr, "Copied to system clipboard.")
			return nil
		}
		if outPath != "" {
			if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
				return err
			}
			return printJSON(map[string]any{"status": "ok", "file": outPath, "entry_type": "clipboard"})
		}
		fmt.Print(content)
		return nil
	}

	defer result.Body.Close()
	if clipboardMode {
		return errors.New("--clipboard flag is only supported for clipboard entries, not files")
	}
	if outPath != "" {
		return writeFileResult(outPath, result, password)
	}
	// Streaming to stdout — if encrypted, need a temp file for decryption.
	if result.Encrypted {
		return decryptToStdout(result, password)
	}
	_, err = io.Copy(os.Stdout, result.Body)
	return err
}

func pullByToken(c *client.Client, token string, outPath string, password string) error {
	result, err := c.DownloadPublicFile(context.Background(), token)
	if err != nil {
		return err
	}
	defer result.Body.Close()

	if outPath != "" {
		return writeFileResult(outPath, result, password)
	}
	if result.Encrypted {
		return decryptToStdout(result, password)
	}
	_, err = io.Copy(os.Stdout, result.Body)
	return err
}

// ────────────────────────────────────────────────────────────
// management commands: list  delete
// ────────────────────────────────────────────────────────────

func newListCommand() *cobra.Command {
	var (
		clipboardMode bool
		limit         int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List files or clipboard entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			c := client.New(cfg)

			if clipboardMode {
				entries, err := c.ClipboardList(context.Background(), limit)
				if err != nil {
					return err
				}
				return printJSON(entries)
			}

			files, err := c.ListFiles(context.Background())
			if err != nil {
				return err
			}

			base := client.ResolveServerURL("", cfg)
			type fileEntry struct {
				ID          int64  `json:"id"`
				Filename    string `json:"filename"`
				Size        int64  `json:"size"`
				Public      bool   `json:"public"`
				PublicURL   string `json:"public_url,omitempty"`
				Encrypted   bool   `json:"encrypted"`
				Checksum    string `json:"checksum,omitempty"`
				UploadedAt  string `json:"uploaded_at"`
				ExpiresAt   string `json:"expires_at"`
				DownloadURL string `json:"download_url"`
			}
			entries := make([]fileEntry, 0, len(files))
			for _, f := range files {
				e := fileEntry{
					ID:          f.ID,
					Filename:    f.Filename,
					Size:        f.Size,
					Public:      f.IsPublic,
					Encrypted:   f.IsEncrypted,
					UploadedAt:  f.UploadedAt,
					ExpiresAt:   f.ExpiresAt,
					DownloadURL: base + f.DownloadLink,
				}
				if f.IsPublic && f.PublicLink != nil {
					e.PublicURL = base + *f.PublicLink
				}
				if f.Checksum != nil {
					e.Checksum = *f.Checksum
				}
				entries = append(entries, e)
			}
			return printJSON(entries)
		},
	}

	cmd.Flags().BoolVarP(&clipboardMode, "clipboard", "c", false, "List clipboard entries instead of files")
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of clipboard entries to retrieve")

	return cmd
}

func newDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an entry by id (file or clipboard)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}
			c := client.New(cfg)
			if err := c.DeleteFile(context.Background(), id); err != nil {
				return err
			}
			return printJSON(map[string]any{"status": "ok", "deleted_id": id})
		},
	}
}

// ────────────────────────────────────────────────────────────
// helpers
// ────────────────────────────────────────────────────────────

func parseID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("id is required")
	}
	var id int64
	_, err := fmt.Sscanf(s, "%d", &id)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id: %s", s)
	}
	return id, nil
}

func writeFileResult(outPath string, result *client.DownloadResult, password string) error {
	if result.Encrypted {
		return decryptToFile(outPath, result, password)
	}
	target, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err := io.Copy(target, result.Body); err != nil {
		return err
	}
	out := map[string]any{"status": "ok", "file": outPath}
	checkResult := verifyChecksumCLI(outPath, result.Checksum)
	if checkResult != nil {
		out["checksum"] = checkResult
	}
	return printJSON(out)
}

func resolveDecryptPassword(password string) (string, error) {
	if strings.TrimSpace(password) != "" {
		return password, nil
	}
	fmt.Fprint(os.Stderr, "File is encrypted. Password: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(raw))
	if p == "" {
		return "", errors.New("password cannot be empty")
	}
	return p, nil
}

func decryptToFile(outPath string, result *client.DownloadResult, password string) error {
	pass, err := resolveDecryptPassword(password)
	if err != nil {
		return err
	}
	salt, err := base64.StdEncoding.DecodeString(result.SaltB64)
	if err != nil {
		return fmt.Errorf("decode encryption salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(result.NonceB64)
	if err != nil {
		return fmt.Errorf("decode encryption nonce: %w", err)
	}

	// Write ciphertext to a temp file, then decrypt to the final path.
	tmp, err := os.CreateTemp("", "opsdrop-enc-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, result.Body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	if err := client.DecryptFile(tmpPath, outPath, pass, salt, nonce); err != nil {
		return err
	}
	out := map[string]any{"status": "ok", "file": outPath, "encrypted": true, "decrypted": true}
	checkResult := verifyChecksumCLI(outPath, result.Checksum)
	if checkResult != nil {
		out["checksum"] = checkResult
	}
	return printJSON(out)
}

func decryptToStdout(result *client.DownloadResult, password string) error {
	pass, err := resolveDecryptPassword(password)
	if err != nil {
		return err
	}
	salt, err := base64.StdEncoding.DecodeString(result.SaltB64)
	if err != nil {
		return fmt.Errorf("decode encryption salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(result.NonceB64)
	if err != nil {
		return fmt.Errorf("decode encryption nonce: %w", err)
	}

	// Write ciphertext to temp, decrypt to another temp, then stream to stdout.
	encTmp, err := os.CreateTemp("", "opsdrop-enc-*")
	if err != nil {
		return err
	}
	encPath := encTmp.Name()
	defer os.Remove(encPath)

	if _, err := io.Copy(encTmp, result.Body); err != nil {
		encTmp.Close()
		return err
	}
	encTmp.Close()

	decTmp, err := os.CreateTemp("", "opsdrop-dec-*")
	if err != nil {
		return err
	}
	decPath := decTmp.Name()
	decTmp.Close()
	defer os.Remove(decPath)

	if err := client.DecryptFile(encPath, decPath, pass, salt, nonce); err != nil {
		return err
	}

	f, err := os.Open(decPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func printUploadJSON(cfg *client.Config, data *client.FileInfo) error {
	base := client.ResolveServerURL("", cfg)
	out := map[string]any{
		"id":          data.ID,
		"filename":    data.Filename,
		"size":        data.Size,
		"public":      data.IsPublic,
		"encrypted":   data.IsEncrypted,
		"uploaded_at": data.UploadedAt,
		"expires_at":  data.ExpiresAt,
	}
	if data.Checksum != nil && *data.Checksum != "" {
		out["checksum"] = *data.Checksum
	}
	if data.PublicLink != nil && strings.TrimSpace(*data.PublicLink) != "" {
		link := *data.PublicLink
		out["public_url"] = base + link
		// Extract token from /public/<token> for easy pull reference.
		if idx := strings.LastIndex(link, "/"); idx >= 0 {
			tok := link[idx+1:]
			out["token"] = tok
			out["pull"] = "opsdrop pull " + tok
		}
	}
	if !data.IsPublic && strings.TrimSpace(data.DownloadLink) != "" {
		out["download_url"] = base + data.DownloadLink
	}
	return printJSON(out)
}

func verifyChecksumCLI(filePath, serverChecksum string) map[string]any {
	if client.SkipChecksum || serverChecksum == "" {
		return nil
	}
	localChecksum, err := client.ComputeFileChecksum(filePath)
	if err != nil {
		return map[string]any{"server": serverChecksum, "error": err.Error()}
	}
	verified := strings.EqualFold(localChecksum, serverChecksum)
	return map[string]any{"server": serverChecksum, "local": localChecksum, "verified": verified}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	if destDir == "" {
		destDir = "."
	}
	destDir, err = filepath.Abs(destDir)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		if strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) {
			return fmt.Errorf("illegal zip entry path: %s", f.Name)
		}
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(target, destDir+string(os.PathSeparator)) && target != destDir {
			return fmt.Errorf("illegal zip entry path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}

	if err := os.Remove(zipPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove zip after extraction: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "Extracted to %s\n", destDir)
	return nil
}

func promptPassword(prompt string, confirm bool) (string, error) {
	fmt.Print(prompt)
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	password := strings.TrimSpace(string(first))
	if password == "" {
		return "", errors.New("password cannot be empty")
	}
	if confirm {
		fmt.Print("Confirm password: ")
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		if password != strings.TrimSpace(string(second)) {
			return "", errors.New("passwords do not match")
		}
	}
	return password, nil
}

func resolveClipboardContent(contentFlag string, reader io.Reader) (string, error) {
	if trimmed := strings.TrimSpace(contentFlag); trimmed != "" {
		return trimmed, nil
	}

	if data, ok, err := readIfAvailable(reader); err != nil {
		return "", err
	} else if ok {
		if trimmed := strings.TrimSpace(data); trimmed != "" {
			return trimmed, nil
		}
	}

	clip, err := clipboard.ReadAll()
	if err != nil {
		return "", fmt.Errorf("clipboard content is empty and could not read system clipboard: %w", err)
	}
	clip = strings.TrimSpace(clip)
	if clip == "" {
		return "", errors.New("clipboard content is empty")
	}
	return clip, nil
}

func readIfAvailable(reader io.Reader) (string, bool, error) {
	if reader == nil {
		return "", false, nil
	}
	if file, ok := reader.(*os.File); ok {
		stat, err := file.Stat()
		if err != nil {
			return "", false, err
		}
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			return "", false, nil
		}
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", false, err
	}
	if len(data) == 0 {
		return "", false, nil
	}
	return string(data), true, nil
}
