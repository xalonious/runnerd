//go:build linux

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

func LockState(state string) (func(), error) {
	fd, err := syscall.Open(filepath.Join(state, ".runnerd.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), "runnerd-state-lock")
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("state directory already locked by another broker: %w", err)
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
}

func processGroupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return !errors.Is(err, syscall.ESRCH)
}

func runProcess(ctx context.Context, cmd *exec.Cmd, grace time.Duration, onStart func(int) error) (int, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	done := make(chan struct{})
	var killers sync.WaitGroup
	cmd.Cancel = func() error {
		pid := cmd.Process.Pid
		err := syscall.Kill(-pid, syscall.SIGINT)
		killers.Add(1)
		go func() {
			defer killers.Done()
			timer := time.NewTimer(grace)
			defer timer.Stop()
			select {
			case <-done:
				return
			case <-timer.C:
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = grace + time.Second
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start official runner: %w", err)
	}
	startErr := onStart(cmd.Process.Pid)
	if startErr != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	err := cmd.Wait()
	close(done)
	killers.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	code := cmd.ProcessState.ExitCode()
	if startErr != nil {
		return code, fmt.Errorf("record runner process: %w", startErr)
	}
	if ctx.Err() != nil {
		return code, ctx.Err()
	}
	if err != nil {
		return code, fmt.Errorf("official runner exited: %w", err)
	}
	return code, nil
}
