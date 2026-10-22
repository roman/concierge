package conciergecli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type projectCommands struct {
	Set struct {
		Repo     string  `arg:"" help:"Repository name used by backlog intake."`
		Merge    *string `enum:"on,off" help:"Operator-approved merge authority."`
		Delivery *string `enum:"pr,fast-forward" help:"Operator-approved delivery mode."`
		Branch   *string `help:"Operator-approved target branch; an empty value restores the remote default."`
	} `cmd:"" help:"Set approved repository settings atomically."`
	Show struct {
		Repo string `arg:"" help:"Repository name used by backlog intake."`
	} `cmd:"" help:"Read operator-owned settings; defaults are merge off, delivery pr, and the remote default branch."`
}

type projectSettings struct {
	Merge    string `json:"merge"`
	Delivery string `json:"delivery"`
	Branch   string `json:"branch,omitempty"`
}

func (command *projectCommands) show(root string, output io.Writer) int {
	path := filepath.Join(root, "config", "projects.json")
	settings, err := readProjectSettings(path, command.Show.Repo)
	if err != nil {
		return fail(
			output,
			1,
			err,
			"ask the operator to correct "+path+"; merge must be off|on and delivery must be pr|fast-forward",
		)
	}
	if _, err := fmt.Fprintf(output, "repo: %s\nmerge: %s\ndelivery: %s\n", quote(command.Show.Repo), settings.Merge, settings.Delivery); err != nil {
		return 1
	}
	if settings.Branch != "" {
		if _, err := fmt.Fprintf(output, "branch: %s\n", quote(settings.Branch)); err != nil {
			return 1
		}
	}
	return 0
}

func readProjectSettings(path, repo string) (projectSettings, error) {
	defaults := projectSettings{Merge: "off", Delivery: "pr"}
	projects, err := readProjects(path)
	if err != nil {
		return projectSettings{}, err
	}
	settings := projects[repo]
	if settings.Merge == "" {
		settings.Merge = defaults.Merge
	}
	if settings.Delivery == "" {
		settings.Delivery = defaults.Delivery
	}
	return settings, nil
}

func readProjects(path string) (map[string]projectSettings, error) {
	data, err := readOptionalConfiguration(path)
	if err == nil && data == nil {
		return map[string]projectSettings{}, nil
	}
	if err != nil {
		return nil, err
	}
	var projects map[string]projectSettings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&projects); err != nil {
		return nil, err
	}
	if projects == nil {
		return nil, errors.New("project settings must be a JSON object")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("project settings must contain one JSON object")
	}
	for name, settings := range projects {
		if settings.Branch != "" {
			if settings.Branch == "HEAD" || strings.HasPrefix(settings.Branch, "-") {
				return nil, fmt.Errorf("invalid target branch for %q", name)
			}
			if err := exec.Command("git", "check-ref-format", "refs/heads/"+settings.Branch).Run(); err != nil {
				return nil, fmt.Errorf("invalid target branch for %q: %w", name, err)
			}
		}
		if settings.Merge != "" && settings.Merge != "off" && settings.Merge != "on" {
			return nil, fmt.Errorf("invalid merge setting for %q", name)
		}
		if settings.Delivery != "" && settings.Delivery != "pr" &&
			settings.Delivery != "fast-forward" {
			return nil, fmt.Errorf("invalid delivery setting for %q", name)
		}
	}
	return projects, nil
}
