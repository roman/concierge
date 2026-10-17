package remoteworker

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/roman/concierge/pkg/workeraccess"
)

type launchDirectoryOwner struct {
	Format  int               `json:"format"`
	Request LaunchSiteRequest `json:"request"`
}

func prepareLaunchDirectory(request LaunchSiteRequest, tools Tools) error {
	root, err := workeraccess.OpenPrivateDirectory(request.Worktree.Location.Base)
	if err != nil {
		return ErrLaunchDirectoryOwnership
	}
	defer root.Close()
	if err := root.Mkdir("tasks", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	before, err := root.Lstat("tasks")
	if err != nil || !privateTunnelFile(before, os.ModeDir) {
		return ErrLaunchDirectoryOwnership
	}
	tasks, err := root.OpenRoot("tasks")
	if err != nil {
		return ErrLaunchDirectoryOwnership
	}
	defer tasks.Close()
	if after, err := tasks.Stat("."); err != nil || !os.SameFile(before, after) {
		return ErrLaunchDirectoryOwnership
	}
	if err := syncSocketDirectory(root); err != nil {
		return err
	}
	name := filepath.Base(tools.TaskDirectory)
	created := true
	if err := tasks.Mkdir(name, 0700); errors.Is(err, os.ErrExist) {
		created = false
	} else if err != nil {
		return err
	}
	before, err = tasks.Lstat(name)
	if err != nil || !privateTunnelFile(before, os.ModeDir) {
		return ErrLaunchDirectoryOwnership
	}
	directory, err := tasks.OpenRoot(name)
	if err != nil {
		return ErrLaunchDirectoryOwnership
	}
	defer directory.Close()
	if after, err := directory.Stat("."); err != nil || !os.SameFile(before, after) {
		return ErrLaunchDirectoryOwnership
	}
	expected, err := json.Marshal(launchDirectoryOwner{Format: 1, Request: request})
	if err != nil {
		return err
	}
	if created {
		if err := writeLaunchDirectoryOwner(directory, expected); err != nil {
			return err
		}
	}
	if err := confirmLaunchDirectoryOwner(directory, expected); err != nil {
		return err
	}
	return syncSocketDirectory(tasks)
}

func confirmLaunchDirectoryOwner(root *os.Root, expected []byte) error {
	before, err := root.Lstat("launch-owner.json")
	if err != nil || !privateProjectFile(before) {
		return ErrLaunchDirectoryOwnership
	}
	file, err := root.OpenFile("launch-owner.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrLaunchDirectoryOwnership
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || !os.SameFile(before, opened) {
		return ErrLaunchDirectoryOwnership
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || !bytes.Equal(data, expected) {
		return ErrLaunchDirectoryOwnership
	}
	if after, err := root.Lstat("launch-owner.json"); err != nil || !os.SameFile(before, after) {
		return ErrLaunchDirectoryOwnership
	}
	return syncSocketDirectory(root)
}

func writeLaunchDirectoryOwner(root *os.Root, data []byte) error {
	name := ".launch-owner-" + rand.Text()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
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
	if err := root.Link(name, "launch-owner.json"); err != nil {
		return err
	}
	return syncSocketDirectory(root)
}
