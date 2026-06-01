package gc

import (
	"context"
	"fmt"
	"os/exec"
)

type Executor struct {
	binary string
}

type Options struct {
	ConfigPath     string
	DryRun         bool
	DeleteUntagged bool
}

func NewExecutor(binary string) *Executor {
	if binary == "" {
		binary = "registry"
	}
	return &Executor{binary: binary}
}

func (e *Executor) Run(ctx context.Context, opts Options) (string, error) {
	if opts.ConfigPath == "" {
		return "", fmt.Errorf("registry config path is required")
	}
	args := []string{"garbage-collect"}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	if opts.DeleteUntagged {
		args = append(args, "--delete-untagged")
	}
	args = append(args, opts.ConfigPath)

	cmd := exec.CommandContext(ctx, e.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("registry garbage-collect failed: %w", err)
	}
	return string(output), nil
}
