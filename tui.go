package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	pendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	runningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	doneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	failedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func statusSymbol(s string) string {
	switch s {
	case "pending":
		return "○"
	case "running":
		return "◐"
	case "done":
		return "✓"
	case "failed":
		return "✗"
	}
	return "?"
}

func statusStyle(s string) lipgloss.Style {
	switch s {
	case "running":
		return runningStyle
	case "done":
		return doneStyle
	case "failed":
		return failedStyle
	}
	return pendingStyle
}

// elapsedString renders a task's duration, computing it live for a
// running task from its start timestamp.
func elapsedString(t TaskView) string {
	var d time.Duration
	switch t.Status {
	case "running":
		d = time.Since(time.UnixMilli(t.StartedMs))
	case "done", "failed":
		d = time.Duration(t.ElapsedMs) * time.Millisecond
	default:
		return "-"
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// --- Bubble Tea model ---

type tuiModel struct {
	conn   net.Conn
	states chan StateMsg
	tasks  []TaskView
	closed bool
}

type stateMsg StateMsg
type connClosedMsg struct{}
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitForState blocks on the next snapshot from the reader goroutine.
func waitForState(ch chan StateMsg) tea.Cmd {
	return func() tea.Msg {
		s, ok := <-ch
		if !ok {
			return connClosedMsg{}
		}
		return stateMsg(s)
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(waitForState(m.states), tick())
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case stateMsg:
		m.tasks = msg.Tasks
		return m, waitForState(m.states)
	case connClosedMsg:
		m.closed = true
		return m, tea.Quit
	case tickMsg:
		return m, tick()
	}
	return m, nil
}

func (m tuiModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("queue") + "\n\n")

	if len(m.tasks) == 0 {
		b.WriteString(pendingStyle.Render("  (empty — submit with `queue add \"cmd\"`)") + "\n")
	}
	for _, t := range m.tasks {
		st := statusStyle(t.Status)
		line := fmt.Sprintf("%3d  %s  %-7s  %s", t.ID, statusSymbol(t.Status), elapsedString(t), t.Cmd)
		b.WriteString(st.Render(line) + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("q/ctrl+c to quit · `queue logs <id>` for output"))
	return b.String()
}

// runTUI connects to the daemon, subscribes, and renders live state.
func runTUI() error {
	conn, err := connect()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(conn).Encode(Request{Type: "subscribe"}); err != nil {
		return err
	}

	states := make(chan StateMsg)
	go func() {
		dec := json.NewDecoder(conn)
		for {
			var s StateMsg
			if err := dec.Decode(&s); err != nil {
				close(states)
				return
			}
			states <- s
		}
	}()

	p := tea.NewProgram(tuiModel{conn: conn, states: states})
	_, err = p.Run()
	conn.Close()
	return err
}
