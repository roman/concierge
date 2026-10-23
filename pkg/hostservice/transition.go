package hostservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskmeta"
	"strings"
	"time"
	"unicode"

	"github.com/roman/concierge/pkg/taskstate"
)

const (
	Start           Operation = "start"
	Reopen          Operation = "reopen"
	Finish          Operation = "finish"
	Hold            Operation = "hold"
	Release         Operation = "release"
	Cleanup         Operation = "cleanup"
	ReleaseWorktree Operation = "release_worktree"
	Cancel          Operation = "cancel"
	Stop            Operation = "stop"
	Discard         Operation = "discard"
	CloseRetired    Operation = "close_retired"
	RecoverOrphaned Operation = "recover_orphaned"
)

type TransitionRequest struct {
	OperatorApproved bool                     `json:"operator_approved,omitempty"`
	TaskID           string                   `json:"task_id"`
	Generation       string                   `json:"generation,omitempty"`
	Mode             taskstate.CompletionMode `json:"mode,omitempty"`
	ClosedOn         string                   `json:"closed_on,omitempty"`
	Reason           string                   `json:"reason,omitempty"`
	Hold             *taskstate.Hold          `json:"hold,omitempty"`
}

func validateRequest(request Request) error {
	payloads := 0
	for _, present := range []bool{request.Intake != nil, request.Transition != nil, request.Resolve != nil, request.Adoption != nil, request.Assignment != nil, request.Control != nil, request.Supervision != nil, request.Launch != nil, request.Status != nil, request.Reconciliation != nil, request.MachinePreparation != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return errors.New("host operation requires exactly one payload")
	}
	if request.Operation == PrepareMachine {
		return validateMachinePreparation(request.MachinePreparation)
	}
	if request.MachinePreparation != nil {
		return errors.New("unrelated machine preparation payload")
	}
	if request.Operation == ReconcileReply {
		if request.Reconciliation == nil {
			return errors.New("reconciliation requires the complete original reply tuple")
		}
		return request.Reconciliation.Validate()
	}
	if request.Reconciliation != nil {
		return errors.New("unrelated reconciliation payload")
	}
	if request.Operation == HostStatus || request.Operation == HostStop {
		if request.Status == nil {
			return errors.New("status requires a status payload")
		}
		return nil
	}
	if request.Status != nil {
		return errors.New("unrelated status payload")
	}
	if request.Operation == Launch {
		return validateLaunchInput(request.Launch)
	}
	if request.Launch != nil {
		return errors.New("unrelated launch payload")
	}
	if request.Operation == Supervise || request.Operation == StopSupervision {
		return validateSupervisionInput(request.Operation, request.Supervision)
	}
	if request.Supervision != nil {
		return errors.New("unrelated supervision payload")
	}
	if request.Operation == ControlStart || request.Operation == ControlInterrupt {
		return validateControlInput(request.Operation, request.Control)
	}
	if request.Control != nil {
		return errors.New("unrelated control payload")
	}
	if request.Operation == Adopt {
		return validateAdoptionInput(request.Adoption)
	}
	if request.Operation == SendAssignment {
		return validateAssignmentInput(request.Assignment)
	}
	if request.Adoption != nil || request.Assignment != nil {
		return errors.New("unrelated worker payload")
	}

	if request.Operation == Resolve {
		return validateResolveRequest(request)
	}
	if request.Resolve != nil {
		return errors.New("unrelated resolution payload")
	}
	if request.Operation == Intake {
		if request.Intake == nil || request.Transition != nil {
			return errors.New("intake requires only an intake definition")
		}
		_, err := taskstate.NewQueued(*request.Intake)
		return err
	}
	input := request.Transition
	if input == nil || request.Intake != nil {
		return errors.New("lifecycle operation requires only a transition payload")
	}
	if input.OperatorApproved && request.Operation != Discard &&
		request.Operation != RecoverOrphaned {
		return errors.New("operator authority is unrelated to this operation")
	}
	if input.Reason != "" && request.Operation != CloseRetired &&
		request.Operation != RecoverOrphaned {
		return errors.New("closure reason is unrelated to this operation")
	}
	// Validate identifiers and optional hold with the same task contract as intake.
	if _, err := taskstate.NewQueued(taskstate.Definition{ID: input.TaskID, Title: "validation", Kind: "task", Repo: "validation", Hold: input.Hold}); err != nil {
		return err
	}
	switch request.Operation {
	case CloseRetired, RecoverOrphaned:
		if request.Operation == RecoverOrphaned && !input.OperatorApproved {
			return errors.New("orphan recovery requires explicit operator approval")
		}
		if input.Mode != "" || input.Hold != nil || strings.TrimSpace(input.Reason) == "" ||
			strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 {
			return errors.New(
				"close requires only a task ID, retired generation, reason and closing date",
			)
		}
		if err := taskmeta.ValidateSpawnGeneration(input.Generation); err != nil {
			return err
		}
		date, err := time.Parse("2006-01-02", input.ClosedOn)
		if err != nil || date.Format("2006-01-02") != input.ClosedOn {
			return errors.New("close requires a valid closing date")
		}
	case Discard:
		if !input.OperatorApproved || input.Generation == "" || input.Mode != "" ||
			input.ClosedOn != "" ||
			input.Hold != nil {
			return errors.New(
				"discard requires a task ID, generation and explicit operator approval",
			)
		}
		return taskmeta.ValidateSpawnGeneration(input.Generation)
	case Start, Release, Cancel, Reopen:
		if input.Hold != nil || input.Generation != "" || input.Mode != "" || input.ClosedOn != "" {
			return errors.New("start and release accept only a task ID")
		}
	case Hold:
		if input.Hold == nil || input.Generation != "" || input.Mode != "" || input.ClosedOn != "" {
			return errors.New("hold requires only a task ID and hold")
		}
	case Cleanup, Stop, AbandonLaunch, ReleaseWorktree:
		if input.Generation == "" || input.Mode != "" || input.ClosedOn != "" || input.Hold != nil {
			return errors.New("operation requires only a task ID and generation")
		}
		return taskmeta.ValidateSpawnGeneration(input.Generation)
	case Finish:
		if input.Hold != nil || input.Generation == "" {
			return errors.New("finish requires a generation and no hold")
		}
		if input.Mode != taskstate.Close && input.Mode != taskstate.Retain {
			return errors.New("finish mode must be close or retain")
		}
		if err := taskmeta.ValidateSpawnGeneration(input.Generation); err != nil {
			return err
		}
		if input.Mode == taskstate.Close {
			date, err := time.Parse("2006-01-02", input.ClosedOn)
			if err != nil || date.Format("2006-01-02") != input.ClosedOn {
				return errors.New("close requires a valid closing date")
			}
		} else if input.ClosedOn != "" {
			return errors.New("retain forbids a closing date")
		}

	default:
		return fmt.Errorf("unknown host operation %q", request.Operation)
	}
	return nil
}

