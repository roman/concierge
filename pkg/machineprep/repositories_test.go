package machineprep

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

// Execute only the quoted remote command in a temporary local fixture. No SSH
// process or network connection participates in these preparation tests.
type localPreparation struct {
	origin      string
	environment []string
}

func (runner localPreparation) Run(
	ctx context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, "sh", "-c", arguments[len(arguments)-1])
	command.Stdin = input
	command.Env = append(
		sshcommand.ConnectionEnvironment(os.Environ()),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ALLOW_PROTOCOL=file",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=url.file://"+runner.origin+"/.insteadOf",
		"GIT_CONFIG_VALUE_0=ssh://git.test/",
	)
	command.Env = append(command.Env, runner.environment...)
	output, err := command.Output()
	if failure, ok := err.(*exec.ExitError); ok {
		return output, failure.Stderr, err
	}
	return output, nil, err
}

func TestRepositoryPreparationClonesAndFetchesWithoutMovingTaskBranches(t *testing.T) {
	origin := t.TempDir()
	source := filepath.Join(origin, "concierge.git")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, source, "init", "-b", "main")
	commitFixture(t, source, "first")
	configuration := Configuration{
		Target:       "baker",
		Base:         filepath.Join(t.TempDir(), "remote owner's base"),
		Agent:        "codex",
		Repositories: []Repository{{Name: "concierge", URL: "ssh://git.test/concierge.git"}},
	}
	preparer := Preparer{Runner: localPreparation{origin: origin}}
	if err := preparer.PrepareRepositories(context.Background(), testMachine(), configuration); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(configuration.Base, "repositories", "concierge.git")
	first := gitCommand(t, repository, "rev-parse", "refs/remotes/origin/main")
	gitCommand(t, repository, "branch", "task-branch", first)
	commitFixture(t, source, "second")
	second := gitCommand(t, source, "rev-parse", "HEAD")
	if err := preparer.PrepareRepositories(context.Background(), testMachine(), configuration); err != nil {
		t.Fatal(err)
	}
	if got := gitCommand(t, repository, "rev-parse", "refs/remotes/origin/main"); got != second {
		t.Fatalf("fetch got %s, want %s", got, second)
	}
	if got := gitCommand(t, repository, "rev-parse", "task-branch"); got != first {
		t.Fatal("preparation moved a worker branch")
	}
	info, err := os.Stat(configuration.Base)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("base is not private: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(configuration.Base, "tasks")); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryPreparationPreservesForeignPaths(t *testing.T) {
	for _, kind := range []string{"foreign contents", "foreign owner", "symbolic base", "changed origin", "symbolic repository"} {
		t.Run(kind, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "base")
			if err := os.Mkdir(base, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(base, "machine-owner")
			if kind == "foreign contents" {
				if err := os.WriteFile(filepath.Join(base, "keep"), []byte("operator data"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "foreign owner" {
				if err := os.WriteFile(marker, []byte("another-machine\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symbolic base" {
				link := base + "-link"
				if err := os.Symlink(base, link); err != nil {
					t.Fatal(err)
				}
				base = link
			}
			if kind == "changed origin" || kind == "symbolic repository" {
				if err := os.WriteFile(marker, []byte("machine-id\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(base, "repositories"), 0700); err != nil {
					t.Fatal(err)
				}
				repository := filepath.Join(base, "repositories", "concierge.git")
				if kind == "symbolic repository" {
					if err := os.Symlink(t.TempDir(), repository); err != nil {
						t.Fatal(err)
					}
				} else {
					gitCommand(t, base, "init", "--bare", repository)
					gitCommand(t, repository, "remote", "add", "origin", "ssh://other.test/source")
				}
			}
			configuration := Configuration{
				Target: "baker",
				Base:   base,
				Agent:  "codex",
				Repositories: []Repository{
					{Name: "concierge", URL: "ssh://git.test/concierge.git"},
				},
			}
			err := (Preparer{Runner: localPreparation{origin: t.TempDir()}}).PrepareRepositories(
				context.Background(),
				testMachine(),
				configuration,
			)
			if err == nil {
				t.Fatal("claimed foreign preparation paths")
			}
			if _, err := os.Lstat(base); err != nil {
				t.Fatal("removed foreign base")
			}
			if kind == "foreign contents" {
				data, err := os.ReadFile(filepath.Join(base, "keep"))
				if err != nil || string(data) != "operator data" {
					t.Fatal("changed operator data")
				}
			}
		})
	}
}

func gitCommand(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(
		sshcommand.ConnectionEnvironment(os.Environ()),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", arguments, output, err)
	}
	return strings.TrimSpace(string(output))
}

func commitFixture(t *testing.T, directory, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, "README"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, directory, "add", "README")
	gitCommand(
		t,
		directory,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.invalid",
		"commit",
		"-m",
		body,
	)
}
