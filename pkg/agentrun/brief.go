package agentrun

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ResolveBrief picks the brief from a path, then stdin, then the environment.
//
// AN EXPLICIT PATH WINS, because someone who passed one meant it, and
// silently preferring an inherited environment variable over an argument is
// how a Job ends up running last week's brief.
//
// A DASH MEANS STDIN, so a Plan can pipe one without a volume.
//
// proven-by: TestResolveBrief_PrefersThePathOverTheEnvironment
// proven-by: TestResolveBrief_ReadsStdinForADash
// proven-by: TestResolveBrief_FallsBackToTheEnvironment
// proven-by: TestResolveBrief_AnEmptyBriefIsRefused
// proven-by: TestResolveBrief_AMissingFileSaysWhichFile
func ResolveBrief(path string, stdin io.Reader, env func(string) string) (string, error) {
	var text string
	switch {
	case path == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("agentrun: reading the brief from stdin: %w", err)
		}
		text = string(b)
	case path != "":
		b, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator's own argument; reading it IS the command
		if err != nil {
			return "", fmt.Errorf("agentrun: reading the brief: %w", err)
		}
		text = string(b)
	default:
		if env != nil {
			text = env(EnvBrief)
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%w. Pass a file, - for stdin, or set %s. An agent "+
			"with nothing to do would spend a turn finding that out and then "+
			"exit 0", ErrNoBrief, EnvBrief)
	}
	return text, nil
}
