package terminal

// PTY backend built on github.com/aymanbagabas/go-pty, which gives one API over
// Windows ConPTY and unix98 ptys and, crucially, tears a ConPTY down correctly
// (closing the pseudo-console so a blocked Read returns rather than corrupting
// the heap). A waiter goroutine records process exit so List/Snapshot can reap
// terminals even when ConPTY has not yet surfaced EOF on the read side.

import (
	"os"
	"runtime"
	"strings"
	"sync/atomic"

	gopty "github.com/aymanbagabas/go-pty"
)

type goPty struct {
	p    gopty.Pty
	cmd  *gopty.Cmd
	done atomic.Bool
}

func startPTY(opts SpawnOptions) (ptyProc, error) {
	p, err := gopty.New()
	if err != nil {
		return nil, err
	}
	name, args := buildShellCommand(opts)
	cmd := p.Command(name, args...)
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	}
	cmd.Env = append(os.Environ(), opts.ExtraEnv...)
	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return nil, err
	}
	g := &goPty{p: p, cmd: cmd}
	go func() {
		_ = cmd.Wait()
		g.done.Store(true)
	}()
	return g, nil
}

func (g *goPty) Read(p []byte) (int, error)     { return g.p.Read(p) }
func (g *goPty) Write(p []byte) (int, error)    { return g.p.Write(p) }
func (g *goPty) Resize(cols, rows uint16) error { return g.p.Resize(int(cols), int(rows)) }
func (g *goPty) Close() error                   { return g.p.Close() }
func (g *goPty) Exited() bool                   { return g.done.Load() }
func (g *goPty) Terminate() error {
	if g.cmd != nil && g.cmd.Process != nil {
		return g.cmd.Process.Kill()
	}
	return nil
}

// buildShellCommand mirrors the shell selection in manager.rs: PowerShell by
// default on Windows (-NoLogo -NoProfile, -NoExit when an initial command
// runs), cmd.exe with /K; the login shell with -c elsewhere.
func buildShellCommand(opts SpawnOptions) (string, []string) {
	if runtime.GOOS == "windows" {
		shell := opts.Shell
		if shell == "" {
			shell = "powershell.exe"
		}
		low := strings.ToLower(shell)
		switch {
		case strings.Contains(low, "powershell"), strings.Contains(low, "pwsh"):
			args := []string{"-NoLogo", "-NoProfile"}
			if opts.InitialCommand != "" {
				args = append(args, "-NoExit", "-Command", opts.InitialCommand)
			}
			return shell, args
		case strings.Contains(low, "cmd"):
			if opts.InitialCommand != "" {
				return shell, []string{"/K", opts.InitialCommand}
			}
			return shell, nil
		default:
			if opts.InitialCommand != "" {
				return shell, []string{opts.InitialCommand}
			}
			return shell, nil
		}
	}
	shell := opts.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
	}
	if opts.InitialCommand != "" {
		return shell, []string{"-c", opts.InitialCommand}
	}
	return shell, nil
}
