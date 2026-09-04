package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var newRAMDiskCommand = func(ctx context.Context, executable string, arguments ...string) *exec.Cmd {
	return exec.CommandContext(ctx, executable, arguments...)
}

func runRAMDiskCommand(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := newRAMDiskCommand(commandContext, executable, arguments...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, message)
	}
	return output, nil
}
