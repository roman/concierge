package workeraccess

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/roman/concierge/pkg/taskmeta"
)

var ErrPrivateSecret = errors.New(
	"worker secret requires a current-user 0600 file in a 0700 directory",
)

func ReadSecretFile(path string) ([]byte, error) {
	if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
		return nil, ErrPrivateSecret
	}
	root, err := openPrivateDirectory(filepath.Dir(path))
	if err != nil {
		return nil, ErrPrivateSecret
	}
	defer root.Close()
	return readSecretAt(root, filepath.Base(path))
}

// ReadDirectorySecret preserves the caller's pinned directory identity.
func ReadDirectorySecret(root *os.Root) ([]byte, error) {
	if err := checkPrivateSecretRoot(root); err != nil {
		return nil, err
	}
	return readSecretAt(root, "secret")
}

func checkPrivateSecretRoot(root *os.Root) error {
	if root == nil {
		return ErrPrivateSecret
	}
	directory, err := root.Stat(".")
	if err != nil || !directory.IsDir() || !privateOwner(directory, 0700) {
		return ErrPrivateSecret
	}
	return nil
}

func readSecretAt(root *os.Root, name string) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || !privateOwner(before, 0600) {
		return nil, ErrPrivateSecret
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrPrivateSecret
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateOwner(info, 0600) ||
		!os.SameFile(before, info) {
		return nil, ErrPrivateSecret
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(info, after) {
		return nil, ErrPrivateSecret
	}
	return readEncodedSecret(file)
}

func readEncodedSecret(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 257))
	if err != nil || len(data) != 43 {
		return nil, ErrPrivateSecret
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(string(data))
	if err != nil || len(decoded) != 32 {
		return nil, ErrPrivateSecret
	}
	return data, nil
}

// OpenPrivateDirectory pins a current-user directory with mode 0700.
func OpenPrivateDirectory(path string) (*os.Root, error) {
	return openPrivateDirectory(path)
}

func openPrivateDirectory(path string) (*os.Root, error) {
	if _, err := taskmeta.ParseAbsolutePath(path); err != nil || path == "/" {
		return nil, ErrPrivateSecret
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || !privateOwner(before, 0700) {
		return nil, ErrPrivateSecret
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrPrivateSecret
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) || !privateOwner(after, 0700) {
		root.Close()
		return nil, ErrPrivateSecret
	}
	return root, nil
}

func privateOwner(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == mode
}
