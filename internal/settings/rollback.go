package settings

import (
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/configedit"
)

// ---- rollback_settings ---------------------------------------------------

type rollbackIn struct {
	Steps int  `json:"steps,omitempty"`
	All   bool `json:"all,omitempty"`
}

type rollbackOut struct {
	OK        bool                       `json:"ok"`
	Reverted  []configedit.RollbackEntry `json:"reverted,omitempty"`
	Batches   int                        `json:"batches"`
	Remaining int                        `json:"remaining"`
	Reloaded  bool                       `json:"reloaded"`
	Note      string                     `json:"note,omitempty"`
}

func rollbackSettings(deps Deps) functiontool.Func[rollbackIn, rollbackOut] {
	return func(_ adk.ToolContext, in rollbackIn) (rollbackOut, error) {
		steps := in.Steps
		switch {
		case in.All:
			steps = -1 // revert everything ("back to the initial state")
		case steps <= 0:
			steps = 1 // default: undo the most recent change
		}
		res, err := configedit.RollbackHistory(steps)
		if err != nil {
			return rollbackOut{}, err
		}
		out := rollbackOut{
			OK:        true,
			Reverted:  res.Reverted,
			Batches:   res.Batches,
			Remaining: res.Remaining,
			Reloaded:  deps.reload(),
		}
		if len(res.Reverted) == 0 {
			out.Note = "nothing to undo"
		}
		return out, nil
	}
}

// ---- settings_history ----------------------------------------------------

type historyIn struct {
	Limit int `json:"limit,omitempty"`
}

type historyOut struct {
	Changes []configedit.HistoryChange `json:"changes"`
	Note    string                     `json:"note,omitempty"`
}

func settingsHistory(_ adk.ToolContext, in historyIn) (historyOut, error) {
	ch := configedit.History()
	if in.Limit > 0 && len(ch) > in.Limit {
		ch = ch[:in.Limit]
	}
	out := historyOut{Changes: ch}
	if len(ch) == 0 {
		out.Note = "no settings changes recorded yet — nothing to undo"
	}
	return out, nil
}
