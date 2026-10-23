package conciergecli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/roman/concierge/internal/lockfile"
)

type briefIncludeCommands struct {
	Set struct {
		File string `required:"" help:"Readable regular file of standing worker instructions."`
	} `cmd:"" help:"Replace standing instructions with the operator-approved file."`
	Clear struct{} `cmd:"" help:"Clear operator-approved standing instructions."`
}

func (command *projectCommands) set(root string, output io.Writer) int {
	if command.Set.Merge == nil && command.Set.Delivery == nil && command.Set.Branch == nil &&
		len(command.Set.NixDevelopArgs) == 0 &&
		!command.Set.ClearNixDevelopArgs && len(command.Set.ReturnIgnoredPaths) == 0 &&
		!command.Set.ClearReturnIgnoredPaths {
		return fail(
			output,
			2,
			errors.New("provide at least one setting"),
			"concierge project set REPO [--merge on|off] [--delivery pr|fast-forward] [--branch BRANCH]",
		)
	}
	if command.Set.ClearNixDevelopArgs && len(command.Set.NixDevelopArgs) != 0 {
		return fail(
			output,
			2,
			errors.New("cannot set and clear Nix arguments together"),
			"choose --nix-develop-arg or --clear-nix-develop-args",
		)
	}
	path := filepath.Join(root, "config", "projects.json")
	if command.Set.ClearReturnIgnoredPaths && len(command.Set.ReturnIgnoredPaths) != 0 {
		return fail(output, 2, errors.New("cannot set and clear return ignored paths together"),
			"choose --return-ignored-path or --clear-return-ignored-paths")
	}
	unlock, err := lockProjectSettings(filepath.Dir(path))
	if err != nil {
		return fail(output, 1, err, "provide a writable config directory and retry")
	}
	defer unlock()
	projects, err := readProjects(path)
	if err == nil {
		settings := projects[command.Set.Repo]
		if command.Set.Merge != nil {
			settings.Merge = *command.Set.Merge
		}
		if command.Set.Delivery != nil {
			settings.Delivery = *command.Set.Delivery
		}
		if command.Set.Branch != nil {
			settings.Branch = *command.Set.Branch
		}
		if len(command.Set.NixDevelopArgs) != 0 {
			settings.NixDevelopArgs = command.Set.NixDevelopArgs
		}
		if command.Set.ClearNixDevelopArgs {
			settings.NixDevelopArgs = nil
		}
		if len(command.Set.ReturnIgnoredPaths) != 0 {
			settings.ReturnIgnoredPaths = command.Set.ReturnIgnoredPaths
		}
		if command.Set.ClearReturnIgnoredPaths {
			settings.ReturnIgnoredPaths = nil
		}
		projects[command.Set.Repo] = settings
		var data []byte
		data, err = json.MarshalIndent(projects, "", "  ")
		if err == nil {
			err = writeConfiguration(path, append(data, '\n'), func(candidate string) error {
				_, err := readProjectSettings(candidate, command.Set.Repo)
				return err
			})
		}
	}
	if err != nil {
		return fail(output, 1, err, "inspect config/projects.json and retry the approved change")
	}
	command.Show.Repo = command.Set.Repo
	return command.show(root, output)
}

func (command *briefIncludeCommands) run(root, selected string, output io.Writer) int {
	var data []byte
	var err error
	if selected == "brief-include set" {
		data, err = readOptionalConfiguration(command.Set.File)
		if err == nil && data == nil {
			err = fmt.Errorf("instruction file does not exist: %s", command.Set.File)
		}
	}
	path := filepath.Join(root, "config", "brief-include.md")
	if err == nil {
		err = writeConfiguration(path, data, func(candidate string) error {
			_, err := readOptionalConfiguration(candidate)
			return err
		})
	}
	if err != nil {
		return fail(
			output,
			1,
			err,
			"provide a readable regular --file and writable config directory",
		)
	}
	if _, err := fmt.Fprintf(output, "brief_include: %s\nbytes: %d\n", quote(path), len(data)); err != nil {
		return 1
	}
	return 0
}

func lockProjectSettings(directory string) (func(), error) {
	return lockSettings(directory, ".projects.lock")
}

func lockSettings(directory, name string) (func(), error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := lockfile.Open(root, name)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}

func writeConfiguration(path string, data []byte, validate func(string) error) error {
	if _, err := readOptionalConfiguration(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := validate(file.Name()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
