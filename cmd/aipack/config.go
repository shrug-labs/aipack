package main

// ConfigCmd groups machine-local aipack configuration commands.
type ConfigCmd struct {
	Env EnvCmd `cmd:"" help:"Manage the config-dir .env file used by {env:*} refs"`
}

func (c *ConfigCmd) Help() string {
	return `Manage aipack configuration that is not specific to one profile.

Examples:
  aipack config env list
  aipack config env set API_TOKEN abc123`
}
