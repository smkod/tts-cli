//go:build !windows

package cli

import (
	"context"
	"os/exec"
)

func gcloudCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	path, err := exec.LookPath("gcloud")
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, path, args...), nil
}
