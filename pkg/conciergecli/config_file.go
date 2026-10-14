package conciergecli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

func readOptionalConfiguration(path string) ([]byte, error) {
	// Nonblocking open lets us reject a FIFO without waiting for a writer.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file: %s", path)
	}
	return io.ReadAll(file)
}
