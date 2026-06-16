package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	pendingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	runningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	doneStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	failedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("63"))
)

// displayName is the label shown for a task: its name if set, else the command.
func displayName(t TaskView) string {
	if t.Name != "" {
		return t.Name
	}
	return t.Cmd
}

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
	cursor int
	closed bool

	width  int
	height int

	// tail mode: when set, the body shows a live view of one task's log.
	tailing bool
	tailID  int
	tailLog string
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
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if m.tailing {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "q", "esc", "t":
				m.tailing = false
				m.tailLog = ""
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "down", "j":
			if m.cursor < len(m.tasks)-1 {
				m.cursor++
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "t", "enter":
			if m.cursor < len(m.tasks) {
				m.tailing = true
				m.tailID = m.tasks[m.cursor].ID
				m.tailLog = readLog(m.tailID)
			}
		}
	case stateMsg:
		m.tasks = msg.Tasks
		if m.cursor >= len(m.tasks) {
			m.cursor = len(m.tasks) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, waitForState(m.states)
	case connClosedMsg:
		m.closed = true
		return m, tea.Quit
	case tickMsg:
		if m.tailing {
			m.tailLog = readLog(m.tailID)
		}
		return m, tick()
	}
	return m, nil
}

// readLog returns the current contents of a task's log file, or a short
// placeholder if it hasn't been created yet.
func readLog(id int) string {
	data, err := os.ReadFile(logPath(id))
	if err != nil {
		return ""
	}
	return string(data)
}

func (m tuiModel) View() string {
	if m.tailing {
		return m.tailView()
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("queue") + "\n\n")

	if len(m.tasks) == 0 {
		b.WriteString(pendingStyle.Render("  (empty — submit with `queue add \"cmd\"`)") + "\n")
	}
	for i, t := range m.tasks {
		line := fmt.Sprintf("%3d  %s  %-7s  %s", t.ID, statusSymbol(t.Status), elapsedString(t), displayName(t))
		if i == m.cursor {
			line = "▌ " + line
			b.WriteString(selectedStyle.Render(line) + "\n")
		} else {
			b.WriteString(statusStyle(t.Status).Render("  "+line) + "\n")
		}
	}

	b.WriteString("\n" + helpStyle.Render("↑/↓ move · t tail selected · q quit"))
	return b.String()
}

// tailView renders the live log of the selected task, showing the last lines
// that fit on screen (newest at the bottom, like `tail -f`).
func (m tuiModel) tailView() string {
	var head string
	if t := findTaskView(m.tasks, m.tailID); t != nil {
		head = fmt.Sprintf("tail %d  %s  %s", t.ID, statusSymbol(t.Status), displayName(*t))
	} else {
		head = fmt.Sprintf("tail %d", m.tailID)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(head) + "\n\n")

	if strings.TrimSpace(m.tailLog) == "" {
		b.WriteString(pendingStyle.Render("  (no output yet)") + "\n")
	} else {
		lines := strings.Split(strings.TrimRight(m.tailLog, "\n"), "\n")
		// Reserve rows for the title (2), blank line, and footer (2).
		max := m.height - 5
		if max > 0 && len(lines) > max {
			lines = lines[len(lines)-max:]
		}
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("q/esc/t back · ctrl+c quit"))
	return b.String()
}

// findTaskView returns the task with the given id, or nil.
func findTaskView(tasks []TaskView, id int) *TaskView {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
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

	p := tea.NewProgram(tuiModel{conn: conn, states: states}, tea.WithAltScreen())
	_, err = p.Run()
	conn.Close()
	return err
}
