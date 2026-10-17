package machineprep

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/sshcommand"
)

type SourceBundle struct {
	Commit    string
	file      *os.File
	directory string
}

func (bundle *SourceBundle) Read(data []byte) (int, error) { return bundle.file.Read(data) }

func (bundle *SourceBundle) Close() error {
	if bundle.file == nil {
		return nil
	}
	err := bundle.file.Close()
	bundle.file = nil
	return errors.Join(err, os.RemoveAll(bundle.directory))
}

// BundleSource captures committed code from the declared Concierge repository.
// Only Git objects enter the bundle; local configuration and credentials stay local.
func BundleSource(ctx context.Context, source, origin string) (*SourceBundle, error) {
	if !filepath.IsAbs(source) || filepath.Clean(source) != source || !validRepositoryURL(origin) {
		return nil, errors.New(
			"client source requires an absolute clean repository path and declared origin",
		)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	run := func(arguments ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", source}, arguments...)...)
		command.Env = sshcommand.ConnectionEnvironment(os.Environ())
		command.Stderr = io.Discard
		command.WaitDelay = time.Second
		output, err := command.Output()
		if err != nil {
			return "", errors.New("cannot read committed Concierge client source")
		}
		return strings.TrimSpace(string(output)), nil
	}
	configuredOrigin, err := run("config", "--get", "remote.origin.url")
	if err != nil || configuredOrigin != origin {
		return nil, errors.New(
			"client source origin differs from the declared concierge repository",
		)
	}
	status, err := run("status", "--porcelain", "--untracked-files=all")
	if err != nil || status != "" {
		return nil, errors.New(
			"commit client source changes and remove untracked files before preparation",
		)
	}
	commit, err := run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "concierge-client-bundle-")
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	filePath := filepath.Join(directory, "source.bundle")
	if _, err = run("bundle", "create", filePath, "HEAD"); err != nil {
		return nil, err
	}
	heads, err := run("bundle", "list-heads", filePath, "HEAD")
	if err != nil || heads != commit+" HEAD" {
		return nil, errors.New(
			"client source HEAD changed during bundle creation; retry from the intended commit",
		)
	}
	if err = os.Chmod(filePath, 0600); err != nil {
		return nil, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	complete = true
	return &SourceBundle{Commit: commit, file: file, directory: directory}, nil
}
