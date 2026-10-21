package conciergecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

const remoteWorkerHelp = "use the task and inherited generation from this remote worker launch; ask the primary to recover revoked access"

func remoteWorkerMode() bool {
	for _, key := range []string{"CONCIERGE_WORKER_SOCKET", "CONCIERGE_WORKER_SECRET_FILE", "CONCIERGE_WORKER_TASK"} {
		if _, present := os.LookupEnv(key); present {
			return true
		}
	}
	return false
}

func (command *cli) runRemoteWorker(
	ctx context.Context,
	selected *kong.Context,
	stdin io.Reader,
	output io.Writer,
) int {
	for _, trace := range selected.Path {
		if trace.Flag == nil {
			continue
		}
		switch trace.Flag.Name {
		case "root", "message-root", "recipient":
			return fail(
				output,
				2,
				errors.New("remote worker roots and recipients are selected by the primary"),
				remoteWorkerHelp,
			)
		}
	}
	task, generation := os.Getenv("CONCIERGE_WORKER_TASK"), os.Getenv(workerLaunchGeneration)
	request, bodyFile, err := command.remoteRequest(selected.Command(), task, generation)
	if err != nil {
		return fail(output, 2, err, remoteWorkerHelp)
	}
	if _, err := taskmeta.ParseTaskID(task); err != nil ||
		taskmeta.ValidateSpawnGeneration(generation) != nil ||
		request.Task != task ||
		request.Generation != generation {
		return fail(
			output,
			1,
			errors.New(
				"remote worker task or generation differs from the inherited launch context",
			),
			remoteWorkerHelp,
		)
	}
	socket, secretFile := os.Getenv(
		"CONCIERGE_WORKER_SOCKET",
	), os.Getenv(
		"CONCIERGE_WORKER_SECRET_FILE",
	)
	for _, path := range []string{socket, secretFile} {
		if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
			return fail(
				output,
				1,
				errors.New("remote worker launch has an invalid socket or secret-file path"),
				remoteWorkerHelp,
			)
		}
	}
	if filepath.Base(socket) == "host.sock" {
		return fail(
			output,
			1,
			errors.New("remote workers require the separate worker socket"),
			remoteWorkerHelp,
		)
	}
	if bodyFile != "" {
		request.Body, err = readRemoteBody(bodyFile, stdin)
		if err != nil {
			return fail(
				output,
				1,
				err,
				"provide a readable result file of at most 1 MiB or use --body-file -",
			)
		}
	}
	var response workerservice.Response
	retained := ""
	if request.Operation == workeraccess.SendReply {
		request.Attachments, err = readReplyAttachments(command.Task.Reply.Send.Attach)
		if err != nil {
			return fail(output, 1, err, replySendUsage)
		}
		response, retained, err = sendRemoteReply(ctx, socket, secretFile, request)
	} else {
		secret, secretError := workeraccess.ReadSecretFile(secretFile)
		if secretError != nil {
			return fail(output, 1, secretError, remoteWorkerHelp)
		}
		binding, bindingError := remoteReplyContext(secretFile, task, generation)
		if bindingError != nil {
			return fail(output, 1, bindingError, remoteWorkerHelp)
		}
		if err := binding.Match(task, generation, secret); err != nil {
			return fail(output, 1, err, remoteWorkerHelp)
		}
		request.Secret = string(secret)
		response, err = workerservice.Call(ctx, socket, request)
	}
	if response.Reply != nil {
		if _, writeError := fmt.Fprintf(output, "message_id: %s\npublication: %s\narchive: %s\nassignment: %s\n",
			quote(response.Reply.MessageID), response.Reply.Publication, response.Reply.Archive,
			response.Reply.Assignment); writeError != nil {
			return 1
		}
	}
	if err != nil {
		if retained != "" {
			if _, writeError := fmt.Fprintf(output, "retained_reply: %s\n", quote(retained)); writeError != nil {
				return 1
			}
		}
		return fail(
			output,
			1,
			err,
			remoteWorkerHelp+"; retain results and retry an unconfirmed reply with identical arguments and body",
		)
	}
	return command.writeRemoteResponse(output, selected.Command(), response)
}

func (command *cli) remoteRequest(
	selected, task, generation string,
) (workerservice.Request, string, error) {
	request := workerservice.Request{Task: task, Generation: generation}
	switch selected {
	case "task receive <task-id> <message-id>":
		input := command.Task.Receive
		request.Task, request.Generation, request.MessageID = input.TaskID, input.Generation, input.MessageID
		request.Operation = workeraccess.Receive
	case "task ack <task-id> <message-id>":
		input := command.Task.Ack
		request.Task, request.Generation, request.MessageID = input.TaskID, input.Generation, input.MessageID
		request.Operation = workeraccess.AckAssignment
	case "task reply send <task-id>":
		input := command.Task.Reply.Send
		request.Task, request.Generation = input.TaskID, input.Generation
		request.Operation, request.Key, request.MessageID = workeraccess.SendReply, input.Key, input.AssignmentID
		return request, input.BodyFile, nil
	case "project show <repo>":
		request.Operation = workeraccess.ReadSettings
	case "task inbox list <task-id>":
		request.Task, request.Operation = command.Task.Inbox.List.TaskID, workeraccess.ListPending
	case "task bootstrap read <task-id> <message-id>", "task bootstrap ack <task-id> <message-id>":
		input, operation := command.Task.Bootstrap.Read, workeraccess.ReadBootstrap
		if selected == "task bootstrap ack <task-id> <message-id>" {
			input, operation = command.Task.Bootstrap.Ack, workeraccess.AckBootstrap
		}
		request.Task, request.Generation, request.MessageID = input.TaskID, input.Generation, input.MessageID
		request.Operation = operation
	case "task settings <task-id>":
		request.Task, request.Generation = command.Task.Settings.TaskID, command.Task.Settings.Generation
		request.Operation = workeraccess.ReadSettings
	default:
		return workerservice.Request{}, "", errors.New(
			"remote workers may use only worker operations",
		)
	}
	return request, "", nil
}

func (command *cli) writeRemoteResponse(
	output io.Writer,
	selected string,
	response workerservice.Response,
) int {
	switch selected {
	case "task inbox list <task-id>":
		return writeRemotePending(output, response.Pending)
	case "task bootstrap read <task-id> <message-id>", "task bootstrap ack <task-id> <message-id>":
		return writeRemoteBootstrap(output, selected, *response.Bootstrap)
	}
	if response.Assignment != nil {
		if _, err := fmt.Fprintf(output, "status: %s\n", response.Assignment.Status); err != nil {
			return 1
		}
		if response.Assignment.Status == taskops.AssignmentCurrent {
			if _, err := fmt.Fprintf(output, "body: %s\n", quote(response.Assignment.Body)); err != nil {
				return 1
			}
		}
	}
	if response.Settings != nil {
		settings := response.Settings
		if selected == "project show <repo>" && command.Project.Show.Repo != settings.Repo {
			return fail(
				output,
				1,
				errors.New("remote worker cannot inspect another repository"),
				remoteWorkerHelp,
			)
		}
		if _, err := fmt.Fprintf(output, "repo: %s\nmerge: %s\ndelivery: %s\n", quote(settings.Repo), settings.Merge, settings.Delivery); err != nil {
			return 1
		}
	}
	return 0
}

func readRemoteBody(path string, stdin io.Reader) (string, error) {
	reader := stdin
	if path != "-" {
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return "", err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("remote reply body must be a regular file")
		}
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", errors.New("remote reply body exceeds 1 MiB")
	}
	return string(data), nil
}
