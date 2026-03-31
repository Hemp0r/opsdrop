package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"opsdrop/internal/client"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileAction int

const (
	fileActionNone fileAction = iota
	fileActionConfirmDelete
	fileActionPromptPassword
)

type filesModel struct {
	client *client.Client
	table  table.Model
	files  []client.FileInfo

	action       fileAction
	actionFileID int64
	passwordIn   textinput.Model

	loading bool
	err     error
	width   int
	height  int
}

// messages
type filesLoadedMsg struct {
	files []client.FileInfo
	err   error
}

type fileDeletedMsg struct {
	id  int64
	err error
}

type fileDownloadedMsg struct {
	filename string
	checksum string
	verified bool
	err      error
}

func newFilesModel(c *client.Client) filesModel {
	columns := []table.Column{
		{Title: "ID", Width: 6},
		{Title: "Name", Width: 30},
		{Title: "Size", Width: 12},
		{Title: "Public", Width: 8},
		{Title: "Encrypted", Width: 10},
		{Title: "Checksum", Width: 14},
		{Title: "Uploaded", Width: 20},
		{Title: "Expires", Width: 20},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	s := table.DefaultStyles()
	s.Header = tableHeaderStyle
	s.Selected = tableSelectedStyle
	s.Cell = tableCellStyle
	t.SetStyles(s)

	pi := textinput.New()
	pi.Placeholder = "decryption password"
	pi.EchoMode = textinput.EchoPassword
	pi.EchoCharacter = '•'

	return filesModel{
		client:     c,
		table:      t,
		passwordIn: pi,
	}
}

func (m filesModel) init() tea.Cmd {
	return func() tea.Msg {
		files, err := m.client.ListFiles(context.Background())
		return filesLoadedMsg{files: files, err: err}
	}
}

func (m *filesModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.table.SetHeight(max(h-6, 5))

	// Distribute column widths proportionally
	available := w - 2 // borders
	if available < 80 {
		available = 80
	}
	cols := m.table.Columns()
	if len(cols) == 8 {
		cols[0].Width = 6                                         // ID
		cols[1].Width = max(available-6-12-8-10-14-20-20-8*2, 15) // Name gets remainder
		cols[2].Width = 12                                        // Size
		cols[3].Width = 8                                         // Public
		cols[4].Width = 10                                        // Encrypted
		cols[5].Width = 14                                        // Checksum
		cols[6].Width = 20                                        // Uploaded
		cols[7].Width = 20                                        // Expires
		m.table.SetColumns(cols)
	}
}

func (m filesModel) update(msg tea.Msg) (filesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case filesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, func() tea.Msg { return statusMsg{text: msg.err.Error(), isErr: true} }
		}
		m.files = msg.files
		m.err = nil
		m.table.SetRows(m.buildRows())
		return m, nil

	case fileDeletedMsg:
		if msg.err != nil {
			return m, func() tea.Msg { return statusMsg{text: msg.err.Error(), isErr: true} }
		}
		return m, tea.Batch(
			func() tea.Msg { return statusMsg{text: fmt.Sprintf("Deleted file %d", msg.id), isErr: false} },
			m.init(),
		)

	case fileDownloadedMsg:
		if msg.err != nil {
			return m, func() tea.Msg { return statusMsg{text: msg.err.Error(), isErr: true} }
		}
		status := fmt.Sprintf("Downloaded to %s", msg.filename)
		if msg.checksum != "" {
			if msg.verified {
				status += " (checksum ✓)"
			} else {
				status += " (checksum MISMATCH!)"
			}
		}
		return m, func() tea.Msg { return statusMsg{text: status, isErr: !msg.verified && msg.checksum != ""} }

	case tea.KeyMsg:
		// Handle action overlays first
		if m.action == fileActionConfirmDelete {
			switch msg.String() {
			case "y", "Y":
				id := m.actionFileID
				m.action = fileActionNone
				return m, func() tea.Msg {
					err := m.client.DeleteFile(context.Background(), id)
					return fileDeletedMsg{id: id, err: err}
				}
			default:
				m.action = fileActionNone
				return m, nil
			}
		}
		if m.action == fileActionPromptPassword {
			switch msg.String() {
			case "enter":
				pw := m.passwordIn.Value()
				id := m.actionFileID
				m.action = fileActionNone
				m.passwordIn.SetValue("")
				return m, m.downloadCmd(id, pw)
			case "esc":
				m.action = fileActionNone
				m.passwordIn.SetValue("")
				return m, nil
			default:
				var cmd tea.Cmd
				m.passwordIn, cmd = m.passwordIn.Update(msg)
				return m, cmd
			}
		}

		// Normal mode keys
		switch msg.String() {
		case "r":
			m.loading = true
			return m, m.init()
		case "d":
			f := m.selectedFile()
			if f == nil {
				return m, nil
			}
			if f.IsEncrypted {
				m.action = fileActionPromptPassword
				m.actionFileID = f.ID
				m.passwordIn.Focus()
				return m, textinput.Blink
			}
			return m, m.downloadCmd(f.ID, "")
		case "x":
			f := m.selectedFile()
			if f == nil {
				return m, nil
			}
			m.action = fileActionConfirmDelete
			m.actionFileID = f.ID
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m filesModel) view() string {
	var b strings.Builder

	if m.loading {
		b.WriteString(helpStyle.Render("  Loading files..."))
		return b.String()
	}

	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		return b.String()
	}

	if len(m.files) == 0 {
		b.WriteString(helpStyle.Render("  No files uploaded yet. Press u to upload."))
		return b.String()
	}

	b.WriteString(m.table.View())

	// Action overlay
	switch m.action {
	case fileActionConfirmDelete:
		b.WriteString("\n")
		b.WriteString(warningStyle.Render(fmt.Sprintf("  Delete file %d? (y/n)", m.actionFileID)))
	case fileActionPromptPassword:
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  Decryption password: %s", m.passwordIn.View()))
	}

	// Footer hints
	if m.action == fileActionNone {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  d download  x delete  u upload  r refresh"))
	}

	return b.String()
}

