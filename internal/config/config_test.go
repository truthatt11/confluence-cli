package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) Env { return func(k string) string { return m[k] } }

func TestDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"explicit override", map[string]string{"CONFLUENCE_CONFIG_DIR": "/x/cfg", "XDG_CONFIG_HOME": "/xdg", "HOME": "/h"}, "/x/cfg"},
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/h"}, "/xdg/cfl"},
		{"home", map[string]string{"HOME": "/h"}, "/h/.config/cfl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Dir(envOf(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
	if _, err := Dir(envOf(nil)); err == nil {
		t.Error("expected an error when no directory can be determined")
	}
}

func TestSaveLoadRoundTripAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfl")
	empty, err := Load(dir)
	if err != nil {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if len(empty.Profiles) != 0 {
		t.Fatalf("expected no profiles, got %v", empty.Profiles)
	}
	f := empty.WithProfile("work", Profile{Domain: "wiki.example.com", Token: "secret"}).WithActive("work")
	if err := Save(dir, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveProfile != "work" || got.Profiles["work"].Token != "secret" {
		t.Errorf("round trip lost data: %+v", got)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, "config.json"), 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Errorf("expected a parse error naming the file, got %v", err)
	}
}

func TestFileHelpersDoNotMutate(t *testing.T) {
	base := File{}.WithProfile("a", Profile{Domain: "a"})
	next := base.WithProfile("b", Profile{Domain: "b"}).WithActive("b")
	if _, ok := base.Profiles["b"]; ok || base.ActiveProfile == "b" {
		t.Error("WithProfile/WithActive mutated the receiver")
	}
	removed := next.WithoutProfile("b")
	if _, ok := next.Profiles["b"]; !ok {
		t.Error("WithoutProfile mutated the receiver")
	}
	if removed.ActiveProfile != "a" {
		t.Errorf("removing the active profile should fall back to a remaining one, got %q", removed.ActiveProfile)
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	f := File{}.
		WithProfile("default", Profile{Domain: "default.example.com", Token: "t1"}).
		WithProfile("ro", Profile{Domain: "ro.example.com", Token: "t2", ReadOnly: true}).
		WithActive("default")
	if err := Save(dir, f); err != nil {
		t.Fatal(err)
	}
	envProfile := map[string]string{"CONFLUENCE_DOMAIN": "env.example.com", "CONFLUENCE_API_TOKEN": "te"}

	tests := []struct {
		name       string
		flag       string
		env        map[string]string
		wantDomain string
		wantSource string
		wantRO     bool
	}{
		{"active profile", "", nil, "default.example.com", "profile", false},
		{"flag wins over env vars", "ro", envProfile, "ro.example.com", "profile", true},
		{"CONFLUENCE_PROFILE wins over env vars", "", merge(envProfile, map[string]string{"CONFLUENCE_PROFILE": "ro"}), "ro.example.com", "profile", true},
		{"env vars win over active profile", "", envProfile, "env.example.com", "env", false},
		{"READ_ONLY env tightens", "", map[string]string{"CONFLUENCE_READ_ONLY": "true"}, "default.example.com", "profile", true},
		{"READ_ONLY=false cannot loosen", "ro", map[string]string{"CONFLUENCE_READ_ONLY": "false"}, "ro.example.com", "profile", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Resolve(dir, tt.flag, envOf(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			if r.Profile.Domain != tt.wantDomain || r.Source != tt.wantSource || r.Profile.ReadOnly != tt.wantRO {
				t.Errorf("got domain=%q source=%q ro=%v", r.Profile.Domain, r.Source, r.Profile.ReadOnly)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Resolve(dir, "", envOf(nil)); err == nil || !strings.Contains(err.Error(), "cfl init") {
		t.Errorf("missing config should point at cfl init, got %v", err)
	}
	if err := Save(dir, File{}.WithProfile("only", Profile{Domain: "d", Token: "t"}).WithActive("only")); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(dir, "nope", envOf(nil))
	if err == nil || !strings.Contains(err.Error(), "only") {
		t.Errorf("unknown profile should list available ones, got %v", err)
	}
	env := envOf(map[string]string{"CONFLUENCE_DOMAIN": "d", "CONFLUENCE_API_TOKEN": "t"})
	if _, err := Resolve(t.TempDir(), "", env); err != nil {
		t.Errorf("env-only configuration should not need a file: %v", err)
	}
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestNormalized(t *testing.T) {
	tests := []struct {
		name string
		in   Profile
		want Profile
	}{
		{"defaults and bearer", Profile{Domain: "wiki.example.com", Token: "t"},
			Profile{Domain: "wiki.example.com", Protocol: "https", APIPath: "/rest/api", AuthType: "bearer", Token: "t"}},
		{"basic when username set", Profile{Domain: "w", Email: "cyang", Token: "p"},
			Profile{Domain: "w", Protocol: "https", APIPath: "/rest/api", AuthType: "basic", Email: "cyang", Token: "p"}},
		{"URL pasted as domain keeps context path", Profile{Domain: " http://intranet.example.com/confluence/ ", Token: "t"},
			Profile{Domain: "intranet.example.com", Protocol: "http", APIPath: "/confluence/rest/api", AuthType: "bearer", Token: "t"}},
		{"api path gets leading slash, loses trailing", Profile{Domain: "w", APIPath: "wiki/rest/api/", Token: "t", AuthType: "Bearer"},
			Profile{Domain: "w", Protocol: "https", APIPath: "/wiki/rest/api", AuthType: "bearer", Token: "t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Normalized(); got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		p       Profile
		wantErr string
	}{
		{"ok", Profile{Domain: "w", Token: "t"}, ""},
		{"missing domain", Profile{Token: "t"}, "domain"},
		{"missing token", Profile{Domain: "w"}, "CONFLUENCE_API_TOKEN"},
		{"basic needs username", Profile{Domain: "w", Token: "t", AuthType: "basic"}, "username"},
		{"bad auth type", Profile{Domain: "w", Token: "t", AuthType: "cookie"}, "auth type"},
		{"bad protocol", Profile{Domain: "w", Token: "t", Protocol: "ftp"}, "protocol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Normalized().Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %v should mention %q", err, tt.wantErr)
			}
		})
	}
}
