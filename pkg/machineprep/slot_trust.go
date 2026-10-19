package machineprep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

// InspectSlotTrust reads documented native project entries without starting an
// agent. Nix parses TOML remotely so other configuration values never leave it.
func (preparer Preparer) InspectSlotTrust(
	ctx context.Context,
	machine herdr.SavedMachine,
	configuration Configuration,
) ([]Check, error) {
	if preparer.Runner == nil || !machine.Enabled || configuration.Target != machine.Target {
		return nil, ErrPreparationUnconfirmed
	}
	if err := configuration.validateResolved(); err != nil {
		return nil, err
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		machine.Target,
		configuration.Authentication,
		"env",
		"CONCIERGE_SLOT_BASE="+configuration.Base,
		"nix",
		"eval",
		"--extra-experimental-features",
		"nix-command flakes",
		"--impure",
		"--json",
		"--expr",
		slotTrustExpression,
	)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, _, err := preparer.Runner.Run(ctx, arguments, nil)
	if err != nil || ctx.Err() != nil || len(output) > 64<<10 {
		return nil, fmt.Errorf(
			"Codex slot trust under %q is unconfirmed; inspect native config.toml without changing trust",
			configuration.Base,
		)
	}
	var slots []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	}
	if json.Unmarshal(output, &slots) != nil || slots == nil || len(slots) > 256 {
		return nil, ErrPreparationUnconfirmed
	}
	checks := make([]Check, 0, len(slots))
	for _, slot := range slots {
		if !validBase(slot.Path) ||
			!strings.HasPrefix(slot.Path, configuration.Base+"/worktrees/") {
			return nil, ErrPreparationUnconfirmed
		}
		check := Check{Name: "slot-trust", Status: slot.Status, Detail: slot.Path}
		switch slot.Status {
		case "ready":
		case "missing":
			check.Step = "On the remote, run codex -C " + sshcommand.Quote(
				slot.Path,
			) + " and make the one-time folder-trust decision for " + sshcommand.Quote(
				slot.Path,
			) + "; exit Codex afterwards. Concierge never answers the dialog."
		default:
			return nil, ErrPreparationUnconfirmed
		}
		checks = append(checks, check)
	}
	if len(checks) == 0 {
		checks = append(
			checks,
			Check{
				Name:   "slot-trust",
				Status: "missing",
				Detail: configuration.Base + "/worktrees",
				Step: "The first remote task launch allocates a stable pool slot. Make its native folder-trust decision in that task's pane, then rerun concierge machine check " + sshcommand.Quote(
					machine.Label,
				) + ".",
			},
		)
	}
	return checks, nil
}

const slotTrustExpression = `let
  base = builtins.getEnv "CONCIERGE_SLOT_BASE";
  configuredHome = builtins.getEnv "CODEX_HOME";
  codexHome = if configuredHome == "" then builtins.getEnv "HOME" + "/.codex" else configuredHome;
  readConfig = file: if builtins.pathExists file then builtins.fromTOML (builtins.unsafeDiscardStringContext (builtins.readFile file)) else {};
  system = readConfig "/etc/codex/config.toml";
  user = readConfig (codexHome + "/config.toml");
  projects = (system.projects or {}) // (user.projects or {});
  scan = depth: directory:
    let entries = builtins.readDir directory;
    in if entries ? ".git" && (entries.".git" == "regular" || entries.".git" == "directory") then
      [{ path = directory; status = if ((projects.${directory}.trust_level or "untrusted") == "trusted") then "ready" else "missing"; }]
    else if depth == 0 then [] else builtins.concatLists (map (name:
      if entries.${name} == "directory" then scan (depth - 1) (directory + "/" + name) else []
    ) (builtins.attrNames entries));
  pool = base + "/worktrees";
in if builtins.pathExists pool then scan 6 pool else []
`
