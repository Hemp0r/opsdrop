package main

import (
	"errors"
	"fmt"
	"strings"

	"opsdrop/internal/client"
	"opsdrop/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

func newUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Launch the interactive TUI for browsing files and clipboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := client.LoadConfig()
			if err != nil {
				return err
			}

			if strings.TrimSpace(cfg.Token) == "" {
				return errors.New("not logged in; run 'opsdrop auth login' first")
			}

			c := client.New(cfg)
			m := tui.New(c)

			p := tea.NewProgram(m, tea.WithAltScreen())
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}
			return nil
		},
	}
}
