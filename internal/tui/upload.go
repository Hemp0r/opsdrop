package tui

import (
	"context"
	"fmt"
	"strings"

	"opsdrop/internal/client"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type uploadField int

const (
	uploadFieldPath uploadField = iota
	uploadFieldRetention
	uploadFieldCount
)

type uploadModel struct {
	client *client.Client

	pathInput      textinput.Model
	retentionInput textinput.Model

	public      bool
	encrypt     bool
	activeField uploadField

	submitting bool
	err        error
	width      int
	height     int
}

func newUploadModel(c *client.Client) uploadModel {
	pi := textinput.New()
	pi.Placeholder = "/path/to/file"
	pi.Focus()
	pi.Width = 50

	ri := textinput.New()
	ri.Placeholder = "14"
	ri.SetValue("14")
	ri.Width = 10
	ri.CharLimit = 2

	return uploadModel{
		client:         c,
		pathInput:      pi,
		retentionInput: ri,
		activeField:    uploadFieldPath,
	}
}

func (m uploadModel) init() tea.Cmd {
	return textinput.Blink
}

func (m *uploadModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.pathInput.Width = max(w-20, 30)
}

func (m uploadModel) update(msg tea.Msg) (uploadModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab":
			if msg.String() == "tab" {
				m.activeField = (m.activeField + 1) % uploadFieldCount
			} else {
				m.activeField = (m.activeField - 1 + uploadFieldCount) % uploadFieldCount
			}
			m.focusActive()
			return m, textinput.Blink

		case "ctrl+p":
			m.public = !m.public
			if m.public {
				m.encrypt = false
			}
			return m, nil

		case "ctrl+e":
			if !m.public {
				m.encrypt = !m.encrypt
			}
			return m, nil

		case "enter":
			if m.submitting {
				return m, nil
			}
			filePath := strings.TrimSpace(m.pathInput.Value())
			if filePath == "" {
				m.err = fmt.Errorf("file path is required")
				return m, nil
			}

			retDays := 14
			if v := strings.TrimSpace(m.retentionInput.Value()); v != "" {
				if n, err := fmt.Sscanf(v, "%d", &retDays); n != 1 || err != nil {
					retDays = 14
				}
			}

			// Resolve path
			resolved, err := client.ExpandPath(filePath)
			if err != nil {
				m.err = err
				return m, nil
			}

			m.submitting = true
			m.err = nil

			pub := m.public
			enc := m.encrypt

			return m, func() tea.Msg {
				var encOpts *client.EncryptionOptions
				if enc {
					// For TUI upload encryption, we'd need a password prompt.
					// For now, skip encryption in TUI - users can use CLI for encrypted uploads.
					return statusMsg{text: "Encrypted uploads not yet supported in TUI; use CLI", isErr: true}
				}

				info, err := m.client.UploadFile(context.Background(), resolved, pub, retDays, encOpts)
				if err != nil {
					return statusMsg{text: err.Error(), isErr: true}
				}
				return uploadCompleteMsg{info: info}
			}
		}
	}

	// Update the focused input
	var cmd tea.Cmd
	switch m.activeField {
	case uploadFieldPath:
		m.pathInput, cmd = m.pathInput.Update(msg)
	case uploadFieldRetention:
		m.retentionInput, cmd = m.retentionInput.Update(msg)
	}
	return m, cmd
}

func (m *uploadModel) focusActive() {
	m.pathInput.Blur()
	m.retentionInput.Blur()
	switch m.activeField {
	case uploadFieldPath:
		m.pathInput.Focus()
	case uploadFieldRetention:
		m.retentionInput.Focus()
	}
}

func (m uploadModel) view() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Upload File"))
	b.WriteString("\n\n")

	// File path
	label := "  File path:      "
	b.WriteString(label)
	b.WriteString(m.pathInput.View())
	b.WriteString("\n\n")

	// Retention
	label = "  Retention days: "
	b.WriteString(label)
	b.WriteString(m.retentionInput.View())
	b.WriteString("\n\n")

	// Toggles
	pubIcon := "[ ]"
	if m.public {
		pubIcon = "[x]"
	}
	encIcon := "[ ]"
	if m.encrypt {
		encIcon = "[x]"
	}

	b.WriteString(fmt.Sprintf("  %s Public    (ctrl+p)\n", pubIcon))
	b.WriteString(fmt.Sprintf("  %s Encrypted (ctrl+e)\n", encIcon))

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  %v", m.err)))
	}

	if m.submitting {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  Uploading..."))
	}

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("  enter submit  tab next field  esc cancel"))

	return b.String()
}
