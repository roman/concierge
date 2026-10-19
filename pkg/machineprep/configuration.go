package machineprep

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type Configuration struct {
	Target         string                    `json:"target"`
	Base           string                    `json:"base"`
	Agent          string                    `json:"agent"`
	Authentication sshcommand.Authentication `json:"authentication,omitempty"`
	Repositories   []Repository              `json:"repositories"`
	MaxResultBytes int64                     `json:"max_result_bytes,omitempty"`
}

// DecodeConfiguration binds operator-owned preparation settings to the saved ID
// and destination. Renaming a label cannot silently move a prepared checkout.
func DecodeConfiguration(data []byte, machine herdr.SavedMachine) (Configuration, error) {
	if len(data) > 64<<10 {
		return Configuration{}, errors.New("machine configuration exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var configurations map[string]Configuration
	if decoder.Decode(&configurations) != nil || decoder.Decode(new(any)) != io.EOF {
		return Configuration{}, errors.New(
			"machine configuration must contain one strict JSON object",
		)
	}
	configuration, found := configurations[machine.ID]
	if !found || !machine.Enabled || machine.ID == "" || configuration.Target != machine.Target {
		return Configuration{}, errors.New(
			"configure the saved machine ID and its current SSH target",
		)
	}
	if err := configuration.Validate(); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func (configuration Configuration) Validate() error {
	if configuration.MaxResultBytes < 0 {
		return errors.New("max_result_bytes must be zero for the default or a positive byte limit")
	}
	if err := configuration.Authentication.Validate(); err != nil {
		return err
	}
	if configuration.Base != "" && !validBase(configuration.Base) {
		return errors.New("machine base must be an absolute, clean remote directory other than /")
	}
	if configuration.Agent != "codex" && configuration.Agent != "claude" {
		return errors.New("machine agent must be codex or claude")
	}
	if err := validateRepositories(configuration.Repositories); err != nil {
		return err
	}
	for _, repository := range configuration.Repositories {
		if repository.Name == "concierge" {
			return nil
		}
	}
	return errors.New("declare the concierge source repository before preparing the client")
}

func (configuration Configuration) validateResolved() error {
	if err := configuration.Validate(); err != nil {
		return err
	}
	if configuration.Base == "" {
		return errors.New("resolve the remote base before using prepared paths")
	}
	return nil
}

func validateRepositories(repositories []Repository) error {
	seen := make(map[string]bool)
	for _, repository := range repositories {
		if !validRepositoryName(repository.Name) || seen[repository.Name] ||
			!validRepositoryURL(repository.URL) {
			return errors.New(
				"repositories require unique path-safe names and credential-free SSH or HTTPS URLs",
			)
		}
		seen[repository.Name] = true
	}
	return nil
}

func validRepositoryName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 100 {
		return false
	}
	return strings.IndexFunc(name, func(character rune) bool {
		return !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.", character))
	}) < 0
}
