package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/config"
	"github.com/truthatt11/confluence-cli/skill"
)

func (a *app) installSkillCmd() *cobra.Command {
	var dest string
	var force bool
	cmd := &cobra.Command{
		Use:   "install-skill",
		Short: "Install the Claude skill that teaches agents to use cfl",
		Long: "Write the bundled SKILL.md to ~/.claude/skills/confluence (or --dest), so Claude Code\n" +
			"knows how to use cfl. Re-run after upgrading cfl to update the skill.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if dest == "" {
				home := config.Home(a.env)
				if home == "" {
					return invalid("cannot find the home directory: pass --dest")
				}
				dest = filepath.Join(home, ".claude", "skills", "confluence")
			}
			path := filepath.Join(dest, "SKILL.md")
			existing, err := os.ReadFile(path)
			switch {
			case err == nil && string(existing) == skill.Content:
				fmt.Fprintf(a.stdout, "Skill already up to date at %s\n", path)
				return nil
			case err == nil && !force:
				return invalid("%s exists and differs from the skill bundled with this cfl; pass --force to replace it", path)
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return err
			}
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(skill.Content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Installed the Confluence skill to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&dest, "dest", "", "skill directory (default ~/.claude/skills/confluence)")
	cmd.Flags().BoolVar(&force, "force", false, "replace a SKILL.md that differs from the bundled one")
	return cmd
}
