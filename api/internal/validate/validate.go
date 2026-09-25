// Package validate contains input validation for stacks and registries.
package validate

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	branchRe  = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	envKeyRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	regHostRe = regexp.MustCompile(`^[A-Za-z0-9.\-:/_\[\]]+$`)
)

func StackName(s string) error {
	if !slugRe.MatchString(s) {
		return errors.New("name must match ^[a-z0-9][a-z0-9-]{0,62}$")
	}
	return nil
}

func RepoURL(s string) error {
	if s == "" {
		return errors.New("repo_url is required")
	}
	if strings.ContainsAny(s, " \t\r\n\x00") {
		return errors.New("repo_url must not contain whitespace")
	}
	u, err := url.Parse(s)
	if err != nil {
		return errors.New("repo_url is not a valid URL")
	}
	if u.Scheme != "https" {
		return errors.New("repo_url must start with https://")
	}
	if u.Host == "" || u.Hostname() == "" {
		return errors.New("repo_url must include a host")
	}
	if u.User != nil {
		return errors.New("repo_url must not contain credentials; use the token field")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("repo_url must not contain a query or fragment")
	}
	if u.Path == "" || u.Path == "/" {
		return errors.New("repo_url must include a repository path")
	}
	return nil
}

func Branch(s string) error {
	if s == "" {
		return errors.New("branch is required")
	}
	if len(s) > 255 {
		return errors.New("branch is too long")
	}
	if strings.HasPrefix(s, "-") {
		return errors.New("branch must not start with '-'")
	}
	if !branchRe.MatchString(s) {
		return errors.New("branch may only contain A-Z a-z 0-9 . _ / -")
	}
	if strings.Contains(s, "..") || strings.Contains(s, "//") ||
		strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") ||
		strings.HasSuffix(s, ".") || strings.HasSuffix(s, ".lock") {
		return errors.New("branch is not a valid git branch name")
	}
	return nil
}

// ComposePath validates a repo-relative compose file path and returns its
// cleaned (slash-separated) form.
func ComposePath(s string) (string, error) {
	if s == "" {
		return "", errors.New("compose_path is required")
	}
	if strings.ContainsAny(s, "\x00\r\n\\") {
		return "", errors.New("compose_path contains invalid characters")
	}
	if strings.HasPrefix(s, "/") {
		return "", errors.New("compose_path must be relative")
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return "", errors.New("compose_path must not contain '..'")
		}
	}
	c := path.Clean(s)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
		return "", errors.New("compose_path must point to a file inside the repository")
	}
	if len(c) > 1024 {
		return "", errors.New("compose_path is too long")
	}
	return c, nil
}

func EnvKey(k string) error {
	if !envKeyRe.MatchString(k) {
		return fmt.Errorf("invalid env key %q: must match ^[A-Za-z_][A-Za-z0-9_]*$", k)
	}
	return nil
}

func EnvValue(k, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("env value for %q must not contain newlines", k)
	}
	return nil
}

// Env validates a full env map.
func Env(env map[string]string) error {
	for k, v := range env {
		if err := EnvKey(k); err != nil {
			return err
		}
		if err := EnvValue(k, v); err != nil {
			return err
		}
	}
	return nil
}

func RegistryURL(s string) error {
	if s == "" {
		return errors.New("url is required")
	}
	if len(s) > 255 || !regHostRe.MatchString(s) {
		return errors.New("url must be a registry host such as ghcr.io")
	}
	return nil
}

func RegistryUsername(s string) error {
	if strings.ContainsAny(s, ":\r\n\x00") {
		return errors.New("username must not contain ':' or newlines")
	}
	return nil
}
