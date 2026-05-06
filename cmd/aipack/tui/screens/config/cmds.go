package configscreen

import (
	tea "charm.land/bubbletea/v2"

	"github.com/shrug-labs/aipack/internal/app"
)

type LoadedMsg struct {
	ProfileName string
	Refs        []app.ProfileRef
	Env         []app.EnvEntry
	Err         error
}

func LoadRefs(configDir string, profile Profile) tea.Cmd {
	return func() tea.Msg {
		result, err := app.ProfileRefs(app.ProfileRefsRequest{
			ConfigDir:   configDir,
			ProfileName: profile.Name,
			ProfilePath: profile.Path,
		})
		if err != nil {
			return LoadedMsg{ProfileName: profile.Name, Err: err}
		}
		env, err := app.EnvList(configDir, false)
		if err != nil {
			return LoadedMsg{ProfileName: result.Profile, Err: err}
		}
		return LoadedMsg{ProfileName: result.Profile, Refs: result.Refs, Env: env.Entries}
	}
}
