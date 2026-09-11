// Package config loads and saves cfl profiles and decides which one applies.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultProfile is the profile name used when none is given.
const DefaultProfile = "default"

const fileName = "config.json"

// Env looks up an environment variable; tests pass a fake.
type Env func(string) string

// Profile is one Confluence connection. The JSON shape matches
// pchuri/confluence-cli, so an existing profile can be copied over.
type Profile struct {
	Domain   string `json:"domain"`
	Protocol string `json:"protocol,omitempty"`
	APIPath  string `json:"apiPath,omitempty"`
	AuthType string `json:"authType,omitempty"`
	Email    string `json:"email,omitempty"` // username on Data Center
	Token    string `json:"token,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// File is the whole config file.
type File struct {
	ActiveProfile string             `json:"activeProfile"`
	Profiles      map[string]Profile `json:"profiles"`
}

// Dir returns the config directory: $CONFLUENCE_CONFIG_DIR, else
// $XDG_CONFIG_HOME/cfl, else ~/.config/cfl.
func Dir(env Env) (string, error) {
	if d := env("CONFLUENCE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if x := env("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cfl"), nil
	}
	if h := Home(env); h != "" {
		return filepath.Join(h, ".config", "cfl"), nil
	}
	return "", errors.New("cannot locate the config directory: set HOME or CONFLUENCE_CONFIG_DIR")
}

// Home is the user's home directory: $HOME, or %USERPROFILE% on Windows.
func Home(env Env) string {
	if h := env("HOME"); h != "" {
		return h
	}
	return env("USERPROFILE")
}

// Path returns the config file path inside dir.
func Path(dir string) string { return filepath.Join(dir, fileName) }

// Load reads the config file. A missing file is an empty config, not an error.
func Load(dir string) (File, error) {
	data, err := os.ReadFile(Path(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return File{Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", Path(dir), err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", Path(dir), err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	return f, nil
}

// Save writes the config atomically with owner-only permissions, since it holds tokens.
func Save(dir string, f File) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("secure config: %w", err)
	}
	if err := os.Rename(tmp.Name(), Path(dir)); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func (f File) clone() File {
	out := File{ActiveProfile: f.ActiveProfile, Profiles: make(map[string]Profile, len(f.Profiles))}
	for k, v := range f.Profiles {
		out.Profiles[k] = v
	}
	return out
}

// WithProfile returns a copy with name set to p.
func (f File) WithProfile(name string, p Profile) File {
	out := f.clone()
	out.Profiles[name] = p
	return out
}

// WithActive returns a copy whose active profile is name.
func (f File) WithActive(name string) File {
	out := f.clone()
	out.ActiveProfile = name
	return out
}

// WithoutProfile returns a copy without name. If it was active, another
// remaining profile (alphabetically first) becomes active.
func (f File) WithoutProfile(name string) File {
	out := f.clone()
	delete(out.Profiles, name)
	if out.ActiveProfile == name {
		out.ActiveProfile = ""
		if names := out.Names(); len(names) > 0 {
			out.ActiveProfile = names[0]
		}
	}
	return out
}

// Names lists the profile names in alphabetical order.
func (f File) Names() []string {
	names := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Normalized fills defaults and repairs common input mistakes, such as a
// full URL pasted as the domain.
func (p Profile) Normalized() Profile {
	p.Domain = strings.TrimSpace(p.Domain)
	if strings.Contains(p.Domain, "://") {
		if u, err := url.Parse(p.Domain); err == nil && u.Host != "" {
			p.Domain = u.Host
			if p.Protocol == "" {
				p.Protocol = u.Scheme
			}
			if ctx := strings.TrimRight(u.Path, "/"); ctx != "" && p.APIPath == "" {
				p.APIPath = ctx + "/rest/api"
			}
		}
	}
	p.Domain = strings.TrimRight(p.Domain, "/")
	p.Protocol = strings.ToLower(defaultTo(p.Protocol, "https"))
	p.APIPath = "/" + strings.Trim(defaultTo(p.APIPath, "/rest/api"), "/")
	if p.AuthType == "" && p.Email != "" {
		p.AuthType = "basic"
	}
	p.AuthType = strings.ToLower(defaultTo(p.AuthType, "bearer"))
	return p
}

// Validate reports the first problem that would stop a request from working.
func (p Profile) Validate() error {
	switch {
	case p.Domain == "":
		return errors.New("no Confluence domain configured: run `cfl init` or set CONFLUENCE_DOMAIN")
	case p.Protocol != "https" && p.Protocol != "http":
		return fmt.Errorf("unsupported protocol %q: use https or http", p.Protocol)
	case p.AuthType != "bearer" && p.AuthType != "basic":
		return fmt.Errorf("unsupported auth type %q: use bearer (personal access token) or basic", p.AuthType)
	case p.Token == "":
		return errors.New("no token configured: run `cfl init` or set CONFLUENCE_API_TOKEN")
	case p.AuthType == "basic" && p.Email == "":
		return errors.New("basic auth needs a username: run `cfl init` or set CONFLUENCE_EMAIL")
	}
	return nil
}

func defaultTo(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
