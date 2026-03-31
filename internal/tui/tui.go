package tui

import (
	"fmt"
	"strings"

	"opsdrop/internal/client"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type view int

const (
	viewFiles view = iota
	viewClipboard
)

// Model is the root Bubble Tea model for the interactive TUI.
type Model struct {
	client *client.Client

	activeView view
	files      filesModel
	clipboard  clipboardModel
	upload     uploadModel

	width, height int
	statusMsg     string
	statusIsErr   bool

	showHelp   bool
	showUpload bool
	quitting   bool
}

// New creates a new TUI model.
func New(c *client.Client) Model {
	return Model{
		client:    c,
		files:     newFilesModel(c),
		clipboard: newClipboardModel(c),
		upload:    newUploadModel(c),
	}
}

func (m Model) Init() tea.Cmd {
	// Kick off initial data load for whichever view is active.
	return m.files.init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.files.setSize(msg.Width, msg.Height-4) // reserve header+status
		m.clipboard.setSize(msg.Width, msg.Height-4)
		m.upload.setSize(msg.Width, msg.Height-4)
		return m, nil

	case tea.KeyMsg:
		// Global keys
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "q":
			if m.showUpload || m.showHelp {
				// close overlay first
				if m.showUpload {
					m.showUpload = false
					m.upload = newUploadModel(m.client)
				}
				m.showHelp = false
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "?":
			if !m.showUpload {
				m.showHelp = !m.showHelp
			}
			return m, nil
		case "tab":
			if m.showUpload || m.showHelp {
				return m, nil
			}
			return m, m.switchView()
		case "u":
			if !m.showUpload && !m.showHelp && m.activeView == viewFiles {
				m.showUpload = true
				m.upload = newUploadModel(m.client)
				m.upload.setSize(m.width, m.height-4)
				return m, m.upload.init()
			}
		case "esc":
			if m.showUpload {
				m.showUpload = false
				m.upload = newUploadModel(m.client)
				return m, nil
			}
			if m.showHelp {
				m.showHelp = false
				return m, nil
			}
			return m, nil
		}

	case statusMsg:
		m.statusMsg = msg.text
		m.statusIsErr = msg.isErr
		return m, nil

	case uploadCompleteMsg:
		m.showUpload = false
		m.upload = newUploadModel(m.client)
		m.statusMsg = fmt.Sprintf("Uploaded %s (id=%d)", msg.info.Filename, msg.info.ID)
		m.statusIsErr = false
		// Refresh file list
		return m, m.files.init()
	}

	// Delegate to overlay first
	if m.showUpload {
		var cmd tea.Cmd
		m.upload, cmd = m.upload.update(msg)
		return m, cmd
	}

	// Delegate to active view
	var cmd tea.Cmd
	switch m.activeView {
	case viewFiles:
		m.files, cmd = m.files.update(msg)
	case viewClipboard:
		m.clipboard, cmd = m.clipboard.update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Tab bar
	b.WriteString(m.renderTabs())
	b.WriteString("\n")

	// Main content
	if m.showHelp {
		b.WriteString(m.renderHelp())
	} else if m.showUpload {
		b.WriteString(m.upload.view())
	} else {
		switch m.activeView {
		case viewFiles:
			b.WriteString(m.files.view())
		case viewClipboard:
			b.WriteString(m.clipboard.view())
		}
	}

	// Status bar
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	return b.String()
}

func (m *Model) switchView() tea.Cmd {
	switch m.activeView {
	case viewFiles:
		m.activeView = viewClipboard
		return m.clipboard.init()
	case viewClipboard:
		m.activeView = viewFiles
		return m.files.init()
	}
	return nil
}

func (m Model) renderTabs() string {
	tabs := []struct {
		label  string
		active bool
	}{
		{"Files", m.activeView == viewFiles},
		{"Clipboard", m.activeView == viewClipboard},
	}

	var rendered []string
	for _, t := range tabs {
		if t.active {
			rendered = append(rendered, activeTabStyle.Render(t.label))
		} else {
			rendered = append(rendered, inactiveTabStyle.Render(t.label))
		}
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
	helpHint := helpStyle.Render("? help")
	gap := strings.Repeat(" ", max(0, m.width-lipgloss.Width(row)-lipgloss.Width(helpHint)-2))

	return row + gap + helpHint
}

func (m Model) renderStatusBar() string {
	server := statusServerStyle.Render(client.ResolveServerURL("", m.client.Cfg))
	user := statusUserStyle.Render(m.client.Cfg.LastLoginUser)
	left := statusBarStyle.Render(fmt.Sprintf(" %s @ %s", user, server))

	var right string
	if m.statusMsg != "" {
		if m.statusIsErr {
			right = errorStyle.Render(m.statusMsg)
		} else {
			right = successStyle.Render(m.statusMsg)
		}
	}

	gap := strings.Repeat(" ", max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1))
	return left + gap + right
}

func (m Model) renderHelp() string {
	var lines []string
	lines = append(lines, titleStyle.Render("Keybindings"))
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("  Global"))
	lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("tab"), "Switch view"))
	lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("?"), "Toggle help"))
	lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("q / ctrl+c"), "Quit"))
	lines = append(lines, "")

	if m.activeView == viewFiles {
		lines = append(lines, helpStyle.Render("  Files"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("↑/↓ j/k"), "Navigate"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("d"), "Download selected"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("x"), "Delete selected"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("u"), "Upload file"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("r"), "Refresh"))
	} else {
		lines = append(lines, helpStyle.Render("  Clipboard"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("↑/↓ j/k"), "Navigate"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("c"), "Copy to clipboard"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("n"), "New entry"))
		lines = append(lines, fmt.Sprintf("  %s  %s", lipgloss.NewStyle().Bold(true).Width(12).Render("r"), "Refresh"))
	}

	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("  Press ? or esc to close"))

	return strings.Join(lines, "\n")
}

// statusMsg is a tea.Msg to set the status bar message.
type statusMsg struct {
	text  string
	isErr bool
}

// uploadCompleteMsg signals the file list should refresh.
type uploadCompleteMsg struct {
	info *client.FileInfo
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
