package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"opsdrop/internal/client"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

type clipAction int

const (
	clipActionNone clipAction = iota
	clipActionNewEntry
)

type clipboardModel struct {
	client  *client.Client
	table   table.Model
	entries []client.ClipboardEntry

	action   clipAction
	textArea textarea.Model

	loading bool
	err     error
	width   int
	height  int
}

// messages
type clipboardLoadedMsg struct {
	entries []client.ClipboardEntry
	err     error
}

type clipboardCopiedMsg struct {
	id  int64
	err error
}

type clipboardSentMsg struct {
	err error
}

func newClipboardModel(c *client.Client) clipboardModel {
	columns := []table.Column{
		{Title: "ID", Width: 8},
		{Title: "Created", Width: 22},
		{Title: "Content", Width: 60},
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

	ta := textarea.New()
	ta.Placeholder = "Type clipboard content..."
	ta.SetHeight(5)
	ta.SetWidth(60)
	ta.CharLimit = 10000

	return clipboardModel{
		client:   c,
		table:    t,
		textArea: ta,
	}
}

func (m clipboardModel) init() tea.Cmd {
	return func() tea.Msg {
		entries, err := m.client.ClipboardList(context.Background(), 50)
		return clipboardLoadedMsg{entries: entries, err: err}
	}
}

func (m *clipboardModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.table.SetHeight(max(h-6, 5))

	cols := m.table.Columns()
	if len(cols) == 3 {
		cols[0].Width = 8
		cols[1].Width = 22
		cols[2].Width = max(w-8-22-3*2-2, 20)
		m.table.SetColumns(cols)
	}

	m.textArea.SetWidth(max(w-4, 30))
}

func (m clipboardModel) update(msg tea.Msg) (clipboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case clipboardLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, func() tea.Msg { return statusMsg{text: msg.err.Error(), isErr: true} }
		}
		m.entries = msg.entries
		m.err = nil
		m.table.SetRows(m.buildRows())
		return m, nil

	case clipboardCopiedMsg:
		if msg.err != nil {
			return m, func() tea.Msg { return statusMsg{text: msg.err.Error(), isErr: true} }
		}
		return m, func() tea.Msg {
			return statusMsg{text: fmt.Sprintf("Copied entry %d to system clipboard", msg.id), isErr: false}
		}

	case clipboardSentMsg:
		if msg.err != nil {
			return m, func() tea.Msg { return statusMsg{text: msg.err.Error(), isErr: true} }
		}
		return m, tea.Batch(
			func() tea.Msg { return statusMsg{text: "Clipboard item sent", isErr: false} },
			m.init(),
		)

	case tea.KeyMsg:
		// New entry overlay
		if m.action == clipActionNewEntry {
			switch msg.String() {
			case "ctrl+s":
				content := strings.TrimSpace(m.textArea.Value())
				if content == "" {
					return m, nil
				}
				m.action = clipActionNone
				m.textArea.Reset()
				return m, func() tea.Msg {
					_, err := m.client.ClipboardSend(context.Background(), content)
					return clipboardSentMsg{err: err}
				}
			case "esc":
				m.action = clipActionNone
				m.textArea.Reset()
				return m, nil
			default:
				var cmd tea.Cmd
				m.textArea, cmd = m.textArea.Update(msg)
				return m, cmd
			}
		}

		// Normal mode
		switch msg.String() {
		case "r":
			m.loading = true
			return m, m.init()
		case "c":
			entry := m.selectedEntry()
			if entry == nil {
				return m, nil
			}
			id := entry.ID
			content := entry.Content
			return m, func() tea.Msg {
				err := clipboard.WriteAll(content)
				return clipboardCopiedMsg{id: id, err: err}
			}
		case "n":
			m.action = clipActionNewEntry
			m.textArea.Focus()
			return m, textarea.Blink
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m clipboardModel) view() string {
	var b strings.Builder

	if m.loading {
		b.WriteString(helpStyle.Render("  Loading clipboard..."))
		return b.String()
	}

	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		return b.String()
	}

	if m.action == clipActionNewEntry {
		b.WriteString(titleStyle.Render("New Clipboard Entry"))
		b.WriteString("\n\n")
		b.WriteString("  " + m.textArea.View())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  ctrl+s send  esc cancel"))
		return b.String()
	}

	if len(m.entries) == 0 {
		b.WriteString(helpStyle.Render("  No clipboard entries yet. Press n to create one."))
		return b.String()
	}

	b.WriteString(m.table.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  c copy  n new entry  r refresh"))

	return b.String()
}

func (m clipboardModel) buildRows() []table.Row {
	rows := make([]table.Row, 0, len(m.entries))
	for _, e := range m.entries {
		snippet := e.Content
		// Replace newlines for table display
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		maxW := max(m.width-8-22-3*2-2, 20)
		if len(snippet) > maxW {
			snippet = snippet[:maxW-3] + "..."
		}
		rows = append(rows, table.Row{
			strconv.FormatInt(e.ID, 10),
			e.CreatedAt,
			snippet,
		})
	}
	return rows
}

func (m clipboardModel) selectedEntry() *client.ClipboardEntry {
	row := m.table.SelectedRow()
	if row == nil {
		return nil
	}
	id, err := strconv.ParseInt(row[0], 10, 64)
	if err != nil {
		return nil
	}
	for i := range m.entries {
		if m.entries[i].ID == id {
			return &m.entries[i]
		}
	}
	return nil
}
