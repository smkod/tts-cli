package cli

import (
	"context"
	"os"
	"os/exec"
	"regexp"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/spf13/cobra"
)

var googleProjectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

func (a *app) login() *cobra.Command {
	login := &cobra.Command{Use: "login", Short: "Set up provider credentials"}
	var project string
	google := command("google", "Sign in to Google using Application Default Credentials", nil, exactly(0),
		func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("project") && project == "" {
				return core.Fail("INVALID_INPUT", "Provide a Google Cloud project ID.", false)
			}
			if err := googleLogin(cmd.Context(), project, func(ctx context.Context, args ...string) error {
				process, err := gcloudCommand(ctx, args...)
				if err != nil {
					return err
				}
				process.Stdin = os.Stdin
				process.Stdout = a.stderr
				process.Stderr = a.stderr
				return process.Run()
			}); err != nil {
				return err
			}
			a.result = map[string]string{"provider": "google", "quota_project": project}
			a.human = "Google Application Default Credentials configured. Run ttsi doctor --online to check access."
			return nil
		})
	google.Flags().StringVar(&project, "project", "", "Set the ADC quota project by ID")
	login.AddCommand(google)
	return login
}

func googleLogin(ctx context.Context, project string, run func(context.Context, ...string) error) error {
	if project != "" && !googleProjectID.MatchString(project) {
		return core.Fail("INVALID_INPUT", "Provide a valid Google Cloud project ID.", false)
	}
	if err := runGcloudStep(ctx, run, "auth", "application-default", "login"); err != nil {
		return err
	}
	if project != "" {
		return runGcloudStep(ctx, run, "auth", "application-default", "set-quota-project", project)
	}
	return nil
}

func runGcloudStep(ctx context.Context, run func(context.Context, ...string) error, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := run(ctx, args...)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, ok := err.(*exec.Error); ok {
		return core.Fail("AUTH_SETUP", "gcloud is not available on PATH. Install Google Cloud CLI and open a new terminal.", false)
	}
	if args[2] == "set-quota-project" {
		return core.Fail("AUTH_SETUP", "Google sign-in succeeded, but setting the quota project failed. Check the gcloud output.", false)
	}
	return core.Fail("AUTH_SETUP", "Google sign-in failed. Check the gcloud output.", false)
}
