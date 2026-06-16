# queue

A minimal CLI that runs shell commands through a **background queue**. A daemon
owns the queue and runs commands sequentially; a separate TUI attaches to show
each command's live status and elapsed time. On macOS, a push notification
(via `osascript`) fires as each command finishes.

It's a single binary with subcommands, communicating over a Unix socket
(`$TMPDIR/queue.sock`). The daemon starts automatically the first time you add a
task or open the TUI — you never launch it by hand — and then stays running
(keeping task history) until you `queue stop` it or reboot.

## Build

```sh
go build -o queue .
```

## Usage

```sh
queue                          # attach the TUI (starts the daemon if needed)
queue add <command>            # submit a command (all args join into one command)
queue add --name <label> <cmd> # submit with a human-friendly label (-n also works)
queue ls                       # print a one-shot snapshot of the queue
queue logs <id>                # print a task's captured output
queue tail [id]                # follow a task's output live (running task if no id)
queue stop                     # stop the daemon and clear the queue
queue install                  # always-run the daemon via a launchd agent
queue uninstall                # remove the launchd agent
queue daemon                   # run the daemon (started automatically; rarely needed)
```

## Always-on daemon

By default the daemon is auto-spawned on first use and lives until you
`queue stop` it or reboot. To keep it running permanently — started at login
and automatically restarted if it ever exits — install it as a launchd agent:

```sh
queue install     # writes ~/Library/LaunchAgents/com.queue.daemon.plist and loads it
queue uninstall   # unloads and removes it
```

State lives under `~/.queue/` (socket, per-task logs, daemon log). The queue is
held in memory, so the *daemon* survives logout/reboot once installed, but the
*job list* resets if the daemon process itself restarts (crash or reboot).

`queue add` joins all its arguments into a single command, so quoting is
optional for plain words (`queue add echo hello world`). Quote when you use
shell operators, since your shell parses those first
(`queue add "make && ./run"`).

If the TUI ever stops reflecting new tasks, a stale daemon is the usual cause —
run `queue stop` (or `pkill -f 'queue daemon'`) to reset.

Typical flow — open the TUI in one terminal, submit from another:

```sh
# terminal 1
queue

# terminal 2
queue add "npm run build"
queue add "go test ./..." "sleep 5"
```

Status symbols: `○` pending · `◐` running · `✓` done · `✗` failed.
Press `q` or `ctrl+c` to quit the TUI (the daemon keeps running).

## Architecture

- **daemon** (`daemon.go`) — owns the queue, runs commands one at a time,
  fires notifications, and broadcasts state snapshots to subscribers.
- **client** (`client.go`) — dials the socket (spawning the daemon if absent);
  implements `add` and `ls`.
- **TUI** (`tui.go`) — subscribes and renders live state from the daemon.
- **protocol** (`protocol.go`) — newline-delimited JSON message types.
