package tui

import profilescreen "github.com/shrug-labs/aipack/cmd/aipack/tui/screens/profiles"

func seedProfiles(m rootModel, seeds ...profilescreen.Seed) rootModel {
	return m.setProfilesScreen(m.profilesScreen().WithProfiles(seeds))
}
