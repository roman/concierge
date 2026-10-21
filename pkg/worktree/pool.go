package worktree

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lease identifies one durable treehouse acquisition.
type Lease struct {
	Path   string `json:"path"`
	ID     string `json:"lease_id"`
	Holder string `json:"lease_holder"`
}

type PoolClient struct {
	Executable string
	Root       string
}

type poolEntry struct {
	Lease
	Name           string         `json:"name"`
	Flavor         string         `json:"flavor"`
	RecoveryReason string         `json:"recovery_reason"`
	BranchError    string         `json:"branch_error"`
	Processes      *[]poolProcess `json:"processes"`
}

type poolProcess struct {
	PID int `json:"pid"`
}

func cleanPoolPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && poolText(path)
}

func poolText(value string) bool {
	return value != "" && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validLeaseForProject(project string, lease Lease) bool {
	return cleanPoolPath(project) && cleanPoolPath(lease.Path) && poolText(lease.ID) &&
		poolText(lease.Holder) &&
		!samePath(lease.Path, project)
}

func (client PoolClient) run(
	ctx context.Context,
	project string,
	arguments ...string,
) ([]byte, error) {
	if !cleanPoolPath(client.Executable) || !cleanPoolPath(project) {
		return nil, fmt.Errorf("treehouse executable and project must be clean absolute paths")
	}
	commandArguments := arguments
	if client.Root != "" {
		if !cleanPoolPath(client.Root) {
			return nil, fmt.Errorf("treehouse pool root must be a clean absolute path")
		}
		commandArguments = append([]string{"--root", client.Root}, arguments...)
	}
	command := exec.CommandContext(ctx, client.Executable, commandArguments...)
	command.Dir = project
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf(
				"treehouse %s: %w: %s",
				arguments[0],
				err,
				strings.TrimSpace(string(exit.Stderr)),
			)
		}
		return nil, fmt.Errorf("treehouse %s: %w", arguments[0], err)
	}
	return output, nil
}

func (client PoolClient) entries(ctx context.Context, project string) ([]poolEntry, error) {
	output, err := client.run(ctx, project, "status", "--json")
	if err != nil {
		return nil, err
	}
	var entries []poolEntry
	if json.Unmarshal(output, &entries) != nil || entries == nil {
		return nil, fmt.Errorf("%w: invalid treehouse JSON inventory", ErrIdentity)
	}
	paths := map[string]bool{}
	for _, entry := range entries {
		if !cleanPoolPath(entry.Path) || paths[entry.Path] || entry.Processes == nil {
			return nil, fmt.Errorf("%w: incomplete or duplicate treehouse inventory", ErrIdentity)
		}
		if client.Root != "" &&
			!strings.HasPrefix(entry.Path, client.Root+string(filepath.Separator)) {
			return nil, fmt.Errorf(
				"%w: worktree inventory escapes the explicit pool root",
				ErrIdentity,
			)
		}
		paths[entry.Path] = true
	}
	return entries, nil
}

// Find recovers a holder's existing acquisition without creating another lease.
func (client PoolClient) Find(ctx context.Context, project, holder string) (Lease, bool, error) {
	if !poolText(holder) {
		return Lease{}, false, ErrIdentity
	}
	entries, err := client.entries(ctx, project)
	if err != nil {
		return Lease{}, false, err
	}
	var found Lease
	for _, entry := range entries {
		if entry.Holder != holder {
			continue
		}
		if !validLeaseForProject(project, entry.Lease) || found.ID != "" || entry.Flavor != "git" ||
			entry.RecoveryReason != "" ||
			entry.BranchError != "" {
			return Lease{}, false, fmt.Errorf(
				"%w: ambiguous or damaged treehouse lease",
				ErrIdentity,
			)
		}
		found = entry.Lease
	}
	return found, found.ID != "", nil
}

// ReserveReturned leases a free slot in place, without resetting its checkout.
func (client PoolClient) ReserveReturned(
	ctx context.Context,
	project, path, holder string,
) (Lease, error) {
	if !cleanPoolPath(path) || !poolText(holder) {
		return Lease{}, ErrIdentity
	}
	entries, err := client.entries(ctx, project)
	if err != nil {
		return Lease{}, err
	}
	for _, entry := range entries {
		if entry.Path != path {
			continue
		}
		if entry.Name == "" || entry.Flavor != "git" ||
			entry.RecoveryReason != "" || entry.BranchError != "" || len(*entry.Processes) != 0 {
			return Lease{}, fmt.Errorf(
				"%w: recorded treehouse slot is leased or damaged",
				ErrIdentity,
			)
		}
		if entry.ID != "" || entry.Holder != "" {
			if entry.ID == "" || entry.Holder != holder {
				return Lease{}, fmt.Errorf(
					"%w: recorded treehouse slot has another holder",
					ErrIdentity,
				)
			}
			confirmed, found, err := client.Find(ctx, project, holder)
			if err != nil || !found || confirmed != entry.Lease {
				return entry.Lease, fmt.Errorf(
					"%w: prior recovery lease is unconfirmed: %v",
					ErrIdentity,
					err,
				)
			}
			if err := client.ConfirmReservation(ctx, project, entry.Lease); err != nil {
				return entry.Lease, err
			}
			return entry.Lease, nil
		}
		output, err := client.run(
			ctx,
			project,
			"lease",
			entry.Name,
			"--json",
			"--lease-holder",
			holder,
		)
		if err != nil {
			return Lease{}, err
		}
		var lease Lease
		if json.Unmarshal(output, &lease) != nil || lease.ID == "" || lease.Path != path ||
			lease.Holder != holder {
			mismatch := fmt.Errorf(
				"%w: Treehouse returned a different recovery lease",
				ErrIdentity,
			)
			if cleanupErr := client.releaseUnconfirmedReservation(ctx, project, holder); cleanupErr != nil {
				return Lease{}, fmt.Errorf(
					"%w; recovery lease cleanup failed: %v",
					mismatch,
					cleanupErr,
				)
			}
			return Lease{}, mismatch
		}
		confirmed, found, err := client.Find(ctx, project, holder)
		if err != nil || !found || confirmed != lease {
			return lease, fmt.Errorf("%w: recovery lease is unconfirmed: %v", ErrIdentity, err)
		}
		if err := client.ConfirmReservation(ctx, project, lease); err != nil {
			return lease, err
		}
		return lease, nil
	}
	return Lease{}, fmt.Errorf("%w: recorded treehouse slot is absent", ErrIdentity)
}

