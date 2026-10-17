package workeraccess

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"os"

	"github.com/roman/concierge/pkg/taskmeta"
)

var ErrSecretConflict = errors.New(
	"remote task has a different secret; retain its generation and request recovery",
)
var ErrSecretUnconfirmed = errors.New("worker secret publication requires confirmation")

// InstallSecretFile reads a secret from stdin and publishes it without replacing
// an existing file. Identical retries preserve the original inode.
func InstallSecretFile(directory string, input io.Reader) (result error) {
	data, err := readEncodedSecret(input)
	if err != nil {
		return err
	}
	if _, err := taskmeta.ParseAbsolutePath(directory); err != nil || directory == "/" {
		return ErrPrivateSecret
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	root, err := openPrivateDirectory(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat("secret"); err == nil {
		return confirmSecret(root, data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrPrivateSecret
	}
	name := ".secret-" + rand.Text()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, ErrSecretUnconfirmed)
			return
		}
		result = errors.Join(result, syncSecretDirectory(root))
	}()
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
	if err := root.Link(name, "secret"); err != nil {
		if errors.Is(err, os.ErrExist) {
			return confirmSecret(root, data)
		}
		return err
	}
	return nil
}

func confirmSecret(root *os.Root, expected []byte) error {
	current, err := readSecretAt(root, "secret")
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(current, expected) != 1 {
		return ErrSecretConflict
	}
	return syncSecretDirectory(root)
}

func syncSecretDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return ErrSecretUnconfirmed
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return ErrSecretUnconfirmed
	}
	return nil
}
