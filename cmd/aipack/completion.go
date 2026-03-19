package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/posener/complete"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// completionPredictors returns named predictors for shell completion.
// Each predictor reads the filesystem at completion time to provide
// dynamic suggestions.
func completionPredictors() map[string]complete.Predictor {
	return map[string]complete.Predictor{
		"pack":            complete.PredictFunc(predictPacks),
		"profile":         complete.PredictFunc(predictProfiles),
		"prompt":          complete.PredictFunc(predictPrompts),
		"registry-source": complete.PredictFunc(predictRegistrySources),
		"resource":        complete.PredictFunc(predictResources),
		"harness":         complete.PredictSet(append(domain.HarnessNames(), "all")...),
		"kind":            complete.PredictSet("rule", "skill", "workflow", "agent", "pack"),
		"trace-type":      complete.PredictSet("rule", "agent", "workflow", "skill", "mcp"),
		"category":        complete.PredictSet("ops", "dev", "infra", "governance", "meta"),
	}
}

func predictPacks(a complete.Args) []string {
	dir, err := configBaseDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(app.PacksDir(dir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			names = append(names, e.Name())
		}
	}
	return names
}

func predictProfiles(a complete.Args) []string {
	dir, err := configBaseDir()
	if err != nil {
		return nil
	}
	names, err := config.ListProfileNames(filepath.Join(dir, "profiles"))
	if err != nil {
		return nil
	}
	return names
}

func predictPrompts(a complete.Args) []string {
	dir, err := configBaseDir()
	if err != nil {
		return nil
	}
	prompts, err := app.PromptList(dir)
	if err != nil {
		return nil
	}
	names := make([]string, len(prompts))
	for i, p := range prompts {
		names[i] = p.Name
	}
	return names
}

func predictRegistrySources(a complete.Args) []string {
	dir, err := configBaseDir()
	if err != nil {
		return nil
	}
	sources, err := app.RegistrySources(dir)
	if err != nil {
		return nil
	}
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.Name
	}
	return names
}

func predictResources(a complete.Args) []string {
	dir, err := configBaseDir()
	if err != nil {
		return nil
	}
	packs, err := app.PackListDetailed(dir)
	if err != nil {
		return nil
	}

	// If the preceding arg indicates a resource type, narrow to that category.
	cats := domain.AllPackCategories()
	for _, arg := range a.Completed {
		if cat, ok := domain.ParseSingularLabel(arg); ok {
			cats = []domain.PackCategory{cat}
		}
	}

	seen := map[string]bool{}
	var names []string
	for _, p := range packs {
		for _, cat := range cats {
			for _, name := range p.ContentIDs(cat) {
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func configBaseDir() (string, error) {
	return config.DefaultConfigDir(os.Getenv("HOME"))
}
