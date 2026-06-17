package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// connect dials the daemon, spawning it in the background if it isn't
// running yet.
func connect() (net.Conn, error) {
	if c, err := net.Dial("unix", sockPath()); err == nil {
		return c, nil
	}
	if err := spawnDaemon(); err != nil {
		return nil, err
	}
	for range 100 {
		time.Sleep(50 * time.Millisecond)
		if c, err := net.Dial("unix", sockPath()); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("could not reach daemon")
}

// spawnDaemon launches `queue daemon` detached from this process.
func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // own session: survives parent
	return cmd.Start()
}

// addTask submits a single command to the queue, with an optional label.
func addTask(cmd, name string) error {
	conn, err := connect()
	if err != nil {
		return err
	}
	defer conn.Close()
	// Capture the submitting shell's cwd and env so the command runs as if
	// invoked here, not in the daemon's (stale) context.
	dir, _ := os.Getwd()
	req := Request{Type: "add", Cmd: cmd, Name: name, Dir: dir, Env: os.Environ()}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	if name != "" {
		fmt.Printf("queued: %s (%s)\n", name, cmd)
	} else {
		fmt.Printf("queued: %s\n", cmd)
	}
	return nil
}

// addTaskWait submits a command and blocks until it finishes, streaming its
// output live. It returns an error (so the caller exits non-zero) if the task
// fails. Designed for unattended callers that need a build's exit status.
func addTaskWait(cmd, name string) error {
	conn, err := connect()
	if err != nil {
		return err
	}
	dir, _ := os.Getwd()
	req := Request{Type: "add", Cmd: cmd, Name: name, Dir: dir, Env: os.Environ(), Wait: true}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		conn.Close()
		return err
	}
	var added AddedMsg
	err = json.NewDecoder(conn).Decode(&added)
	conn.Close()
	if err != nil {
		return fmt.Errorf("daemon did not return a task id: %w", err)
	}
	if name != "" {
		fmt.Printf("queued: %s (%s) [id %d]\n", name, cmd, added.ID)
	} else {
		fmt.Printf("queued: %s [id %d]\n", cmd, added.ID)
	}
	return waitTask(added.ID)
}

// waitTask blocks until task id reaches a terminal status, streaming its output
// (including time spent pending in the queue). Returns an error if it failed.
func waitTask(id int) error {
	if st, err := fetchState(); err == nil && findTask(st, id) == nil {
		return fmt.Errorf("no such task %d", id)
	}
	if err := followLog(id); err != nil {
		return err
	}
	st, err := fetchState()
	if err != nil {
		return err
	}
	t := findTask(st, id)
	if t == nil {
		return fmt.Errorf("task %d disappeared (daemon restarted)", id)
	}
	if t.Status == "failed" {
		return fmt.Errorf("task %d failed", id)
	}
	return nil
}

// stopDaemon asks a running daemon to exit (clearing its queue).
func stopDaemon() error {
	conn, err := net.Dial("unix", sockPath())
	if err != nil {
		fmt.Println("no daemon running")
		return nil
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Type: "shutdown"}); err != nil {
		return err
	}
	fmt.Println("daemon stopped")
	return nil
}

// showLog prints the captured output (stdout+stderr) of one task.
func showLog(idStr string) error {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return fmt.Errorf("invalid task id: %q", idStr)
	}
	data, err := os.ReadFile(logPath(id))
	if err != nil {
		fmt.Printf("no output for task %d (not started yet, or daemon restarted)\n", id)
		return nil
	}
	os.Stdout.Write(data)
	return nil
}

// fetchState asks the daemon for a one-shot snapshot of the queue.
func fetchState() (StateMsg, error) {
	conn, err := net.Dial("unix", sockPath())
	if err != nil {
		return StateMsg{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Type: "list"}); err != nil {
		return StateMsg{}, err
	}
	var st StateMsg
	err = json.NewDecoder(conn).Decode(&st)
	return st, err
}

func findTask(st StateMsg, id int) *TaskView {
	for i := range st.Tasks {
		if st.Tasks[i].ID == id {
			return &st.Tasks[i]
		}
	}
	return nil
}

// listTasks prints a one-shot snapshot of the queue.
func listTasks(reverse bool) error {
	st, err := fetchState()
	if err != nil {
		fmt.Println("queue is empty (no daemon running)")
		return nil
	}
	if len(st.Tasks) == 0 {
		fmt.Println("queue is empty")
		return nil
	}
	tasks := st.Tasks
	if reverse {
		// Iterate in reverse order
		for i := len(tasks) - 1; i >= 0; i-- {
			t := tasks[i]
			fmt.Printf("%3d  %s  %-7s  %s\n", t.ID, statusSymbol(t.Status), elapsedString(t), displayName(t))
		}
	} else {
		for _, t := range tasks {
			fmt.Printf("%3d  %s  %-7s  %s\n", t.ID, statusSymbol(t.Status), elapsedString(t), displayName(t))
		}
	}
	return nil
}

// tailLog follows a task's output (like `tail -f`) until the task finishes.
// With no id it follows the running task, or the most recent one.
func tailLog(args []string) error {
	st, err := fetchState()
	if err != nil {
		fmt.Println("queue is empty (no daemon running)")
		return nil
	}

	var id int
	if len(args) >= 1 {
		if id, err = strconv.Atoi(args[0]); err != nil {
			return fmt.Errorf("invalid task id: %q", args[0])
		}
	} else {
		id = latestOrRunning(st)
		if id == 0 {
			fmt.Println("queue is empty")
			return nil
		}
	}
	if findTask(st, id) == nil {
		fmt.Printf("no such task %d\n", id)
		return nil
	}
	return followLog(id)
}

// latestOrRunning returns the running task's id, or else the highest id.
func latestOrRunning(st StateMsg) int {
	latest := 0
	for _, t := range st.Tasks {
		if t.Status == "running" {
			return t.ID
		}
		if t.ID > latest {
			latest = t.ID
		}
	}
	return latest
}

// followLog streams the log file for id, polling for appended output until
// the task reaches a terminal status.
func followLog(id int) error {
	path := logPath(id)

	// Wait for the log to appear (it's created when the task starts running).
	var f *os.File
	for {
		var err error
		if f, err = os.Open(path); err == nil {
			break
		}
		if taskFinished(id) {
			fmt.Printf("no output for task %d\n", id)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			os.Stdout.Write(buf[:n])
			continue
		}
		if err == io.EOF {
			if taskFinished(id) {
				// Drain anything written since the last read, then stop.
				for {
					n, _ := f.Read(buf)
					if n == 0 {
						break
					}
					os.Stdout.Write(buf[:n])
				}
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}

// taskFinished reports whether a task has reached a terminal status (or is
// gone, e.g. the daemon was restarted).
func taskFinished(id int) bool {
	st, err := fetchState()
	if err != nil {
		return true
	}
	t := findTask(st, id)
	return t == nil || t.Status == "done" || t.Status == "failed"
}
