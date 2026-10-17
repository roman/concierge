package workeraccess

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"

	"github.com/roman/concierge/pkg/taskmeta"
)

// RemoveSecretFile requires the primary to revoke the grant first. The expected
// verifier prevents cleanup from removing a different task incarnation's file.
func RemoveSecretFile(directory, verifier string) error {
	expected, err := hex.DecodeString(verifier)
	if err != nil || len(expected) != sha256.Size {
		return ErrSecretConflict
	}
	if _, err := taskmeta.ParseAbsolutePath(directory); err != nil || directory == "/" {
		return ErrPrivateSecret
	}
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	root, err := openPrivateDirectory(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	before, err := root.Lstat("secret")
	if errors.Is(err, os.ErrNotExist) {
		return syncSecretDirectory(root)
	}
	if err != nil {
		return ErrPrivateSecret
	}
	data, err := readSecretAt(root, "secret")
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return ErrSecretConflict
	}
	after, err := root.Lstat("secret")
	if err != nil || !os.SameFile(before, after) {
		return ErrPrivateSecret
	}
	if err := root.Remove("secret"); err != nil {
		return err
	}
	return syncSecretDirectory(root)
}
