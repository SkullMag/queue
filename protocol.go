package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Wire protocol: newline-delimited JSON over a Unix socket.

// Request is sent client -> daemon.
type Request struct {
	Type string   `json:"type"`           // "add" | "subscribe" | "list" | "shutdown"
	Cmd  string   `json:"cmd,omitempty"`
	Name string   `json:"name,omitempty"` // optional human-friendly job label
	Dir  string   `json:"dir,omitempty"`  // working directory to run the command in
	Env  []string `json:"env,omitempty"`  // environment of the submitting shell
	Wait bool     `json:"wait,omitempty"` // on "add": reply with the assigned id so the client can block on it
}

// AddedMsg is the daemon's reply to an "add" request made with Wait set: it
// carries the id assigned to the new task so the client can follow it.
type AddedMsg struct {
	Type string `json:"type"` // "added"
	ID   int    `json:"id"`
}

// TaskView is the daemon's view of a single task, sent to clients.
type TaskView struct {
	ID        int      `json:"id"`
	Cmd       string   `json:"cmd"`
	Name      string   `json:"name,omitempty"`       // optional human-friendly job label
	Dir       string   `json:"dir,omitempty"`        // working directory for the command
	Status    string   `json:"status"`               // pending | running | done | failed
	StartedMs int64    `json:"started_ms,omitempty"` // when a running task began
	ElapsedMs int64    `json:"elapsed_ms,omitempty"` // final duration for done/failed
	Env       []string `json:"-"`                    // submit-time env; kept off the wire
}

// StateMsg is broadcast daemon -> client whenever the queue changes.
type StateMsg struct {
	Type  string     `json:"type"` // "state"
	Tasks []TaskView `json:"tasks"`
}

// queueDir is the stable per-user state directory. A fixed path (rather than
// $TMPDIR) keeps the socket consistent across shells and the launchd agent.
func queueDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".queue")
}

// sockPath returns the Unix socket path shared by daemon and clients.
func sockPath() string {
	return filepath.Join(queueDir(), "queue.sock")
}

// logDir holds one log file per task (combined stdout+stderr).
func logDir() string {
	return filepath.Join(queueDir(), "logs")
}

// logPath returns the log file path for a given task ID.
func logPath(id int) string {
	return filepath.Join(logDir(), fmt.Sprintf("%d.log", id))
}
