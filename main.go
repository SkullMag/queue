package main

import (
	"fmt"
	"os"
	"strings"
)

const usage = `queue — run shell commands through a background queue

usage:
  queue                       attach the TUI (starts the daemon if needed)
  queue add "cmd" ["cmd" ...] submit one or more commands to the queue
  queue ls                    print a one-shot snapshot of the queue
  queue logs <id>             print the output of a task by its id
  queue stop                  stop the daemon and clear the queue
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
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: queue add <command>")
			os.Exit(1)
		}
		// All remaining args form a single command, so quoting is optional:
		//   queue add echo hello world   ->   "echo hello world"
		err = addTask(strings.Join(args[1:], " "))
	case args[0] == "ls", args[0] == "list":
		err = listTasks()
	case args[0] == "stop":
		err = stopDaemon()
	case args[0] == "logs", args[0] == "log":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: queue logs <id>")
			os.Exit(1)
		}
		err = showLog(args[1])
	case args[0] == "-h", args[0] == "--help", args[0] == "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
