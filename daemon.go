package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var errAlreadyRunning = errors.New("daemon already running")

type daemon struct {
	mu     sync.Mutex
	tasks  []*TaskView
	subs   map[net.Conn]struct{}
	work   chan *TaskView
	nextID int
}

// runDaemon is the entry point for `queue daemon`. It runs until the process
// is stopped (`queue stop`, a signal, or reboot).
func runDaemon() error {
	os.MkdirAll(queueDir(), 0o755) // ensure the socket's directory exists
	ln, err := listen(sockPath())
	if err != nil {
		if errors.Is(err, errAlreadyRunning) {
			return nil // another daemon won the race; nothing to do
		}
		return err
	}
	defer ln.Close()
	defer os.Remove(sockPath())

	// Fresh log dir per daemon (task IDs restart at 1 with each daemon).
	os.RemoveAll(logDir())
	os.MkdirAll(logDir(), 0o755)

	// Remove the socket on Ctrl-C / kill so the next client doesn't dial a
	// dead address (which would make it spawn a competing daemon).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		os.Remove(sockPath())
		os.Exit(0)
	}()

	d := &daemon{
		subs: make(map[net.Conn]struct{}),
		work: make(chan *TaskView, 4096),
	}
	go d.worker()
	d.serve(ln)
	return nil
}

// listen binds the Unix socket, clearing a stale socket file if no live
// daemon is answering on it.
func listen(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err == nil {
		return ln, nil
	}
	if c, derr := net.Dial("unix", path); derr == nil {
		c.Close()
		return nil, errAlreadyRunning
	}
	// Stale socket left by a dead daemon.
	os.Remove(path)
	return net.Listen("unix", path)
}

func (d *daemon) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go d.handle(conn)
	}
}

func (d *daemon) handle(conn net.Conn) {
	dec := json.NewDecoder(conn)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			conn.Close()
			return
		}
		switch req.Type {
		case "add":
			// Multiple adds may arrive on one connection; processing them
			// here (one goroutine per connection) keeps submit order.
			id := d.add(req.Cmd, req.Name, req.Dir, req.Env)
			// A waiting client needs the assigned id to follow the task to
			// completion; hand it back, then close (it polls state + log).
			if req.Wait {
				data, _ := json.Marshal(AddedMsg{Type: "added", ID: id})
				conn.Write(append(data, '\n'))
				conn.Close()
				return
			}
		case "list":
			d.writeState(conn)
			conn.Close()
			return
		case "shutdown":
			conn.Close()
			os.Remove(sockPath())
			os.Exit(0)
		case "subscribe":
			d.mu.Lock()
			d.subs[conn] = struct{}{}
			d.mu.Unlock()
			d.writeState(conn)
			// Block until the client disconnects.
			io.Copy(io.Discard, conn)
			d.mu.Lock()
			delete(d.subs, conn)
			d.mu.Unlock()
			conn.Close()
			return
		default:
			conn.Close()
			return
		}
	}
}

// add appends a task and enqueues it for the worker, returning its id.
func (d *daemon) add(cmd, name, dir string, env []string) int {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	t := &TaskView{ID: id, Cmd: cmd, Name: name, Dir: dir, Env: env, Status: "pending"}
	d.tasks = append(d.tasks, t)
	d.work <- t // buffered; serialized under the lock to preserve order
	d.mu.Unlock()
	d.broadcast()
	return id
}

// worker runs queued tasks one at a time.
func (d *daemon) worker() {
	for t := range d.work {
		d.mu.Lock()
		t.Status = "running"
		t.StartedMs = time.Now().UnixMilli()
		d.mu.Unlock()
		d.broadcast()

		start := time.Now()
		c := exec.Command("sh", "-c", t.Cmd)
		c.Dir = t.Dir // empty string means the daemon's own cwd
		if len(t.Env) > 0 {
			c.Env = t.Env // run with the submitting shell's environment
		}
		logf, _ := os.Create(logPath(t.ID))
		if logf != nil {
			c.Stdout = logf
			c.Stderr = logf
		}
		err := c.Run()
		if logf != nil {
			logf.Close()
		}

		d.mu.Lock()
		t.ElapsedMs = time.Since(start).Milliseconds()
		if err != nil {
			t.Status = "failed"
		} else {
			t.Status = "done"
		}
		d.mu.Unlock()
		d.broadcast()

		if err != nil {
			notify("Queue: command failed", displayName(*t))
		} else {
			notify("Queue: command finished", displayName(*t))
		}
	}
}

func (d *daemon) snapshot() StateMsg {
	out := make([]TaskView, len(d.tasks))
	for i, t := range d.tasks {
		out[i] = *t
	}
	return StateMsg{Type: "state", Tasks: out}
}

// writeState sends a single current snapshot to one connection.
func (d *daemon) writeState(conn net.Conn) {
	d.mu.Lock()
	msg := d.snapshot()
	d.mu.Unlock()
	data, _ := json.Marshal(msg)
	conn.Write(append(data, '\n'))
}

// broadcast pushes the current snapshot to every subscriber.
func (d *daemon) broadcast() {
	d.mu.Lock()
	msg := d.snapshot()
	subs := make([]net.Conn, 0, len(d.subs))
	for c := range d.subs {
		subs = append(subs, c)
	}
	d.mu.Unlock()

	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	for _, c := range subs {
		if _, err := c.Write(data); err != nil {
			d.mu.Lock()
			delete(d.subs, c)
			d.mu.Unlock()
			c.Close()
		}
	}
}

// notify shows a macOS notification via osascript.
func notify(title, message string) {
	script := fmt.Sprintf("display notification %q with title %q", message, title)
	_ = exec.Command("osascript", "-e", script).Run()
}
