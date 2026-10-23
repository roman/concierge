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

	"github.com/roman/concierge/pkg/worktree"
)

type projectCommands struct {
	Set struct {
		Repo                    string   `arg:"" help:"Repository name used by backlog intake."`
		Merge                   *string  `enum:"on,off" help:"Operator-approved merge authority."`
		Delivery                *string  `enum:"pr,fast-forward" help:"Operator-approved delivery mode."`
		Branch                  *string  `help:"Operator-approved target branch; an empty value restores the remote default."`
		NixDevelopArgs          []string `name:"nix-develop-arg" sep:"none" help:"Operator-approved extra nix develop argument; repeat in order with --nix-develop-arg=VALUE."`
		ClearNixDevelopArgs     bool     `help:"Restore nix develop without extra project arguments."`
		ReturnIgnoredPaths      []string `name:"return-ignored-path" sep:"none" help:"Operator-approved ignored file or directory/ to remove during lease return; repeat to replace the list. No wildcards."`
		ClearReturnIgnoredPaths bool     `help:"Restore lease return without approved ignored paths."`
	} `cmd:"" help:"Set approved repository settings atomically."`
	Show struct {
		Repo string `arg:"" help:"Repository name used by backlog intake."`
	} `cmd:"" help:"Read operator-owned settings; defaults are merge off, delivery pr, and the remote default branch."`
}

type projectSettings struct {
	Merge              string   `json:"merge"`
	Delivery           string   `json:"delivery"`
	Branch             string   `json:"branch,omitempty"`
	NixDevelopArgs     []string `json:"nix_develop_args,omitempty"`
	ReturnIgnoredPaths []string `json:"return_ignored_paths,omitempty"`
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
	if err := writeNixDevelopArguments(output, settings.NixDevelopArgs); err != nil {
		return 1
	}
	if len(settings.ReturnIgnoredPaths) != 0 {
		values := make([]string, len(settings.ReturnIgnoredPaths))
		for index, path := range settings.ReturnIgnoredPaths {
			values[index] = quote(path)
		}
		if _, err := fmt.Fprintf(output, "return_ignored_paths[%d]: %s\n", len(values), strings.Join(values, ",")); err != nil {
			return 1
		}
	}
	return 0
}

func writeNixDevelopArguments(output io.Writer, arguments []string) error {
	if len(arguments) == 0 {
		return nil
	}
	values := make([]string, len(arguments))
	for index, argument := range arguments {
		values[index] = quote(argument)
	}
	_, err := fmt.Fprintf(
		output,
		"nix_develop_args[%d]: %s\n",
		len(values),
		strings.Join(values, ","),
	)
	return err
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
		if err := worktree.ValidateReturnIgnoredPaths(settings.ReturnIgnoredPaths); err != nil {
			return nil, fmt.Errorf("invalid return ignored paths for %q: %w", name, err)
		}
		for _, argument := range settings.NixDevelopArgs {
			if argument == "" || strings.ContainsAny(argument, "\x00\r\n") {
				return nil, fmt.Errorf("invalid nix develop argument for %q", name)
			}
		}
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
