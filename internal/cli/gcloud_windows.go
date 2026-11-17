package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func gcloudCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	path, err := exec.LookPath("gcloud")
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		return exec.CommandContext(ctx, path, args...), nil
	}
	if strings.ContainsAny(path, "%\"\r\n") {
		return nil, errors.New("gcloud path is not safe for cmd.exe")
	}
	systemRoot := os.Getenv("SystemRoot")
	if !filepath.IsAbs(systemRoot) {
		return nil, errors.New("cannot locate Windows system directory")
	}
	shell := filepath.Join(systemRoot, "System32", "cmd.exe")
	process := exec.CommandContext(ctx, shell)
	// The SDK provides a cmd launcher. Arguments are fixed tokens and an
	// optional validated project ID, not arbitrary shell input.
	process.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `"` + shell + `" /d /v:off /s /c ""` + path + `" ` + strings.Join(args, " ") + `"`,
	}
	return process, nil
}
