package primary

import (
	"os"
	"syscall"
)

// WithOwner holds ownership stable through a bounded operation. Callers must
// not acquire an exclusive primary claim lock from inside action.
func WithOwner(root string, action func(Identity) error) error {
	return withCurrentOwner(root, func(owner Identity) (bool, error) {
		boot, err := readBootID()
		if err != nil {
			return false, err
		}
		namespace, err := readPIDNamespace()
		if err != nil {
			return false, err
		}
		if namespace != owner.PIDNamespace {
			return false, ErrProcessOwnership
		}
		return checkOwnerLive(owner, boot, namespace, readProcess)
	}, action)
}

func withCurrentOwner(
	root string,
	probe func(Identity) (bool, error),
	action func(Identity) error,
) error {
	_, err := withClaimLock(root, syscall.LOCK_SH|syscall.LOCK_NB, func() (Status, error) {
		owner, err := readOwner(root)
		if err != nil {
			return Held, err
		}
		if err := owner.validate(); err != nil {
			return Held, err
		}
		live, err := probe(owner)
		if err != nil {
			return Held, err
		}
		if !live {
			return Stale, ErrUnauthorizedCaller
		}
		return Owner, action(owner)
	})
	return err
}

// WithCallerOwner uses the local process ancestry while holding the same claim
// lock as the operation. Host socket handlers must use kernel peer credentials.
func WithCallerOwner(root string, caller Caller, action func(Identity) error) error {
	return WithOwner(root, func(owner Identity) error {
		boot, err := readBootID()
		if err != nil {
			return err
		}
		namespace, err := readPIDNamespace()
		if err != nil {
			return err
		}
		if err := authorizeCallerIdentity(owner, caller, uint32(os.Geteuid()), boot, namespace, readProcess); err != nil {
			return err
		}
		return action(owner)
	})
}
