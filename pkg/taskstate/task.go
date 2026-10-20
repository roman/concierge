// Package taskstate defines validated local and remote task records.
package taskstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/tincan/pkg/inbox"
)

var ErrInvalidTask = errors.New("invalid task record")

const formatVersion = 2
const remoteFormatVersion = 3
const maximumTaskBytes = 1 << 20
const maximumTaskIDBytes = 200

// State is the task's queue position. A hold is recorded separately.
type State string

const (
	Queued   State = "queued"
	InFlight State = "in_flight"
	Done     State = "done"
)

// Dependency preserves a typed task relationship and its optional reason.
type Dependency struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

// Hold prevents dispatch while a task remains in its queue position.
type Hold struct {
	Reason string `json:"reason"`
	Kind   string `json:"kind,omitempty"`
	Until  string `json:"until,omitempty"`
}

// Definition holds the backlog fields that must survive the migration.
type Definition struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Body         string       `json:"body,omitempty"`
	Repo         string       `json:"repo"`
	Kind         string       `json:"kind"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
	Hold         *Hold        `json:"hold,omitempty"`
	Priority     *int         `json:"priority,omitempty"`
	Created      string       `json:"created,omitempty"`
	Closed       string       `json:"closed,omitempty"`
}

func (definition Definition) DisplayName() string {
	return definition.Repo + "/" + strings.TrimPrefix(definition.ID, definition.Repo+"-")
}

// RestartPlan freezes the mailbox obligations for one launch reservation.
type RestartPlan struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	Assignments []string `json:"assignments,omitempty"`
	Replies     []string `json:"replies,omitempty"`
}

// Worker preserves the exact launch and endpoint identity for recovery.
type Worker struct {
	RemoteResult            RemoteResult        `json:"remote_result,omitzero"`
	Access                  *workeraccess.Grant `json:"access,omitempty"`
	TransportGeneration     string              `json:"transport_generation,omitempty"`
	LaunchAttemptGeneration string              `json:"launch_attempt_generation,omitempty"`
	Connections             *Connections        `json:"connections,omitempty"`
	Machine                 string              `json:"machine,omitempty"`
	Lease                   WorktreeLease       `json:"lease,omitzero"`
	OwnedWorkspaceLabel     string              `json:"owned_workspace_label,omitempty"`
	PromptProgress          *PromptProgress     `json:"prompt_progress,omitempty"`
	Restarts                []RestartPlan       `json:"restarts,omitempty"`
	Name                    string              `json:"name"`
	Harness                 string              `json:"harness"`
	Session                 string              `json:"session"`
	Workspace               string              `json:"workspace,omitempty"`
	Tab                     string              `json:"tab,omitempty"`
	Pane                    string              `json:"pane"`
	Project                 string              `json:"project"`
	Worktree                string              `json:"worktree"`
	Generation              string              `json:"generation"`
	PendingGeneration       string              `json:"pending_generation,omitempty"`
	PendingLaunch           string              `json:"pending_launch,omitempty"`
	PendingAdoption         bool                `json:"pending_adoption,omitempty"`
	AgentArgs               []string            `json:"agent_args"`
}

// CompletionMode selects the queue state after a worker result.
type CompletionMode string

const (
	Close  CompletionMode = "close"
	Retain CompletionMode = "retain"
)

// CompletionPhase tracks a result across pane retirement.
type CompletionPhase string

const (
	Accepted CompletionPhase = "accepted"
	Retired  CompletionPhase = "retired"
)

// Completion identifies the result accepted for one worker generation.
type Completion struct {
	Generation string          `json:"generation"`
	Mode       CompletionMode  `json:"mode"`
	Phase      CompletionPhase `json:"phase"`
}

// Receipt preserves the latest retired generation after worker records disappear.
type Receipt struct {
	RemoteResult RemoteResult   `json:"remote_result,omitzero"`
	Lease        WorktreeLease  `json:"lease,omitzero"`
	Generation   string         `json:"generation"`
	Mode         CompletionMode `json:"mode"`
}

// AbandonmentKind distinguishes discarded workers from unfinished launches.
type AbandonmentKind string

const DiscardedWorker AbandonmentKind = "discarded_worker"

// LaunchAbandonment preserves resources without asserting successful work.
type LaunchAbandonment struct {
	Kind          AbandonmentKind `json:"kind,omitempty"`
	WorkerRetired bool            `json:"worker_retired,omitempty"`
	Remote        RemoteLease     `json:"remote,omitzero"`
	BaseCommit    string          `json:"base_commit,omitempty"`
	Lease         WorktreeLease   `json:"lease,omitzero"`
	LeaseHolder   string          `json:"lease_holder,omitempty"`
	Project       string          `json:"project,omitempty"`
	Generation    string          `json:"generation"`
	Worktree      string          `json:"worktree"`
	Branch        string          `json:"branch"`
}

type record struct {
	AbandonedLaunches []LaunchAbandonment `json:"abandoned_launches,omitempty"`
	Format            int                 `json:"format"`
	Definition
	State         State         `json:"state"`
	Worker        *Worker       `json:"worker,omitempty"`
	LaunchIntent  *LaunchIntent `json:"launch_intent,omitempty"`
	Completion    *Completion   `json:"completion,omitempty"`
	Receipt       *Receipt      `json:"receipt,omitempty"`
	PriorReceipts []Receipt     `json:"prior_receipts,omitempty"`
}

// Snapshot carries one complete task across the explicit migration boundary.
type Snapshot struct {
	AbandonedLaunches []LaunchAbandonment
	Definition        Definition
	State             State
	Worker            *Worker
	LaunchIntent      *LaunchIntent
	Completion        *Completion
	Receipt           *Receipt
	PriorReceipts     []Receipt
}

// Task keeps a validated snapshot private. Changes create a new snapshot.
type Task struct{ record record }

// NewQueued creates an unassigned backlog task.
func NewQueued(input Definition) (Task, error) {
	selected := record{Format: formatVersion, Definition: cloneDefinition(input), State: Queued}
	if err := validate(selected); err != nil {
		return Task{}, err
	}
	return Task{record: selected}, nil
}

// NewMigrated accepts a complete snapshot only when its combined state is valid.
func NewMigrated(input Snapshot) (Task, error) {
	selected := record{
		AbandonedLaunches: slices.Clone(input.AbandonedLaunches),
		Format:            formatVersion, Definition: cloneDefinition(input.Definition),
		State: input.State, Worker: cloneWorker(input.Worker),
		LaunchIntent: cloneLaunchIntent(input.LaunchIntent),
		Completion:   cloneCompletion(input.Completion),
		Receipt:      cloneReceipt(input.Receipt), PriorReceipts: slices.Clone(input.PriorReceipts),
	}
	if recordUsesRemote(selected) {
		selected.Format = remoteFormatVersion
	}
	if err := validate(selected); err != nil {
		return Task{}, err
	}
	return Task{record: selected}, nil
}

// Decode validates a complete task record and rejects unknown fields.
func Decode(data []byte) (Task, error) {
	if len(data) > maximumTaskBytes {
		return Task{}, fmt.Errorf("%w: record exceeds 1 MiB", ErrInvalidTask)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var selected record
	if err := decoder.Decode(&selected); err != nil {
		return Task{}, fmt.Errorf("%w: %v", ErrInvalidTask, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Task{}, fmt.Errorf("%w: trailing data", ErrInvalidTask)
	}
	if err := validate(selected); err != nil {
		return Task{}, err
	}
	return Task{record: selected}, nil
}

// Encode returns the validated task record for atomic storage.
func (task Task) Encode() ([]byte, error) {
	if err := validate(task.record); err != nil {
		return nil, err
	}
	data, err := json.Marshal(task.record)
	if err != nil {
		return nil, err
	}
	if len(data) > maximumTaskBytes {
		return nil, fmt.Errorf("%w: record exceeds 1 MiB", ErrInvalidTask)
	}
	return data, nil
}

// ID returns the stable task ID.
func (task Task) ID() string { return task.record.ID }

// State returns the queue state.
func (task Task) State() State { return task.record.State }

// Definition returns a detached copy of the backlog fields.
func (task Task) Definition() Definition { return cloneDefinition(task.record.Definition) }

// Snapshot returns a detached copy for migration checks and task transitions.
func (task Task) Snapshot() Snapshot {
	return Snapshot{
		AbandonedLaunches: slices.Clone(task.record.AbandonedLaunches),
		Definition:        cloneDefinition(task.record.Definition), State: task.record.State,
		Worker:        cloneWorker(task.record.Worker),
		LaunchIntent:  cloneLaunchIntent(task.record.LaunchIntent),
		Completion:    cloneCompletion(task.record.Completion),
		Receipt:       cloneReceipt(task.record.Receipt),
		PriorReceipts: slices.Clone(task.record.PriorReceipts),
	}
}

// HasReceipt reports whether a completed worker generation has durable proof.
func (task Task) HasReceipt(generation string, mode CompletionMode) bool {
	if task.record.Receipt != nil && task.record.Receipt.Generation == generation &&
		task.record.Receipt.Mode == mode {
		return true
	}
	for _, receipt := range task.record.PriorReceipts {
		if receipt.Generation == generation && receipt.Mode == mode {
			return true
		}
	}
	return false
}

func cloneDefinition(input Definition) Definition {
	selected := input
	selected.Dependencies = slices.Clone(input.Dependencies)
	if input.Hold != nil {
		hold := *input.Hold
		selected.Hold = &hold
	}
	if input.Priority != nil {
		priority := *input.Priority
		selected.Priority = &priority
	}
	return selected
}

func cloneWorker(input *Worker) *Worker {
	if input == nil {
		return nil
	}
	selected := *input
	selected.PromptProgress = clonePromptProgress(input.PromptProgress)
	if input.Access != nil {
		access := *input.Access
		access.Operations = slices.Clone(input.Access.Operations)
		selected.Access = &access
	}
	if input.Connections != nil {
		connections := *input.Connections
		selected.Connections = &connections
	}
	selected.AgentArgs = slices.Clone(input.AgentArgs)
	selected.Restarts = slices.Clone(input.Restarts)
	for index := range selected.Restarts {
		selected.Restarts[index].Assignments = slices.Clone(input.Restarts[index].Assignments)
		selected.Restarts[index].Replies = slices.Clone(input.Restarts[index].Replies)
	}
	return &selected
}

func cloneCompletion(input *Completion) *Completion {
	if input == nil {
		return nil
	}
	selected := *input
	return &selected
}

func cloneReceipt(input *Receipt) *Receipt {
	if input == nil {
		return nil
	}
	selected := *input
	return &selected
}

func validate(selected record) error {
	if !validLaunchAbandonments(selected) {
		return ErrInvalidTask
	}

	if (selected.Format != formatVersion && selected.Format != remoteFormatVersion) ||
		!validDefinition(selected.Definition) {
		return ErrInvalidTask
	}
	if selected.Format == formatVersion && recordUsesRemote(selected) {
		return ErrInvalidTask
	}
	if selected.State != Queued && selected.State != InFlight && selected.State != Done {
		return ErrInvalidTask
	}
	if !validLaunchIntent(selected) {
		return ErrInvalidTask
	}
	if selected.Worker != nil && !validWorker(*selected.Worker) {
		return ErrInvalidTask
	}
	if selected.Worker != nil && selected.Worker.Access != nil &&
		selected.Worker.Access.Task != selected.Definition.ID {
		return ErrInvalidTask
	}
	if selected.Worker != nil && selected.Worker.Workspace == "" &&
		!selected.Worker.PendingAdoption &&
		(selected.Completion == nil || selected.Completion.Phase != Retired) {
		return ErrInvalidTask
	}
	if selected.State == InFlight && selected.Worker == nil &&
		(selected.Completion == nil || selected.Completion.Phase != Retired) {
		return ErrInvalidTask
	}
	if selected.Worker != nil && selected.Worker.PendingAdoption && selected.State != Queued {
		return ErrInvalidTask
	}
	if selected.Completion != nil {
		if selected.Worker != nil && selected.Worker.RemoteResult != (RemoteResult{}) &&
			selected.Worker.RemoteResult.Phase != RemoteResultReady {
			return ErrInvalidTask
		}
		if !validCompletion(*selected.Completion) {
			return ErrInvalidTask
		}
		if selected.Worker == nil && selected.Completion.Phase != Retired {
			return ErrInvalidTask
		}
		if selected.Worker != nil &&
			(selected.Completion.Generation != selected.Worker.Generation ||
				selected.Worker.PendingAdoption || selected.Worker.PendingGeneration != "" ||
				selected.Worker.PendingLaunch != "") {
			return ErrInvalidTask
		}
		if (selected.State == Done && selected.Completion.Mode != Close) ||
			(selected.State == Queued && selected.Completion.Mode != Retain) {
			return ErrInvalidTask
		}
	}
	if selected.Receipt != nil {
		if !validRemoteResult(selected.Receipt.RemoteResult, selected.Receipt.Lease) {
			return ErrInvalidTask
		}
		if selected.Receipt.RemoteResult != (RemoteResult{}) &&
			selected.Receipt.RemoteResult.Phase != RemoteResultReady {
			return ErrInvalidTask
		}
		if !validWorktreeLease(selected.Receipt.Lease) ||
			taskmeta.ValidateSpawnGeneration(selected.Receipt.Generation) != nil ||
			(selected.Receipt.Mode != Close && selected.Receipt.Mode != Retain) {
			return ErrInvalidTask
		}
		if selected.Receipt.Mode == Close &&
			(selected.State != Done ||
				(selected.Worker != nil && selected.Worker.Generation != selected.Receipt.Generation) ||
				(selected.Completion != nil && selected.Completion.Generation != selected.Receipt.Generation)) {
			return ErrInvalidTask
		}
		if selected.Worker != nil &&
			(selected.Receipt.Generation == selected.Worker.Generation ||
				selected.Receipt.Generation == selected.Worker.PendingGeneration ||
				selected.Receipt.Generation == selected.Worker.PendingLaunch) {
			return ErrInvalidTask
		}
		if selected.Worker == nil && selected.Completion == nil &&
			((selected.State == Done && selected.Receipt.Mode != Close) ||
				(selected.State == Queued && selected.Receipt.Mode != Retain)) {
			return ErrInvalidTask
		}
		if selected.Completion != nil &&
			selected.Completion.Generation == selected.Receipt.Generation &&
			selected.Completion.Mode != selected.Receipt.Mode {
			return ErrInvalidTask
		}
	}
	if len(selected.PriorReceipts) > 0 && selected.Receipt == nil {
		return ErrInvalidTask
	}
	seenReceipts := make(map[string]bool, len(selected.PriorReceipts))
	for _, receipt := range selected.PriorReceipts {
		if !validRemoteResult(receipt.RemoteResult, receipt.Lease) {
			return ErrInvalidTask
		}
		if receipt.RemoteResult != (RemoteResult{}) &&
			receipt.RemoteResult.Phase != RemoteResultReady {
			return ErrInvalidTask
		}
		if !validWorktreeLease(receipt.Lease) ||
			taskmeta.ValidateSpawnGeneration(receipt.Generation) != nil ||
			receipt.Mode != Retain ||
			seenReceipts[receipt.Generation] {
			return ErrInvalidTask
		}
		seenReceipts[receipt.Generation] = true
		if selected.Receipt != nil && receipt.Generation == selected.Receipt.Generation {
			return ErrInvalidTask
		}
		if selected.Worker != nil &&
			(receipt.Generation == selected.Worker.Generation ||
				receipt.Generation == selected.Worker.PendingGeneration ||
				receipt.Generation == selected.Worker.PendingLaunch) {
			return ErrInvalidTask
		}
		if selected.Completion != nil && receipt.Generation == selected.Completion.Generation {
			return ErrInvalidTask
		}
	}
	if selected.State != Done && selected.Closed != "" {
		return ErrInvalidTask
	}
	if selected.State == Done && selected.Worker != nil && selected.Completion == nil {
		return ErrInvalidTask
	}
	return nil
}

func validDefinition(input Definition) bool {
	if _, err := taskmeta.ParseTaskID(input.ID); err != nil ||
		len(input.ID) > maximumTaskIDBytes ||
		input.ID[0] == '.' || input.ID[0] == '_' || input.ID[0] == '-' {
		return false
	}
	if !singleLine(input.Title) || !singleLine(input.Repo) || !singleLine(input.Kind) ||
		!utf8.ValidString(input.Body) || strings.ContainsRune(input.Body, 0) {
		return false
	}
	if input.Priority != nil && (*input.Priority < 0 || *input.Priority > 4) {
		return false
	}
	if !validDate(input.Created) || !validDate(input.Closed) {
		return false
	}
	if input.Hold != nil && (!singleLine(input.Hold.Reason) ||
		!validHoldKind(input.Hold.Kind) || !validDate(input.Hold.Until)) {
		return false
	}
	seen := make(map[string]bool, len(input.Dependencies))
	for _, dependency := range input.Dependencies {
		if _, err := taskmeta.ParseTaskID(dependency.ID); err != nil ||
			dependency.ID == input.ID || seen[dependency.ID] ||
			!validDependencyType(dependency.Type) || !optionalLine(dependency.Reason) {
			return false
		}
		seen[dependency.ID] = true
	}
	return true
}

func validWorker(worker Worker) bool {
	if !validRemoteResult(worker.RemoteResult, worker.Lease) {
		return false
	}
	if worker.RemoteResult != (RemoteResult{}) {
		if worker.PendingAdoption || worker.PendingLaunch != "" || worker.PendingGeneration != "" {
			return false
		}
		if worker.RemoteResult.Lease != (WorktreeLease{}) &&
			worker.RemoteResult.Lease.State != LeaseHeld {
			return false
		}
	}
	if worker.LaunchAttemptGeneration != "" {
		if worker.Machine == "" || worker.Lease.Remote == (RemoteLease{}) {
			return false
		}
		if worker.LaunchAttemptGeneration != worker.Generation ||
			worker.PendingLaunch != worker.Generation {
			return false
		}
		if len(worker.Restarts) == 0 ||
			worker.Restarts[len(worker.Restarts)-1].To != worker.Generation {
			return false
		}
	}
	if worker.TransportGeneration != "" &&
		(worker.Machine == "" || worker.Access == nil || worker.TransportGeneration != worker.Generation) {
		return false
	}
	if remote := worker.Lease.Remote; remote != (RemoteLease{}) &&
		remote.Machine != worker.Machine {
		return false
	}
	if access := worker.Access; access != nil &&
		(worker.Machine == "" || access.Generation != worker.Generation || access.Validate() != nil) {
		return false
	}
	if connections := worker.Connections; connections != nil &&
		(worker.Machine == "" || !validConnection(connections.Herdr) || !validConnection(connections.Tunnel)) {
		return false
	}
	if !validPromptProgress(worker.PromptProgress) {
		return false
	}
	seen := map[string]bool{}
	for index, plan := range worker.Restarts {
		if index == 0 {
			seen[plan.From] = true
		}
		if taskmeta.ValidateSpawnGeneration(plan.From) != nil ||
			taskmeta.ValidateSpawnGeneration(plan.To) != nil ||
			plan.From == plan.To || seen[plan.To] {
			return false
		}
		seen[plan.To] = true
		if index > 0 && plan.From != worker.Restarts[index-1].To {
			return false
		}
		for _, ids := range [][]string{plan.Assignments, plan.Replies} {
			unique := map[string]bool{}
			for _, id := range ids {
				if _, err := inbox.ParseMessageID(id); err != nil || unique[id] {
					return false
				}
				unique[id] = true
			}
		}
	}
	if len(worker.Restarts) > 0 {
		last := worker.Restarts[len(worker.Restarts)-1]
		if worker.Generation != last.To &&
			(worker.Generation != last.From || worker.PendingLaunch != last.To) {
			return false
		}
	}

	if worker.OwnedWorkspaceLabel != "" &&
		(!validWorkspaceLabel(worker.OwnedWorkspaceLabel) || worker.Workspace == "" || worker.PendingAdoption) {
		return false
	}
	if !validWorktreeLease(worker.Lease) ||
		(worker.Lease != (WorktreeLease{}) && (worker.Lease.State != LeaseHeld || worker.Lease.Path != worker.Worktree || worker.Lease.Project != worker.Project || worker.PendingAdoption)) {
		return false
	}
	if !validAtom(worker.Name, false) || len(worker.AgentArgs) == 0 ||
		!validAtom(worker.Harness, false) ||
		taskmeta.ValidateSpawnGeneration(worker.Generation) != nil {
		return false
	}
	for _, argument := range worker.AgentArgs {
		if !singleLine(argument) {
			return false
		}
	}
	if _, err := herdr.ParseEndpoint(worker.Machine, worker.Session, worker.Pane); err != nil {
		return false
	}
	for _, path := range []string{worker.Project, worker.Worktree} {
		if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
			return false
		}
	}
	if worker.PendingGeneration != "" &&
		(taskmeta.ValidateSpawnGeneration(worker.PendingGeneration) != nil ||
			worker.PendingGeneration == worker.Generation) {
		return false
	}
	if worker.PendingLaunch != "" &&
		taskmeta.ValidateSpawnGeneration(worker.PendingLaunch) != nil {
		return false
	}
	if worker.PendingAdoption && (worker.PendingGeneration != "" || worker.PendingLaunch != "") {
		return false
	}
	if worker.PendingGeneration != "" && worker.PendingLaunch != "" &&
		worker.PendingGeneration != worker.PendingLaunch {
		return false
	}
	return (worker.Workspace == "" && worker.Tab == "") ||
		(validAtom(worker.Workspace, false) && validAtom(worker.Tab, true))
}

func validCompletion(completion Completion) bool {
	return taskmeta.ValidateSpawnGeneration(completion.Generation) == nil &&
		(completion.Mode == Close || completion.Mode == Retain) &&
		(completion.Phase == Accepted || completion.Phase == Retired)
}

func validAtom(value string, allowColon bool) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if strings.ContainsRune(
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._@%+-",
			character,
		) ||
			(allowColon && character == ':') {
			continue
		}
		return false
	}
	return true
}

func validWorkspaceLabel(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if !validAtom(segment, false) {
			return false
		}
	}
	return true
}

func singleLine(value string) bool {
	return value != "" && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func optionalLine(value string) bool { return value == "" || singleLine(value) }

func validDate(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validHoldKind(value string) bool {
	return value == "" ||
		slices.Contains([]string{"primary", "external", "load", "parked", "future"}, value)
}

func validDependencyType(value string) bool {
	return slices.Contains([]string{"blocked-by", "parent", "discovered-from"}, value)
}
