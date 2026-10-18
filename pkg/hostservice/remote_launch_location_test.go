package hostservice

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
)

type launchLocationSSH struct{ reads int }

func (runner *launchLocationSSH) Run(
	_ context.Context,
	_ []string,
	_ io.Reader,
) ([]byte, []byte, error) {
	runner.reads++
	return []byte(strings.Repeat("a", 40)), nil, nil
}

type launchLocationCatalog struct{ profile herdr.SavedMachine }

func (catalog launchLocationCatalog) Resolve(context.Context, string) (herdr.SavedMachine, error) {
	return catalog.profile, nil
}

func TestRemoteLaunchLocationPinsPreparedClientAndDeclaredRepository(t *testing.T) {
	server := workerHost(t, 3)
	profile, _ := (preparationCatalog{}).Resolve(context.Background(), "baker")
	server.preparationMachines = launchLocationCatalog{profile}
	runner := &launchLocationSSH{}
	server.preparationSSH = runner
	configuration := `{"saved-id":{"target":"baker","base":"/remote/base","agent":"codex","authentication":"agent-forwarding","repositories":[{"name":"concierge","url":"ssh://git.test/concierge.git"},{"name":"project","url":"ssh://git.test/project.git"}]}}`
	if err := os.MkdirAll(filepath.Join(server.config.Root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server.config.Root, "config", "machines.json"), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	server.config.GitExecutable = git
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"remote", "add", "origin", "ssh://git.test/project.git"}} {
		if output, err := exec.Command(git, append([]string{"-C", project}, arguments...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	selected, location, err := server.resolveRemoteLaunchLocation(
		context.Background(),
		"saved-id",
		project,
		"project",
		nil,
	)
	if err != nil || selected != profile || location.Machine != profile.ID ||
		location.Authentication != sshcommand.ForwardedAgent ||
		location.Target != profile.Target ||
		location.PrimaryRoot != server.config.Root ||
		location.PrimaryProject != project ||
		location.Repository != "project" ||
		location.ClientCommit != strings.Repeat("a", 40) ||
		location.Backend != "" {
		t.Fatalf("location=%+v profile=%+v err=%v", location, selected, err)
	}
	if err := location.Validate(); err != nil {
		t.Fatal(err)
	}
	intent := &taskstate.LaunchIntent{
		Machine: profile.ID,
		Session: profile.Session,
		Project: location.ProjectPath(),
		Remote:  location,
	}
	intent.Remote.ClientCommit = strings.Repeat("b", 40)
	_, recovered, err := server.resolveRemoteLaunchLocation(
		context.Background(),
		"saved-id",
		project,
		"project",
		intent,
	)
	if err != nil || recovered != intent.Remote || runner.reads != 1 {
		t.Fatalf("recovery changed client: %+v reads=%d err=%v", recovered, runner.reads, err)
	}
	for _, kind := range []string{"undeclared repository", "wrong origin", "changed target", "changed session", "changed base", "different primary", "local intent", "label", "changed authentication"} {
		t.Run(kind, func(t *testing.T) {
			candidate := *intent
			repository := "project"
			selector := "saved-id"
			switch kind {
			case "changed authentication":
				candidate.Remote.Authentication = sshcommand.RemoteCredentials
			case "undeclared repository":
				repository = "missing"
			case "wrong origin":
				repository = "concierge"
			case "changed target":
				candidate.Remote.Target = "other"
			case "changed session":
				candidate.Session = "other"
			case "changed base":
				candidate.Remote.Base = "/different"
			case "different primary":
				candidate.Remote.PrimaryRoot = "/different"
			case "local intent":
				candidate.Machine = ""
			case "label":
				selector = "baker"
			}
			_, _, err := server.resolveRemoteLaunchLocation(
				context.Background(),
				selector,
				project,
				repository,
				&candidate,
			)
			if err == nil || runner.reads != 1 {
				t.Fatalf(
					"accepted changed location or probed remote: reads=%d err=%v",
					runner.reads,
					err,
				)
			}
		})
	}
}
