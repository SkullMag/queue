package main

import (
	"fmt"
	"os"
	"strings"
)

const usage = `queue — run shell commands through a background queue

usage:
  queue                       attach the TUI (starts the daemon if needed)
  queue add [--name <l>] [--wait] <cmd> submit a command (optionally labeled);
                              --wait blocks until it finishes, exiting with its status
  queue ls [--reverse]        print a one-shot snapshot of the queue
                              --reverse/-r shows newest tasks first
  queue logs <id>             print the output of a task by its id
  queue tail [id]             follow a task's output live (running task if no id)
  queue clear                 empty the queue (keeps the daemon and running task)
  queue stop                  stop the daemon and clear the queue
  queue install               always-run the daemon via a launchd agent
  queue uninstall             remove the launchd agent
  queue daemon                run the queue daemon (started automatically)
`

func main() {
	args := os.Args[1:]

	var err error
	switch {
	case len(args) == 0:
		err = runTUI()
	case args[0] == "daemon":
		err = runDaemon()
	case args[0] == "add":
		// Optional leading --name/-n and --wait/-w flags (in any order), then
		// the command. The command is all remaining args joined, so quoting is
		// optional:
		//   queue add echo hello world                  -> "echo hello world"
		//   queue add --name build make release         -> name "build", cmd "make release"
		//   queue add --wait --name build make release  -> submit, then block until it finishes
		rest := args[1:]
		name := ""
		wait := false
		for len(rest) > 0 {
			switch {
			case strings.HasPrefix(rest[0], "--name="):
				name = strings.TrimPrefix(rest[0], "--name=")
				rest = rest[1:]
			case rest[0] == "--name" || rest[0] == "-n":
				if len(rest) < 2 {
					fmt.Fprintln(os.Stderr, "usage: queue add [--name <label>] [--wait] <command>")
					os.Exit(1)
				}
				name = rest[1]
				rest = rest[2:]
			case rest[0] == "--wait" || rest[0] == "-w":
				wait = true
				rest = rest[1:]
			default:
				goto cmdParsed
			}
		}
	cmdParsed:
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: queue add [--name <label>] [--wait] <command>")
			os.Exit(1)
		}
		if wait {
			err = addTaskWait(strings.Join(rest, " "), name)
		} else {
			err = addTask(strings.Join(rest, " "), name)
		}
	case args[0] == "ls", args[0] == "list":
		reverse := false
		if len(args) > 1 && (args[1] == "--reverse" || args[1] == "-r") {
			reverse = true
		}
		err = listTasks(reverse)
	case args[0] == "clear":
		err = clearQueue()
	case args[0] == "stop":
		err = stopDaemon()
	case args[0] == "install":
		err = installAgent()
	case args[0] == "uninstall":
		err = uninstallAgent()
	case args[0] == "logs", args[0] == "log":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: queue logs <id>")
			os.Exit(1)
		}
		err = showLog(args[1])
	case args[0] == "tail":
		err = tailLog(args[1:])
	case args[0] == "-h", args[0] == "--help", args[0] == "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	if err != nil {
		// A --wait command that finished non-zero propagates its own status
		// without an extra "error:" line; the command's output already streamed.
		if ee, ok := err.(*exitError); ok {
			os.Exit(ee.code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
