package shell

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
)

// ExitCodeOrError keeps exit code from application termination
// either error if application failed in any stage.
type ExitCodeOrError struct {
	ExitCode int
	Error    error
}

// App struct keep everything regarding external application started process
// including command line, wait channel which tracks process completion
// and exit code ether any exception happened in any stage of
// application start up or completion.
type App struct {
	cmd             *exec.Cmd
	waitCh          chan ExitCodeOrError
	exitCodeOrError atomic.Value
}

// NewApp return new application instance defined by executable name
// and arguments, and ready to start by following Run call.
func NewApp(name string, args ...string) *App {
	cmd := exec.Command(name, args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	app := &App{cmd: cmd}
	return app
}

// AddEnvironments add environments in the form "key=value".
func (app *App) AddEnvironments(env []string) {
	if app.cmd.Env == nil {
		app.cmd.Env = os.Environ()
	}
	app.cmd.Env = append(app.cmd.Env, env...)
}

// Run start application synchronously with link to the process
// stdout/stderr output, to get output.
// Method doesn't return control until the application
// finishes its execution.
func (app *App) Run(stdin io.Reader, stdout, stderr io.Writer) ExitCodeOrError {
	_, err := app.Start(stdin, stdout, stderr)
	if err != nil {
		return ExitCodeOrError{0, err}
	}
	/*
		err = syscall.Setpriority(1, app.cmd.Process.Pid, 19)
		if err != nil {
			return ExitCodeOrError{0, err}
		}
	*/
	st := app.Wait()
	return st
}

func (app *App) sendExitCodeOrError(exitCode int, err error) {
	state := &ExitCodeOrError{ExitCode: exitCode, Error: err}
	// log.Printf("Exit status: %+v", state)
	app.exitCodeOrError.Store(state)
	app.waitCh <- *state
}

func (app *App) asyncWait() {
	defer close(app.waitCh)

	err := app.cmd.Wait()
	var exitCode int
	if err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if stat, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				exitCode = stat.ExitStatus()
				// reset error, since exitCode already not equal to zero
				err = nil
			}
		}
	}
	app.sendExitCodeOrError(exitCode, err)
}

// Start run application asynchronously and
// return channel to wait/track exit state and status.
// If application failed to run, error returned,
func (app *App) Start(stdin io.Reader, stdout, stderr io.Writer) (chan ExitCodeOrError, error) {
	if stdin != nil {
		app.cmd.Stdin = stdin
	}
	if stdout != nil {
		app.cmd.Stdout = stdout
	}
	if stderr != nil {
		app.cmd.Stderr = stderr
	}
	err := app.cmd.Start()
	if err != nil {
		return nil, err
	}
	app.waitCh = make(chan ExitCodeOrError)
	go app.asyncWait()
	return app.waitCh, nil
}

// CheckIsInstalled use Linux/FreeBSD utility [which] to find
// if app is installed or not in the system.
func (app *App) CheckIsInstalled() error {
	// Won't use [whereis], because it doesn't return correct exit code
	// based on search results. Can use [type], as an option.
	whApp := NewApp("which", app.cmd.Path)
	st := whApp.Run(nil, nil, nil)
	if st.Error != nil {
		return st.Error
	}
	if st.ExitCode != 0 {
		return fmt.Errorf("App \"%s\" does not exist", app.cmd.Path)
	}
	return nil
}

// ExitCodeOrError return exit status once application has been finished.
func (app *App) ExitCodeOrError() *ExitCodeOrError {
	ref := app.exitCodeOrError.Load()
	return ref.(*ExitCodeOrError)
}

// Wait switch from asynchronous mode to synchronous
// and wait until application is finished.
func (app *App) Wait() ExitCodeOrError {
	st, ok := <-app.waitCh
	if ok {
		return st
	} else {
		return ExitCodeOrError{ExitCode: 0, Error: fmt.Errorf("Exited already")}
	}
}

// Kill terminate application started asynchronously.
func (app *App) Kill() error {
	//log.Println(fmt.Sprintf("Start killing app: %v", app.cmd))
	if IsLinuxMacOSFreeBSD() {
		// Kill not only main but all child processes,
		// so extract for this purpose group id.
		pgid, err := syscall.Getpgid(app.cmd.Process.Pid)
		if err != nil {
			return err
		}
		// Specifying gid with negative sign also results in the killing of child processes.
		err = syscall.Kill(-pgid, syscall.SIGKILL)
		if err != nil {
			return err
		}
	} else {
		// Kill only mother process
		err := app.cmd.Process.Kill()
		if err != nil {
			return err
		}
	}
	state := app.Wait()
	//log.Println(fmt.Sprintf("Done killing app: %v", app.cmd))
	return state.Error
}
