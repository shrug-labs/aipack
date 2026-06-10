package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

// ConfigParamsCmd manages profile-scoped params used by `{params.*}` refs.
type ConfigParamsCmd struct {
	List  ConfigParamsListCmd  `cmd:"" help:"List params for a profile"`
	Get   ConfigParamsGetCmd   `cmd:"" help:"Print one profile param value"`
	Set   ConfigParamsSetCmd   `cmd:"" help:"Set a profile param"`
	Unset ConfigParamsUnsetCmd `cmd:"" help:"Remove a profile param"`
}

func (c *ConfigParamsCmd) Help() string {
	return `Manage profile-scoped params used by {params.*} refs. Params are
stored in the selected profile, but configured under config alongside env
values and sync defaults.

Examples:
  aipack config params list
  aipack config params set workspace team-alpha
  aipack config params set tracker_url https://tracker.example.com --profile oncall
  aipack config params unset old_param --profile oncall`
}

type ConfigParamsListCmd struct {
	Profile string `help:"Profile to read (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
	JSON    bool   `help:"Emit machine-readable JSON" name:"json"`
}

func (c *ConfigParamsListCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, profileName, err := resolveConfigParamsProfile(g, c.Profile)
	if err != nil {
		return err
	}
	result, err := app.ProfileParamsList(app.ProfileParamsRequest{ConfigDir: cfgDir, ProfileName: profileName})
	if err != nil {
		return err
	}
	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, result)
	}
	fmt.Fprintf(g.Stdout, "Profile: %s\n", result.Profile)
	if len(result.Params) == 0 {
		fmt.Fprintln(g.Stdout, "No params set.")
		fmt.Fprintln(g.Stdout, "Use 'aipack config params set KEY VALUE' to add one.")
		return nil
	}
	for _, param := range result.Params {
		fmt.Fprintf(g.Stdout, "  %s=%s\n", param.Key, param.Value)
	}
	return nil
}

type ConfigParamsGetCmd struct {
	Key     string `arg:"" help:"Parameter key"`
	Profile string `help:"Profile to read (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *ConfigParamsGetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, profileName, err := resolveConfigParamsProfile(g, c.Profile)
	if err != nil {
		return err
	}
	value, ok, err := app.ProfileParamGet(app.ProfileParamRequest{ConfigDir: cfgDir, ProfileName: profileName, Key: c.Key})
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(g.Stderr, "%s is not set in profile %s\n", strings.TrimSpace(c.Key), profileName)
		return ExitError{Code: cmdutil.ExitFail}
	}
	fmt.Fprintln(g.Stdout, value)
	return nil
}

type ConfigParamsSetCmd struct {
	Key     string `arg:"" help:"Parameter key"`
	Value   string `arg:"" help:"Parameter value"`
	Profile string `help:"Profile to edit (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *ConfigParamsSetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, profileName, err := resolveConfigParamsProfile(g, c.Profile)
	if err != nil {
		return err
	}
	if err := app.ProfileSetParam(app.ProfileParamRequest{
		ConfigDir: cfgDir, ProfileName: profileName, Key: c.Key, Value: c.Value, Stdout: g.Stdout,
	}); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Set %s in profile %s\n", strings.TrimSpace(c.Key), profileName)
	return nil
}

type ConfigParamsUnsetCmd struct {
	Key     string `arg:"" help:"Parameter key"`
	Profile string `help:"Profile to edit (default: sync-config defaults.profile, then 'default')" name:"profile" predictor:"profile"`
}

func (c *ConfigParamsUnsetCmd) Run(_ context.Context, g *Globals) error {
	cfgDir, profileName, err := resolveConfigParamsProfile(g, c.Profile)
	if err != nil {
		return err
	}
	if err := app.ProfileUnsetParam(app.ProfileParamRequest{
		ConfigDir: cfgDir, ProfileName: profileName, Key: c.Key, Stdout: g.Stdout,
	}); err != nil {
		return err
	}
	fmt.Fprintf(g.Stdout, "Unset %s in profile %s\n", strings.TrimSpace(c.Key), profileName)
	return nil
}

func resolveConfigParamsProfile(g *Globals, explicit string) (string, string, error) {
	cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
	if err != nil {
		return "", "", err
	}
	return cfgDir, effectiveProfile(explicit, cfgDir), nil
}
