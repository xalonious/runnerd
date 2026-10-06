//go:build !linux

package runner

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

func LockState(string) (func(), error) { return nil, fmt.Errorf("runnerd execution requires Linux") }
func processGroupAlive(int) bool       { return true }
func runProcess(context.Context, *exec.Cmd, time.Duration, func(int) error) (int, error) {
	return -1, fmt.Errorf("runnerd execution requires Linux")
}
