// Package remoteworker controls task resources on saved SSH machines.
package remoteworker

import (
	"errors"

	"github.com/roman/concierge/pkg/taskmeta"
)

type Tools struct {
	Concierge     string
	TaskDirectory string
}

func (tools Tools) Validate() error {
	for _, path := range []string{tools.Concierge, tools.TaskDirectory} {
		if _, err := taskmeta.ParseAbsolutePath(path); err != nil {
			return err
		}
	}
	if tools.TaskDirectory == "/" || tools.Concierge == "/" {
		return errors.New("remote worker tools cannot use the filesystem root")
	}
	return nil
}
