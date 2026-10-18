// Package gitremote resolves repository URLs to the scopes used by GitHub CLI.
package gitremote

import (
	"errors"
	"net/url"
	"strings"
)

func Repository(remote string) (string, error) {
	if strings.HasPrefix(remote, "git@") && !strings.Contains(remote, "://") {
		remote = "ssh://" + strings.Replace(remote, ":", "/", 1)
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if (parsed.Scheme != "https" && parsed.Scheme != "ssh" && parsed.Scheme != "http") ||
		parsed.Host == "" || len(strings.Split(path, "/")) != 2 || strings.ContainsAny(path, " \t\n?#") {
		return "", errors.New("PR delivery requires an origin URL with a host and owner/repository")
	}
	return parsed.Host + "/" + path, nil
}
