package workeraccess

import "io"

// RestoreSecret recovers setup after a primary restart without issuing another credential.
// The caller must read its private staging file under the task's launch lock.
func (grant Grant) RestoreSecret(input io.Reader) (Secret, error) {
	if input == nil || grant.Validate() != nil || grant.Revoked {
		return Secret{}, ErrDenied
	}
	data, err := readEncodedSecret(input)
	if err != nil {
		return Secret{}, ErrDenied
	}
	if err := grant.Authorize(grant.Task, grant.Generation, grant.Operations[0], data); err != nil {
		return Secret{}, ErrDenied
	}
	return Secret{value: data}, nil
}
