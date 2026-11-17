package cli

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/smkod/tts-cli/internal/core"
)

func TestGoogleLogin(t *testing.T) {
	for _, tc := range []struct {
		name, project, wantCode string
		runErr                  error
		wantCalls               []string
	}{
		{"default", "", "", nil, []string{"auth application-default login"}},
		{"explicit quota project", "my-project", "", nil, []string{
			"auth application-default login",
			"auth application-default set-quota-project my-project",
		}},
		{"invalid project", "invalid&project", "INVALID_INPUT", nil, nil},
		{"missing gcloud", "my-project", "AUTH_SETUP", &exec.Error{
			Name: "gcloud", Err: exec.ErrNotFound,
		}, []string{"auth application-default login"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			err := googleLogin(context.Background(), tc.project, func(_ context.Context, args ...string) error {
				calls = append(calls, strings.Join(args, " "))
				return tc.runErr
			})
			code := ""
			if err != nil {
				code = core.Public(err).Code
			}
			if code != tc.wantCode || !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Fatalf("code = %q, calls = %v; want %q, %v", code, calls, tc.wantCode, tc.wantCalls)
			}
		})
	}
}
