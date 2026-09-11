package config

import (
	"fmt"
	"strings"
)

// Source values for Resolved.Source.
const (
	SourceProfile = "profile"
	SourceEnv     = "env"
)

// Resolved is the connection a command will use.
type Resolved struct {
	Name    string // profile name; empty when configured from the environment
	Source  string
	Profile Profile // normalized
}

// Resolve picks the connection, highest priority first:
//  1. --profile, or CONFLUENCE_PROFILE
//  2. CONFLUENCE_DOMAIN plus CONFLUENCE_API_TOKEN (no file needed)
//  3. the config file's active profile
//
// CONFLUENCE_READ_ONLY=true then forces read-only; it can never lift it.
func Resolve(dir, profileFlag string, env Env) (Resolved, error) {
	r, err := pick(dir, profileFlag, env)
	if err != nil {
		return Resolved{}, err
	}
	r.Profile = r.Profile.Normalized()
	if strings.EqualFold(env("CONFLUENCE_READ_ONLY"), "true") {
		r.Profile.ReadOnly = true
	}
	return r, nil
}

func pick(dir, profileFlag string, env Env) (Resolved, error) {
	name := profileFlag
	if name == "" {
		name = env("CONFLUENCE_PROFILE")
	}
	if name == "" && env("CONFLUENCE_DOMAIN") != "" && env("CONFLUENCE_API_TOKEN") != "" {
		return Resolved{Source: SourceEnv, Profile: fromEnv(env)}, nil
	}
	f, err := Load(dir)
	if err != nil {
		return Resolved{}, err
	}
	if name == "" {
		name = defaultTo(f.ActiveProfile, DefaultProfile)
	}
	p, ok := f.Profiles[name]
	if !ok {
		return Resolved{}, missingProfile(name, f)
	}
	return Resolved{Name: name, Source: SourceProfile, Profile: p}, nil
}

func fromEnv(env Env) Profile {
	return Profile{
		Domain:   env("CONFLUENCE_DOMAIN"),
		Protocol: env("CONFLUENCE_PROTOCOL"),
		APIPath:  env("CONFLUENCE_API_PATH"),
		AuthType: env("CONFLUENCE_AUTH_TYPE"),
		Email:    env("CONFLUENCE_EMAIL"),
		Token:    env("CONFLUENCE_API_TOKEN"),
	}
}

func missingProfile(name string, f File) error {
	if len(f.Profiles) == 0 {
		return fmt.Errorf("no configuration found: run `cfl init`, or set CONFLUENCE_DOMAIN and CONFLUENCE_API_TOKEN")
	}
	return fmt.Errorf("profile %q not found; available: %s", name, strings.Join(f.Names(), ", "))
}
