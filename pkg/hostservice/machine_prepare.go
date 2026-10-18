package hostservice

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskmeta"
)

const PrepareMachine Operation = "prepare_machine"

type MachinePreparationRequest struct {
	Label        string `json:"label"`
	ClientSource string `json:"client_source"`
}

type PreparedMachineResult struct {
	MachineID string             `json:"machine_id"`
	Target    string             `json:"target"`
	Client    machineprep.Client `json:"client"`
}

func validateMachinePreparation(input *MachinePreparationRequest) error {
	if input == nil || input.Label == "" || len(input.Label) > 256 ||
		strings.IndexFunc(input.Label, unicode.IsControl) >= 0 {
		return errors.New("machine preparation requires a saved machine label or ID")
	}
	_, err := taskmeta.ParseAbsolutePath(input.ClientSource)
	return err
}

func requestTimeout(operation Operation) time.Duration {
	if operation == PrepareMachine {
		return 25 * time.Minute
	}
	return operationTimeout
}

func (server *Server) prepareMachine(
	ctx context.Context,
	input MachinePreparationRequest,
) (Response, error) {
	if !server.machineMu.TryLock() {
		return Response{Outcome: Busy, Error: "machine preparation is already running"}, nil
	}
	defer server.machineMu.Unlock()
	root, err := os.OpenRoot(server.config.Root)
	if err != nil {
		return Response{}, err
	}
	defer root.Close()
	if err := stateversion.RequireMinimum(root, 3); err != nil {
		return Response{}, err
	}
	machines := server.preparationMachines
	if machines == nil {
		machines = herdr.MachineCatalog{
			Runner: herdr.NewClient(server.config.HerdrExecutable).Runner,
		}
	}
	machine, err := machines.Resolve(ctx, input.Label)
	if err != nil {
		return Response{}, err
	}
	configuration, err := readMachineConfiguration(server.config.Root, machine)
	if err != nil {
		return Response{}, err
	}
	origin := ""
	for _, repository := range configuration.Repositories {
		if repository.Name == "concierge" {
			origin = repository.URL
		}
	}
	bundle, err := machineprep.BundleSource(ctx, input.ClientSource, origin)
	if err != nil {
		return Response{}, err
	}
	defer bundle.Close()
	runner := server.preparationSSH
	if runner == nil {
		runner = sshcommand.SystemRunner{}
	}
	report, err := (machineprep.Checker{Runner: runner, Authentication: configuration.Authentication}).Check(
		ctx,
		machine,
		configuration.Agent,
		configuration.Repositories,
	)
	if err != nil {
		return Response{}, err
	}
	for _, check := range report.Checks {
		if check.Status != "ready" {
			return Response{}, errors.New(check.Name + ": " + check.Step)
		}
	}
	preparer := machineprep.Preparer{Runner: runner}
	if err = preparer.PrepareRepositories(ctx, machine, configuration); err != nil {
		return Response{}, errors.Join(ErrOutcomeUnknown, err)
	}
	client, err := preparer.BuildClient(ctx, machine, configuration, bundle.Commit, bundle)
	if err != nil {
		return Response{}, errors.Join(ErrOutcomeUnknown, err)
	}
	return Response{
		PreparedMachine: &PreparedMachineResult{
			MachineID: machine.ID,
			Target:    machine.Target,
			Client:    client,
		},
	}, nil
}

func readMachineConfiguration(
	root string,
	machine herdr.SavedMachine,
) (machineprep.Configuration, error) {
	file, err := os.OpenFile(
		filepath.Join(root, "config", "machines.json"),
		os.O_RDONLY|syscall.O_NONBLOCK,
		0,
	)
	if err != nil {
		return machineprep.Configuration{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return machineprep.Configuration{}, err
	}
	if !info.Mode().IsRegular() {
		return machineprep.Configuration{}, errors.New(
			"machine configuration must be a regular file",
		)
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return machineprep.Configuration{}, err
	}
	return machineprep.DecodeConfiguration(data, machine)
}

func validateMachinePreparationResponse(response Response, request Request) error {
	if request.Operation != PrepareMachine || validateRequest(request) != nil {
		return errors.New("host returned an unrelated machine preparation result")
	}
	if response.Task != nil || response.Transition != nil || response.Resolution != nil ||
		response.Assignment != nil ||
		response.Stopped != nil ||
		response.Control != nil ||
		response.Status != nil ||
		response.Supervision != nil ||
		response.Checkpoint != nil ||
		response.Abandoned != nil ||
		response.Reconciled != nil ||
		len(response.RemovedStoreLinks) != 0 {
		return errors.New("host returned other operation data during machine preparation")
	}
	if response.Outcome != Completed {
		if response.PreparedMachine != nil || response.Error == "" {
			return errors.New("host returned contradictory machine preparation data")
		}
		switch response.Outcome {
		case Failed, Invalid, Unauthorized, Busy, Uncertain:
			return nil
		}
		return errors.New("host returned an unknown machine preparation outcome")
	}
	prepared := response.PreparedMachine
	if prepared == nil || response.Error != "" || prepared.MachineID == "" ||
		!sshcommand.ValidTarget(prepared.Target) {
		return errors.New("host omitted prepared machine identity")
	}
	if decoded, err := hex.DecodeString(prepared.Client.Commit); err != nil || len(decoded) != 20 ||
		strings.ToLower(prepared.Client.Commit) != prepared.Client.Commit {
		return errors.New("host returned a mutable prepared client identity")
	}
	_, err := taskmeta.ParseAbsolutePath(prepared.Client.Executable)
	return err
}
