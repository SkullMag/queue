package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Wire protocol: newline-delimited JSON over a Unix socket.

// Request is sent client -> daemon.
type Request struct {
	Type string   `json:"type"`          // "add" | "subscribe" | "list" | "shutdown"
	Cmd  string   `json:"cmd,omitempty"`
	Dir  string   `json:"dir,omitempty"` // working directory to run the command in
	Env  []string `json:"env,omitempty"` // environment of the submitting shell
}

// TaskView is the daemon's view of a single task, sent to clients.
type TaskView struct {
	ID        int      `json:"id"`
	Cmd       string   `json:"cmd"`
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

// sockPath returns the Unix socket path shared by daemon and clients.
func sockPath() string {
	dir := os.TempDir()
	return filepath.Join(dir, "queue.sock")
}

// logDir holds one log file per task (combined stdout+stderr).
func logDir() string {
	return filepath.Join(os.TempDir(), "queue-logs")
}

// logPath returns the log file path for a given task ID.
func logPath(id int) string {
	return filepath.Join(logDir(), fmt.Sprintf("%d.log", id))
}
