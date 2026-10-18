package remoteworker

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrSocketOwnership = errors.New(
	"remote socket ownership is uncertain; retain resources and inspect the task directory",
)

type SocketOwner struct {
	Task       string `json:"task"`
	Generation string `json:"generation"`
	Verifier   string `json:"verifier"`
}

func (owner SocketOwner) Validate() error {
	if _, err := taskmeta.ParseTaskID(owner.Task); err != nil {
		return ErrSocketOwnership
	}
	decoded, err := hex.DecodeString(owner.Verifier)
	if taskmeta.ValidateSpawnGeneration(owner.Generation) != nil || err != nil ||
		len(decoded) != sha256.Size || hex.EncodeToString(decoded) != owner.Verifier {
		return ErrSocketOwnership
	}
	return nil
}

type socketOwnerRecord struct {
	Format int         `json:"format"`
	Owner  SocketOwner `json:"owner"`
}

func ClaimSocketDirectory(directory string, owner SocketOwner) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	root, err := workeraccess.OpenPrivateDirectory(directory)
	if err != nil {
		return ErrSocketOwnership
	}
	defer root.Close()
	current, err := readSocketOwner(root)
	if err == nil {
		if current != owner {
			return ErrSocketOwnership
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrSocketOwnership
	}
	for _, name := range []string{"worker.sock", "agent.sock"} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return ErrSocketOwnership
		}
	}
	secret, err := workeraccess.ReadDirectorySecret(root)
	if err != nil {
		return ErrSocketOwnership
	}
	expected, _ := hex.DecodeString(owner.Verifier)
	actual := sha256.Sum256(secret)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return ErrSocketOwnership
	}
	return writeSocketOwner(root, owner)
}

func readSocketOwner(root *os.Root) (SocketOwner, error) {
	before, err := root.Lstat("socket-owner.json")
	if err != nil {
		return SocketOwner{}, err
	}
	stat, owned := before.Sys().(*syscall.Stat_t)
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || !owned ||
		stat.Uid != uint32(os.Geteuid()) {
		return SocketOwner{}, ErrSocketOwnership
	}
	file, err := root.OpenFile("socket-owner.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return SocketOwner{}, ErrSocketOwnership
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return SocketOwner{}, ErrSocketOwnership
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return SocketOwner{}, ErrSocketOwnership
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record socketOwnerRecord
	if decoder.Decode(&record) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		record.Format != 1 || record.Owner.Validate() != nil {
		return SocketOwner{}, ErrSocketOwnership
	}
	after, err := root.Lstat("socket-owner.json")
	if err != nil || !os.SameFile(before, after) {
		return SocketOwner{}, ErrSocketOwnership
	}
	return record.Owner, nil
}

func writeSocketOwner(root *os.Root, owner SocketOwner) (result error) {
	data, err := json.Marshal(socketOwnerRecord{Format: 1, Owner: owner})
	if err != nil {
		return err
	}
	name := ".socket-owner-" + rand.Text()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrSocketOwnership
	}
	defer func() {
		if err := root.Remove(name); err != nil {
			result = errors.Join(result, ErrSocketOwnership)
		}
		result = errors.Join(result, syncSocketDirectory(root))
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return ErrSocketOwnership
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return ErrSocketOwnership
	}
	if err := file.Close(); err != nil {
		return ErrSocketOwnership
	}
	if err := root.Link(name, "socket-owner.json"); err != nil && !errors.Is(err, os.ErrExist) {
		return ErrSocketOwnership
	}
	current, err := readSocketOwner(root)
	if err != nil || current != owner {
		return ErrSocketOwnership
	}
	return nil
}

func syncSocketDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return ErrSocketOwnership
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrSocketOwnership
	}
	return nil
}