func (server *Server) execute(
	ctx context.Context,
	request Request,
	reportRemovedLink func(string),
) (taskstate.Task, error) {
	updated, err := server.executeTransition(ctx, request, reportRemovedLink)
	if err != nil {
		return updated, err
	}
	switch request.Operation {
	case Cleanup, CloseRetired, Discard, RecoverOrphaned:
		return updated, server.service.RetireStalePrimaryWakes(ctx, server.config.MessageRoot)
	default:
		return updated, nil
	}
}

func (server *Server) executeTransition(
	ctx context.Context,
	request Request,
	reportRemovedLink func(string),
) (taskstate.Task, error) {
	if request.Operation == Intake {
		return server.create(ctx, *request.Intake)
	}
	input := request.Transition
	service := server.service.WithRemovedStoreLinkReporter(reportRemovedLink).
		WithRemovedPathReporter(reportRemovedLink)
	switch request.Operation {
	case RecoverOrphaned:
		return service.RecoverOrphaned(
			ctx,
			input.TaskID,
			input.Generation,
			input.Reason,
			input.ClosedOn,
			herdr.NewClient(server.config.HerdrExecutable),
		)
	case CloseRetired:
		return service.CloseRetired(
			ctx,
			server.config.MessageRoot,
			input.TaskID,
			input.Generation,
			input.Reason,
			input.ClosedOn,
			herdr.NewClient(server.config.HerdrExecutable),
		)
	case Start:
		return server.service.Start(ctx, input.TaskID)
	case Finish:
		return server.service.Finish(
			ctx,
			input.TaskID,
			input.Generation,
			input.Mode,
			input.ClosedOn,
		)
	case Hold:
		return server.service.PlaceHold(ctx, input.TaskID, *input.Hold)
	case Release:
		return server.service.ReleaseHold(ctx, input.TaskID)
	case Cancel:
		return server.service.CancelAdoption(ctx, input.TaskID)
	case Discard:
		return service.DiscardWorker(
			ctx,
			server.config.MessageRoot,
			input.TaskID,
			input.Generation,
			herdr.NewClient(server.config.HerdrExecutable),
		)
	case ReleaseWorktree:
		return service.ReleaseWorktree(ctx, input.TaskID, input.Generation)
	case Cleanup:
		if err := server.retireRemoteTransport(ctx, input.TaskID, input.Generation); err != nil {
			return taskstate.Task{}, err
		}
		return service.Retire(
			ctx,
			input.TaskID,
			input.Generation,
			herdr.NewClient(server.config.HerdrExecutable),
		)
	default:
		return taskstate.Task{}, errors.New("unknown host operation")
	}
}