func (client PoolClient) releaseUnconfirmedReservation(
	ctx context.Context,
	project, holder string,
) error {
	lease, found, err := client.Find(ctx, project, holder)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: recovery lease holder is absent", ErrIdentity)
	}
	return client.Return(ctx, project, lease)
}

// ConfirmReservation requires the exact recovery lease to remain quiet and undamaged.
func (client PoolClient) ConfirmReservation(
	ctx context.Context,
	project string,
	lease Lease,
) error {
	if !validLeaseForProject(project, lease) {
		return ErrIdentity
	}
	entries, err := client.entries(ctx, project)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Path != lease.Path {
			continue
		}
		if entry.Lease != lease || entry.Flavor != "git" || entry.RecoveryReason != "" ||
			entry.BranchError != "" || len(*entry.Processes) != 0 {
			return fmt.Errorf("%w: recovery lease changed or has live processes", ErrIdentity)
		}
		return nil
	}
	return fmt.Errorf("%w: recovery lease slot is absent", ErrIdentity)
}

// Acquire confirms the returned identity against treehouse's durable inventory.
// The caller must record the request before this non-idempotent external effect.
func (client PoolClient) Acquire(ctx context.Context, project, holder string) (Lease, error) {
	if !poolText(holder) {
		return Lease{}, ErrIdentity
	}
	arguments := []string{
		"get",
		"--lease",
		"--json",
		"--no-fetch",
		"--lease-holder",
		holder,
	}
	if client.Root != "" {
		if _, err := client.entries(ctx, project); err != nil {
			return Lease{}, err
		}
		// Repository configuration and older slots can specify paths outside the pool.
		arguments = append(arguments, "--worktree-path", "{pool}/{slot}/{repo}")
	}
	output, err := client.run(ctx, project, arguments...)
	if err != nil {
		return Lease{}, err
	}
	var lease Lease
	if json.Unmarshal(output, &lease) != nil || !validLeaseForProject(project, lease) ||
		lease.Holder != holder ||
		lease.Path == project {
		return Lease{}, fmt.Errorf("%w: invalid acquired treehouse lease", ErrIdentity)
	}
	confirmed, found, err := client.Find(ctx, project, holder)
	if err != nil {
		return Lease{}, err
	}
	if !found || confirmed != lease {
		return Lease{}, fmt.Errorf("%w: treehouse acquisition is unconfirmed", ErrIdentity)
	}
	return lease, nil
}

// Return releases only the recorded, quiet lease and verifies the postcondition.
// Dirty worktrees receive EOF rather than approval to discard their changes.
func (client PoolClient) Return(ctx context.Context, project string, lease Lease) error {
	if !validLeaseForProject(project, lease) {
		return ErrIdentity
	}
	entries, err := client.entries(ctx, project)
	if err != nil {
		return err
	}
	var selected *poolEntry
	for index := range entries {
		if entries[index].Path == lease.Path {
			selected = &entries[index]
		}
	}
	if selected == nil {
		return fmt.Errorf("%w: recorded treehouse worktree is absent", ErrIdentity)
	}
	if selected.ID == "" && selected.Holder == "" {
		return nil
	}
	if selected.Lease != lease || selected.Flavor != "git" || selected.RecoveryReason != "" ||
		selected.BranchError != "" {
		return fmt.Errorf("%w: lease changed or checkout is damaged", ErrIdentity)
	}
	if len(*selected.Processes) != 0 {
		return describeProcessBlockers(ctx, *selected.Processes)
	}
	if _, err := client.run(ctx, project, "return", lease.Path, "--if-lease-id", lease.ID, "--if-lease-holder", lease.Holder); err != nil {
		return err
	}
	entries, err = client.entries(ctx, project)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Path == lease.Path && entry.ID == lease.ID {
			return fmt.Errorf("%w: treehouse lease release is unconfirmed", ErrIdentity)
		}
	}
	return nil
}