func (m filesModel) buildRows() []table.Row {
	rows := make([]table.Row, 0, len(m.files))
	for _, f := range m.files {
		pub := "no"
		if f.IsPublic {
			pub = "yes"
		}
		enc := "no"
		if f.IsEncrypted {
			enc = "yes"
		}
		chk := "—"
		if f.Checksum != nil && *f.Checksum != "" {
			c := *f.Checksum
			if len(c) > 12 {
				c = c[:12] + ".."
			}
			chk = c
		}
		rows = append(rows, table.Row{
			strconv.FormatInt(f.ID, 10),
			f.Filename,
			humanSize(f.Size),
			pub,
			enc,
			chk,
			f.UploadedAt,
			f.ExpiresAt,
		})
	}
	return rows
}

func (m filesModel) selectedFile() *client.FileInfo {
	row := m.table.SelectedRow()
	if row == nil {
		return nil
	}
	id, err := strconv.ParseInt(row[0], 10, 64)
	if err != nil {
		return nil
	}
	for i := range m.files {
		if m.files[i].ID == id {
			return &m.files[i]
		}
	}
	return nil
}

func (m filesModel) downloadCmd(id int64, password string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.client.DownloadFile(context.Background(), id)
		if err != nil {
			return fileDownloadedMsg{err: err}
		}
		defer result.Body.Close()

		filename := result.Filename

		if !result.Encrypted {
			f, err := os.Create(filename)
			if err != nil {
				return fileDownloadedMsg{err: err}
			}
			defer f.Close()
			if _, err := io.Copy(f, result.Body); err != nil {
				return fileDownloadedMsg{err: err}
			}
			chk, verified := verifyChecksum(filename, result.Checksum)
			return fileDownloadedMsg{filename: filename, checksum: chk, verified: verified}
		}

		// Encrypted
		if result.SaltB64 == "" || result.NonceB64 == "" {
			return fileDownloadedMsg{err: fmt.Errorf("server did not provide encryption metadata")}
		}
		salt, err := base64.StdEncoding.DecodeString(result.SaltB64)
		if err != nil {
			return fileDownloadedMsg{err: err}
		}
		nonce, err := base64.StdEncoding.DecodeString(result.NonceB64)
		if err != nil {
			return fileDownloadedMsg{err: err}
		}

		tmp, err := os.CreateTemp("", "opsdrop-tui-enc-*")
		if err != nil {
			return fileDownloadedMsg{err: err}
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, err := io.Copy(tmp, result.Body); err != nil {
			tmp.Close()
			return fileDownloadedMsg{err: err}
		}
		tmp.Close()

		if err := client.DecryptFile(tmpPath, filename, password, salt, nonce); err != nil {
			return fileDownloadedMsg{err: err}
		}
		return fileDownloadedMsg{filename: filename}
	}
}

func verifyChecksum(filePath, serverChecksum string) (string, bool) {
	if client.SkipChecksum || serverChecksum == "" {
		return "", true
	}
	localChecksum, err := client.ComputeFileChecksum(filePath)
	if err != nil {
		return serverChecksum, false
	}
	return serverChecksum, strings.EqualFold(localChecksum, serverChecksum)
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ensure lipgloss is used (styles reference it)
var _ = lipgloss.NewStyle
