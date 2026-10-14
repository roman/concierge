package conciergecli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

type projectCommands struct {
	Show struct {
		Repo string `arg:"" help:"Repository name used by backlog intake."`
	} `cmd:"" help:"Read operator-owned merge and delivery settings; defaults are off and pr."`
}

type projectSettings struct {
	Merge    string `json:"merge"`
	Delivery string `json:"delivery"`
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
	return 0
}

func readProjectSettings(path, repo string) (projectSettings, error) {
	defaults := projectSettings{Merge: "off", Delivery: "pr"}
	data, err := readOptionalConfiguration(path)
	if err == nil && data == nil {
		return defaults, nil
	}
	if err != nil {
		return projectSettings{}, err
	}
	var projects map[string]projectSettings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&projects); err != nil {
		return projectSettings{}, err
	}
	if projects == nil {
		return projectSettings{}, errors.New("project settings must be a JSON object")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return projectSettings{}, errors.New("project settings must contain one JSON object")
	}
	for name, settings := range projects {
		if settings.Merge != "" && settings.Merge != "off" && settings.Merge != "on" {
			return projectSettings{}, fmt.Errorf("invalid merge setting for %q", name)
		}
		if settings.Delivery != "" && settings.Delivery != "pr" &&
			settings.Delivery != "fast-forward" {
			return projectSettings{}, fmt.Errorf("invalid delivery setting for %q", name)
		}
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
