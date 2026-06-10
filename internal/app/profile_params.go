package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
)

type ProfileParamsRequest struct {
	ConfigDir   string
	ProfileName string
}

type ProfileParamEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ProfileParamsResult struct {
	Profile     string              `json:"profile"`
	ProfilePath string              `json:"profile_path"`
	Params      []ProfileParamEntry `json:"params"`
}

func ProfileParamsList(req ProfileParamsRequest) (ProfileParamsResult, error) {
	cfg, name, path, err := loadProfileParamsConfig(req)
	if err != nil {
		return ProfileParamsResult{}, err
	}
	keys := make([]string, 0, len(cfg.Params))
	for key := range cfg.Params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	params := make([]ProfileParamEntry, 0, len(keys))
	for _, key := range keys {
		params = append(params, ProfileParamEntry{Key: key, Value: cfg.Params[key]})
	}
	return ProfileParamsResult{Profile: name, ProfilePath: path, Params: params}, nil
}

func ProfileParamGet(req ProfileParamRequest) (string, bool, error) {
	key := strings.TrimSpace(req.Key)
	if !validProfileParamKey(key) {
		return "", false, fmt.Errorf("invalid param key %q", req.Key)
	}
	cfg, _, _, err := loadProfileParamsConfig(ProfileParamsRequest{ConfigDir: req.ConfigDir, ProfileName: req.ProfileName})
	if err != nil {
		return "", false, err
	}
	value, ok := cfg.Params[key]
	return value, ok, nil
}

func loadProfileParamsConfig(req ProfileParamsRequest) (config.ProfileConfig, string, string, error) {
	if req.ConfigDir == "" {
		return config.ProfileConfig{}, "", "", fmt.Errorf("config dir is required")
	}
	name, err := config.NormalizeProfileName(req.ProfileName)
	if err != nil {
		return config.ProfileConfig{}, "", "", err
	}
	path, err := config.ResolveProfilePath("", req.ConfigDir, name, config.HomeDir())
	if err != nil {
		return config.ProfileConfig{}, "", "", err
	}
	cfg, err := config.LoadProfile(path)
	if err != nil {
		return config.ProfileConfig{}, "", "", err
	}
	return cfg, name, path, nil
}
