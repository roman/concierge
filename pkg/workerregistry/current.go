package workerregistry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/roman/concierge/pkg/taskmeta"
)

var ErrStaleRecord = errors.New("worker registry record does not match current task")

// CurrentTask keeps one validated worker binding under its task metadata lock.
type CurrentTask struct {
	record             Record
	metadata           taskmeta.Metadata
	id                 taskmeta.TaskID
	taskRoot           *os.Root
	workerRegistryRoot *os.Root
	taskPath           string
	workerRegistryPath string
	taskInfo           os.FileInfo
	workerRegistryInfo os.FileInfo
	metaInfo           os.FileInfo
	recordInfo         os.FileInfo
}

// Record returns the current launch details.
func (task *CurrentTask) Record() Record {
	record := task.record
	record.AgentArgs = append([]string(nil), record.AgentArgs...)
	return record
}

// CheckCurrent rejects task or workerregistry replacement after a blocking operation.
func (task *CurrentTask) CheckCurrent() error {
	if task == nil || !sameDirectory(task.taskInfo, task.taskPath) ||
		!sameDirectory(task.workerRegistryInfo, task.workerRegistryPath) {
		return ErrStaleRecord
	}
	metadata, metaInfo, err := taskmeta.ReadTaskWithInfoFromRoot(task.taskRoot, task.id)
	if err != nil || !os.SameFile(metaInfo, task.metaInfo) ||
		!reflect.DeepEqual(metadata, task.metadata) {
		return ErrStaleRecord
	}
	record, recordInfo, err := readRecordWithInfo(task.workerRegistryRoot, task.id.String())
	if err != nil || !os.SameFile(recordInfo, task.recordInfo) ||
		!reflect.DeepEqual(record, task.record) {
		return ErrStaleRecord
	}
	return nil
}

// WithCurrentTask holds the task metadata lock while a caller uses its worker registry record.
func (registry *Registry) WithCurrentTask(
	taskID string, operation func(*CurrentTask) error,
) error {
	return registry.withCurrentTask(taskID, operation, ordinaryTaskAccess)
}

// WithLaunchTask permits a supervisor to recover a reserved worker launch.
func (registry *Registry) WithLaunchTask(
	taskID string, operation func(*CurrentTask) error,
) error {
	return registry.withCurrentTask(taskID, operation, launchTaskAccess)
}

type taskAccess int

const (
	ordinaryTaskAccess taskAccess = iota
	launchTaskAccess
)

func (registry *Registry) withCurrentTask(
	taskID string,
	operation func(*CurrentTask) error,
	access taskAccess,
) (result error) {
	if registry == nil || registry.root == "" || operation == nil {
		return ErrInvalidRecord
	}
	id, err := taskmeta.ParseTaskID(taskID)
	if err != nil {
		return err
	}
	state, err := taskmeta.ParseAbsolutePath(filepath.Join(registry.root, "tasks"))
	if err != nil {
		return err
	}
	registryRoot, err := verifiedRoot(registry.root)
	if err != nil {
		return err
	}
	defer registryRoot.Close()
	taskRoot, taskInfo, err := openSelectedDirectory(registryRoot, "tasks")
	if err != nil {
		return err
	}
	defer taskRoot.Close()
	workerRegistryRoot, workerRegistryInfo, err := openSelectedDirectory(
		registryRoot,
		"workerregistry",
	)
	if err != nil {
		return err
	}
	defer workerRegistryRoot.Close()
	lock, err := taskmeta.AcquireTaskLock(state, id, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Release()) }()
	if !sameDirectory(taskInfo, state.String()) ||
		!sameDirectory(workerRegistryInfo, filepath.Join(registry.root, "workerregistry")) {
		return fmt.Errorf("%w: task or workerregistry directory changed", ErrStaleRecord)
	}
	if pending, err := completionPending(taskRoot, id); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("%w: task completion is pending", ErrStaleRecord)
	}
	record, err := readRecord(workerRegistryRoot, taskID)
	if err != nil {
		return err
	}
	if record.PendingAdoption {
		return fmt.Errorf("%w: task adoption is pending", ErrStaleRecord)
	}
	if record.PendingLaunch != "" && access != launchTaskAccess {
		return fmt.Errorf("%w: worker launch is pending", ErrStaleRecord)
	}
	if record.PendingGeneration != "" {
		record, err = recoverPendingGeneration(taskRoot, workerRegistryRoot, id, record)
		if err != nil {
			return err
		}
	}
	metadata, metaInfo, err := taskmeta.ReadTaskWithInfoFromRoot(taskRoot, id)
	if err != nil {
		return err
	}
	record, recordInfo, err := readRecordWithInfo(workerRegistryRoot, taskID)
	if err != nil {
		return err
	}
	if !matchesTask(record, metadata) {
		return fmt.Errorf("%w: %s", ErrStaleRecord, taskID)
	}
	return operation(&CurrentTask{
		record: record, metadata: metadata, id: id, taskRoot: taskRoot, workerRegistryRoot: workerRegistryRoot,
		taskPath: state.String(), workerRegistryPath: filepath.Join(registry.root, "workerregistry"),
		taskInfo: taskInfo, workerRegistryInfo: workerRegistryInfo, metaInfo: metaInfo, recordInfo: recordInfo,
	})
}

func matchesTask(record Record, metadata taskmeta.Metadata) bool {
	return sameTaskIdentity(record, metadata) &&
		record.SpawnGeneration == metadata.SpawnGeneration()
}

func sameTaskIdentity(record Record, metadata taskmeta.Metadata) bool {
	return record.Project == metadata.Project().String() &&
		record.Worktree == metadata.Worktree().String() &&
		record.Kind == metadata.Harness() &&
		record.HerdrSession == metadata.HerdrSession() &&
		record.PaneID == metadata.HerdrPaneID()
}

func openSelectedDirectory(root *os.Root, name string) (*os.Root, os.FileInfo, error) {
	selected, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !selected.IsDir() {
		return nil, nil, ErrInvalidRecord
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := child.Open(".")
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	openedInfo, statErr := opened.Stat()
	opened.Close()
	if statErr != nil || !os.SameFile(selected, openedInfo) {
		child.Close()
		return nil, nil, ErrInvalidRecord
	}
	return child, openedInfo, nil
}

func sameDirectory(selected os.FileInfo, path string) bool {
	current, err := os.Stat(path)
	return err == nil && os.SameFile(selected, current)
}

// withLockedTask pins the selected tasks directory while adopting a worker.
func (registry *Registry) withLockedTask(
	taskID string,
	operation func(*os.Root, func() error) error,
) (result error) {
	if registry == nil || registry.root == "" || operation == nil {
		return ErrInvalidRecord
	}
	id, err := taskmeta.ParseTaskID(taskID)
	if err != nil {
		return err
	}
	registryRoot, err := verifiedRoot(registry.root)
	if err != nil {
		return err
	}
	defer registryRoot.Close()
	taskRoot, selected, err := openSelectedDirectory(registryRoot, "tasks")
	if err != nil {
		return err
	}
	defer taskRoot.Close()
	path := filepath.Join(registry.root, "tasks")
	state, err := taskmeta.ParseAbsolutePath(path)
	if err != nil {
		return err
	}
	lock, err := taskmeta.AcquireTaskLock(state, id, 2*time.Second)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Release()) }()
	check := func() error {
		if !sameDirectory(selected, path) {
			return fmt.Errorf("%w: tasks directory changed", ErrStaleRecord)
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	return operation(taskRoot, check)
}
