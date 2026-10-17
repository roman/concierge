package replyoutbox

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

	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrContext = errors.New(
	"remote reply context is missing, invalid or differs from its task secret",
)

// Context binds offline reply metadata to the task secret delivered by SSH.
// Root and recipient are never opened or selected as resources on the remote.
type Context struct {
	Format     int    `json:"format"`
	Root       string `json:"root"`
	Task       string `json:"task"`
	Generation string `json:"generation"`
	Recipient  string `json:"recipient"`
	Verifier   string `json:"verifier"`
}

func (binding Context) Validate() error {
	metadata := Payload{Root: binding.Root, Task: binding.Task, Generation: binding.Generation,
		Recipient: binding.Recipient, Key: "context", Body: "context"}
	if binding.Format != 1 || metadata.Validate() != nil {
		return ErrContext
	}
	verifier, err := hex.DecodeString(binding.Verifier)
	if err != nil || len(verifier) != sha256.Size ||
		hex.EncodeToString(verifier) != binding.Verifier {
		return ErrContext
	}
	encoded, err := json.Marshal(binding)
	if err != nil || len(encoded) > 16384 {
		return ErrContext
	}
	return nil
}

func (binding Context) Match(task, generation string, secret []byte) error {
	expected := [sha256.Size]byte{}
	decoded, _ := hex.DecodeString(binding.Verifier)
	copy(expected[:], decoded)
	actual := sha256.Sum256(secret)
	matched := subtle.ConstantTimeCompare(expected[:], actual[:])
	if matched != 1 || binding.Validate() != nil || binding.Task != task ||
		binding.Generation != generation {
		return ErrContext
	}
	return nil
}

func ReadContext(directory string) (Context, error) {
	root, err := workeraccess.OpenPrivateDirectory(directory)
	if err != nil {
		return Context{}, ErrContext
	}
	defer root.Close()
	binding, err := readContext(root)
	if err != nil {
		return Context{}, ErrContext
	}
	return binding, nil
}

func readContext(root *os.Root) (Context, error) {
	before, err := root.Lstat("reply-context.json")
	if err != nil {
		return Context{}, err
	}
	if !privateInfo(before, 0600) {
		return Context{}, ErrContext
	}
	file, err := root.OpenFile("reply-context.json", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Context{}, ErrContext
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return Context{}, ErrContext
	}
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 {
		return Context{}, ErrContext
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var binding Context
	if decoder.Decode(&binding) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) ||
		binding.Validate() != nil {
		return Context{}, ErrContext
	}
	after, err := root.Lstat("reply-context.json")
	if err != nil || !os.SameFile(before, after) {
		return Context{}, ErrContext
	}
	return binding, nil
}

func InstallContext(directory string, binding Context) (result error) {
	if err := binding.Validate(); err != nil {
		return err
	}
	root, err := workeraccess.OpenPrivateDirectory(directory)
	if err != nil {
		return ErrContext
	}
	defer root.Close()
	secret, err := workeraccess.ReadDirectorySecret(root)
	if err != nil || binding.Match(binding.Task, binding.Generation, secret) != nil {
		return ErrContext
	}
	previous, err := readContext(root)
	if err == nil {
		if previous != binding {
			return ErrContext
		}
		if syncDirectory(root) != nil {
			return ErrContext
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrContext
	}
	data, _ := json.Marshal(binding)
	name := ".reply-context-" + rand.Text()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrContext
	}
	defer func() {
		if root.Remove(name) != nil || syncDirectory(root) != nil {
			result = errors.Join(result, ErrContext)
		}
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return ErrContext
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return ErrContext
	}
	if file.Close() != nil {
		return ErrContext
	}
	if err := root.Link(name, "reply-context.json"); err != nil && !errors.Is(err, os.ErrExist) {
		return ErrContext
	}
	actual, err := readContext(root)
	if err != nil || actual != binding {
		return ErrContext
	}
	return nil
}
