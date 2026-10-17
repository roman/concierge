package herdr

import (
	"context"
	"encoding/json"
	"fmt"
)

// MachineCatalog reads saved SSH destinations without probing or changing them.
type MachineCatalog struct{ Runner Runner }

func (catalog MachineCatalog) Resolve(ctx context.Context, selector string) (SavedMachine, error) {
	if catalog.Runner == nil || selector == "" {
		return SavedMachine{}, ErrResponse
	}
	stdout, stderr, err := catalog.Runner.Run(ctx, "machine", "list", "--json")
	if err != nil || len(stderr) != 0 {
		return SavedMachine{}, fmt.Errorf("%w: saved machine catalog unavailable", ErrCommand)
	}
	var machines []SavedMachine
	if json.Unmarshal(stdout, &machines) != nil || machines == nil {
		return SavedMachine{}, fmt.Errorf("%w: invalid saved machine catalog", ErrResponse)
	}
	identities := make(map[string]SavedMachine, len(machines))
	for _, machine := range machines {
		if _, exists := identities[machine.ID]; exists {
			return SavedMachine{}, fmt.Errorf("%w: duplicate saved machine identity", ErrResponse)
		}
		identities[machine.ID] = machine
	}
	if machine, found := identities[selector]; found {
		return checkedSavedMachine(machine)
	}
	var matches []SavedMachine
	for _, machine := range machines {
		if machine.Label == selector {
			matches = append(matches, machine)
		}
	}
	if len(matches) != 1 {
		return SavedMachine{}, fmt.Errorf("%w: saved machine is missing or ambiguous", ErrResponse)
	}
	return checkedSavedMachine(matches[0])
}

func checkedSavedMachine(machine SavedMachine) (SavedMachine, error) {
	if !machine.Enabled || !healthAtom(machine.ID, false) ||
		!healthAtom(machine.Session, false) || !validSSHDestination(machine.Target) {
		return SavedMachine{}, fmt.Errorf("%w: saved machine is disabled or invalid", ErrResponse)
	}
	return machine, nil
}
