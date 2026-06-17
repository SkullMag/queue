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
	markerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
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

	// for vim-style gg movement
	lastKey     string
	lastKeyTime time.Time

	// for number-prefixed movements (e.g., 5j, 10G)
	countBuffer string

	// reverse order (newest first)
	reverseOrder bool
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

// getCount returns the numeric count from the buffer and clears it.
// Returns 1 if buffer is empty (default count).
func (m *tuiModel) getCount() int {
	if m.countBuffer == "" {
		return 1
	}
	count := 0
	fmt.Sscanf(m.countBuffer, "%d", &count)
	m.countBuffer = ""
	if count == 0 {
		return 1
	}
	return count
}

// getTaskAtCursor returns the task at the current cursor position,
// accounting for reverse order display.
func (m *tuiModel) getTaskAtCursor() *TaskView {
	if m.cursor >= len(m.tasks) || m.cursor < 0 {
		return nil
	}
	if m.reverseOrder {
		// In reverse mode, cursor 0 is the last task
		idx := len(m.tasks) - 1 - m.cursor
		return &m.tasks[idx]
	}
	return &m.tasks[m.cursor]
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
		key := msg.String()

		// Handle number keys for count prefix
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			m.countBuffer += key
			return m, nil
		}

		switch key {
		case "q", "ctrl+c":
			m.countBuffer = "" // clear on quit
			return m, tea.Quit
		case "down", "j":
			count := m.getCount()
			m.cursor += count
			if m.cursor >= len(m.tasks) {
				m.cursor = len(m.tasks) - 1
			}
		case "up", "k":
			count := m.getCount()
			m.cursor -= count
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "g":
			// gg or Ngg: go to top or line N
			if m.lastKey == "g" && time.Since(m.lastKeyTime) < 500*time.Millisecond {
				count := m.getCount()
				if count == 1 {
					// gg: go to top
					m.cursor = 0
				} else {
					// Ngg: go to task N (1-indexed)
					m.cursor = count - 1
					if m.cursor < 0 {
						m.cursor = 0
					}
					if m.cursor >= len(m.tasks) {
						m.cursor = len(m.tasks) - 1
					}
				}
				m.lastKey = ""
			} else {
				m.lastKey = "g"
				m.lastKeyTime = time.Now()
			}
		case "G":
			count := m.getCount()
			if count == 1 {
				// G: go to bottom
				if len(m.tasks) > 0 {
					m.cursor = len(m.tasks) - 1
				}
			} else {
				// NG: go to task N (1-indexed)
				m.cursor = count - 1
				if m.cursor < 0 {
					m.cursor = 0
				}
				if m.cursor >= len(m.tasks) {
					m.cursor = len(m.tasks) - 1
				}
			}
		case "r":
			m.countBuffer = "" // clear count on action
			m.reverseOrder = !m.reverseOrder
		case "t", "enter":
			m.countBuffer = "" // clear count on action
			if t := m.getTaskAtCursor(); t != nil {
				m.tailing = true
				m.tailID = t.ID
				m.tailLog = readLog(m.tailID)
			}
		default:
			// Clear count buffer on unrecognized key
			m.countBuffer = ""
		}
		// Clear lastKey if it's not 'g' and some time has passed
		if key != "g" && time.Since(m.lastKeyTime) > 500*time.Millisecond {
			m.lastKey = ""
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

	if m.reverseOrder {
		// Display in reverse order (newest first)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			t := m.tasks[i]
			line := fmt.Sprintf("%3d  %s  %-7s  %s", t.ID, statusSymbol(t.Status), elapsedString(t), displayName(t))
			displayIdx := len(m.tasks) - 1 - i
			if displayIdx == m.cursor {
				b.WriteString(markerStyle.Render("▌ ") + statusStyle(t.Status).Render(line) + "\n")
			} else {
				b.WriteString(statusStyle(t.Status).Render("  "+line) + "\n")
			}
		}
	} else {
		for i, t := range m.tasks {
			line := fmt.Sprintf("%3d  %s  %-7s  %s", t.ID, statusSymbol(t.Status), elapsedString(t), displayName(t))
			if i == m.cursor {
				b.WriteString(markerStyle.Render("▌ ") + statusStyle(t.Status).Render(line) + "\n")
			} else {
				b.WriteString(statusStyle(t.Status).Render("  "+line) + "\n")
			}
		}
	}

	b.WriteString("\n" + helpStyle.Render("j/k/↑/↓ move · gg top · G bottom · #j/k/G jump · r reverse · t tail · q quit"))
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

	p := tea.NewProgram(tuiModel{conn: conn, states: states, reverseOrder: true}, tea.WithAltScreen())
	_, err = p.Run()
	conn.Close()
	return err
}
