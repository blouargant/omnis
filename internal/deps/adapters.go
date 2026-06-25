package deps

import (
	"context"
	"fmt"

	bashtools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/askuser"
)

// installTimeoutSecs bounds a single dependency install command.
const installTimeoutSecs = 600

// BashInstaller is an Installer that runs the install command through the Bash
// safety floor (so catastrophic commands are still blocked even though the
// install was user-approved).
func BashInstaller(ctx context.Context, command string) (string, error) {
	return bashtools.RunBash(ctx, bashtools.BashIn{Command: command, Timeout: installTimeoutSecs})
}

const (
	choiceInstall = "Install"
	choiceSkip    = "Skip"
)

// NewAskuserConfirmer returns a Confirmer that asks the user — in the active
// session — to approve a dependency install via the askuser registry. A nil
// registry or an empty session id declines (so off-surface contexts such as a
// sub-agent runner never hang waiting for an answer nobody can give).
func NewAskuserConfirmer(reg *askuser.Registry) Confirmer {
	return &askuserConfirmer{reg: reg}
}

type askuserConfirmer struct {
	reg *askuser.Registry
}

func (a *askuserConfirmer) ConfirmInstall(ctx context.Context, sessionID string, req Requirement) (bool, error) {
	if a == nil || a.reg == nil || sessionID == "" {
		return false, nil
	}
	q := askuser.Question{
		Kind: askuser.KindSingle,
		Prompt: fmt.Sprintf(
			"**Install %s?**\n\nThis is required but `%s` is not installed. Approving runs:\n\n```sh\n%s\n```",
			req.Name(), req.Command, req.Install.Command()),
		Choices: []string{choiceSkip, choiceInstall},
		Default: choiceSkip,
		Item:    &askuser.QuestionItem{Kind: "package", Name: req.Name()},
	}
	ans, err := a.reg.Ask(ctx, sessionID, q)
	if err != nil || ans.Cancelled || len(ans.Selected) == 0 {
		return false, err
	}
	return ans.Selected[0] == choiceInstall, nil
}
