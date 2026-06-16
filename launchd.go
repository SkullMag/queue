package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const launchdLabel = "com.queue.daemon"

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

// installAgent registers a launchd agent so the daemon starts at login and is
// kept alive (restarted if it ever exits).
func installAgent() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	logFile := filepath.Join(queueDir(), "daemon.log")
	if err := os.MkdirAll(queueDir(), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(plistTemplate, launchdLabel, exe, logFile, logFile)
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}

	// Hand the socket over to the launchd-managed instance: stop any
	// auto-spawned daemon, then (re)load the agent.
	stopDaemon()
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", domain+"/"+launchdLabel).Run() // ignore: may not be loaded
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap failed: %v: %s", err, out)
	}

	fmt.Printf("installed launchd agent %s\n", launchdLabel)
	fmt.Printf("  plist:  %s\n", path)
	fmt.Printf("  binary: %s\n", exe)
	fmt.Println("the daemon now starts at login and restarts if it exits.")
	return nil
}

// uninstallAgent removes the launchd agent.
func uninstallAgent() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", domain+"/"+launchdLabel).Run() // ignore if not loaded
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("removed launchd agent %s\n", launchdLabel)
	return nil
}
