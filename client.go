package main

import (
	"encoding/json"
	"fmt"
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

// addTask submits a single command to the queue.
func addTask(cmd string) error {
	conn, err := connect()
	if err != nil {
		return err
	}
	defer conn.Close()
	// Capture the submitting shell's cwd and env so the command runs as if
	// invoked here, not in the daemon's (stale) context.
	dir, _ := os.Getwd()
	req := Request{Type: "add", Cmd: cmd, Dir: dir, Env: os.Environ()}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	fmt.Printf("queued: %s\n", cmd)
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

// listTasks prints a one-shot snapshot of the queue.
func listTasks() error {
	conn, err := net.Dial("unix", sockPath())
	if err != nil {
		fmt.Println("queue is empty (no daemon running)")
		return nil
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Type: "list"}); err != nil {
		return err
	}
	var st StateMsg
	if err := json.NewDecoder(conn).Decode(&st); err != nil {
		return err
	}
	if len(st.Tasks) == 0 {
		fmt.Println("queue is empty")
		return nil
	}
	for _, t := range st.Tasks {
		fmt.Printf("%3d  %s  %-7s  %s\n", t.ID, statusSymbol(t.Status), elapsedString(t), t.Cmd)
	}
	return nil
}