func validateTransitionResponse(response Response, request Request) error {
	if err := validateRequest(request); err != nil {
		return err
	}
	if response.Transition == nil || response.Task != nil || response.Error != "" {
		return errors.New("host returned an incomplete lifecycle result")
	}
	task, err := taskstate.NewMigrated(*response.Transition)
	if err != nil {
		return err
	}
	input := request.Transition
	if task.ID() != input.TaskID {
		return errors.New("host returned a different task")
	}

	var repeated taskstate.Task
	switch request.Operation {
	case RecoverOrphaned:
		repeated, err = task.RecoverOrphaned(input.Generation, input.Reason, input.ClosedOn)
	case CloseRetired:
		repeated, err = task.CloseRetired(input.Generation, input.Reason, input.ClosedOn)
	case Start:
		repeated, err = task.Start()
	case Reopen:
		if task.State() != taskstate.InFlight || task.Snapshot().Completion != nil ||
			len(task.Snapshot().Reopens) == 0 {
			return errors.New("host returned an unapplied reopen result")
		}
		repeated = task
	case Hold:
		repeated, err = task.PlaceHold(*input.Hold)
	case Release:
		repeated, err = task.ReleaseHold()
	case Finish:
		repeated, err = task.AcceptResult(input.Generation, input.Mode, input.ClosedOn)
	case ReleaseWorktree:
		lease, found := task.RetiredLease(input.Generation)
		if !found || lease == (taskstate.WorktreeLease{}) ||
			lease.State != taskstate.LeaseReturned {
			return errors.New("host returned an unreleased worktree lease")
		}
		repeated, err = task.RecordLeaseReturned(input.Generation, lease)
	case Cleanup:
		repeated, err = task.RetireResult(input.Generation)
	case Cancel:
		repeated, err = task.CancelAdoption()
	case Discard:
		receipt, found := task.LaunchAbandonment(input.Generation)
		if !found || receipt.Kind != taskstate.DiscardedWorker ||
			receipt.Lease.State != taskstate.LeaseReturned {
			return errors.New("host returned an incomplete worker discard")
		}
		repeated, err = task.DiscardWorker(input.Generation)
	}
	if err != nil {
		return err
	}
	actual, err := task.Encode()
	if err != nil {
		return err
	}
	retried, err := repeated.Encode()
	if err != nil {
		return err
	}
	// A completed response must already satisfy the domain's identical retry.
	if !bytes.Equal(actual, retried) {
		return errors.New("host returned an unapplied lifecycle result")
	}
	return nil
}
