package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/truthatt11/confluence-cli/internal/config"
	"github.com/truthatt11/confluence-cli/skill"
)

func setupEnv(t *testing.T) map[string]string {
	return map[string]string{"HOME": t.TempDir(), "CONFLUENCE_API_TOKEN": "pat-from-env"}
}

func loadConfig(t *testing.T, env map[string]string) config.File {
	t.Helper()
	dir, err := config.Dir(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	f, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInitVerifiesAndSaves(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/user/current", `{"type":"known","username":"cyang","displayName":"Chris Yang"}`)
	env := setupEnv(t)

	r := mustSucceed(t, run(t, env, "", "init", "-d", "http://"+s.host()+"/", "--read-only"))
	assertContains(t, r.stdout, "Chris Yang")
	if strings.Contains(r.stdout+r.stderr, "pat-from-env") {
		t.Error("init printed the token")
	}
	f := loadConfig(t, env)
	p := f.Profiles["default"]
	if f.ActiveProfile != "default" || p.Domain != s.host() || p.Protocol != "http" || p.Token != "pat-from-env" || !p.ReadOnly || p.AuthType != "bearer" {
		t.Errorf("saved config = %+v", f)
	}
	if got := s.requests("GET", "/rest/api/user/current")[0].Header.Get("Authorization"); got != "Bearer pat-from-env" {
		t.Errorf("verification used %q", got)
	}
}

func TestInitRejectsAnonymous(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/user/current", `{"type":"anonymous"}`)
	env := setupEnv(t)
	r := run(t, env, "", "init", "-d", s.host(), "--protocol", "http")
	if r.code == 0 || !strings.Contains(r.stderr, "anonymous") {
		t.Errorf("anonymous login should fail: %+v", r)
	}
	if len(loadConfig(t, env).Profiles) != 0 {
		t.Error("failed init still saved a profile")
	}
}

func TestInitNeedsTokenWhenNotInteractive(t *testing.T) {
	r := run(t, map[string]string{"HOME": t.TempDir()}, "", "init", "-d", "wiki.example.com")
	if r.code == 0 || !strings.Contains(r.stderr, "CONFLUENCE_API_TOKEN") {
		t.Errorf("missing token should explain how to provide it: %+v", r)
	}
	if strings.Contains(strings.Join([]string{r.stdout, r.stderr}, ""), "--token") {
		t.Error("init must not suggest a --token flag")
	}
}

func TestProfiles(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/user/current", `{"type":"known","username":"cyang"}`)
	env := setupEnv(t)
	mustSucceed(t, run(t, env, "", "init", "-d", s.host(), "--protocol", "http"))
	mustSucceed(t, run(t, env, "", "profile", "add", "ro", "-d", s.host(), "--protocol", "http", "--read-only"))

	list := mustSucceed(t, run(t, env, "", "profile", "list"))
	assertContains(t, list.stdout, "* default", "ro", s.host(), "read-only")
	if strings.Contains(list.stdout, "pat-from-env") {
		t.Error("profile list printed a token")
	}

	mustSucceed(t, run(t, env, "", "profile", "use", "ro"))
	if loadConfig(t, env).ActiveProfile != "ro" {
		t.Error("profile use did not switch")
	}
	if r := run(t, env, "", "profile", "use", "nope"); r.code == 0 {
		t.Error("using an unknown profile should fail")
	}
	mustSucceed(t, run(t, env, "", "profile", "remove", "ro"))
	f := loadConfig(t, env)
	if _, ok := f.Profiles["ro"]; ok || f.ActiveProfile != "default" {
		t.Errorf("after remove: %+v", f)
	}
}

func TestInstallSkill(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home}
	want := filepath.Join(home, ".claude", "skills", "confluence", "SKILL.md")

	mustSucceed(t, run(t, env, "", "install-skill"))
	assertFile(t, want, skill.Content)
	mustSucceed(t, run(t, env, "", "install-skill")) // identical content: nothing to do

	if err := os.WriteFile(want, []byte("my local edits"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, env, "", "install-skill"); r.code == 0 {
		t.Error("changed SKILL.md must not be overwritten without --force")
	}
	assertFile(t, want, "my local edits")
	mustSucceed(t, run(t, env, "", "install-skill", "--force"))
	assertFile(t, want, skill.Content)

	custom := filepath.Join(t.TempDir(), "proj-skill")
	mustSucceed(t, run(t, env, "", "install-skill", "--dest", custom))
	assertFile(t, filepath.Join(custom, "SKILL.md"), skill.Content)
}
