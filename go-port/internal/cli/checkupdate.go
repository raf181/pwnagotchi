package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/jayofelony/pwnagotchi/go-port/internal/config"
)

// CheckUpdate ports cli.py's --check-update branch: fetches the latest
// GitHub release, compares versions with the SAME lexical (non-numeric)
// comparison as parse_version/CompareVersions (see
// docs/known-differences.md), and — if a "newer" (lexically) release
// exists — prompts interactively before touching /root/.auto-update and
// restarting the service, exactly like Python's input()-driven flow.
func CheckUpdate(currentVersion string, stdin *os.File, stdout *os.File) error {
	resp, err := http.Get("https://api.github.com/repos/jayofelony/pwnagotchi/releases/latest")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var latest struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return err
	}
	latestVer := strings.ReplaceAll(latest.TagName, "v", "")

	if config.CompareVersions(latestVer, currentVersion) > 0 {
		fmt.Fprintf(stdout, "There is a new version available! Update from v%s to v%s?\n[Y/N] ", currentVersion, latestVer)
		reader := bufio.NewReader(stdin)
		line, _ := reader.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			if _, err := os.Stat("/root/.auto-update"); err == nil {
				// Python: os.system("rm /root/.auto-update && systemctl
				// restart pwnagotchi") — the second command only runs if
				// the first succeeds; replicated with the same
				// short-circuit rather than always running both.
				if rmErr := os.Remove("/root/.auto-update"); rmErr == nil {
					exec.Command("systemctl", "restart", "pwnagotchi").Run()
				}
			} else {
				fmt.Fprintln(stdout, "You should make sure auto-update is enabled!")
			}
			fmt.Fprintln(stdout, "Okay, give me a couple minutes. Just watch pwnlog while you wait.")
		case "n", "no":
			fmt.Fprintln(stdout, "Okay, guess not!")
		}
	} else {
		fmt.Fprintf(stdout, "You are currently on the latest release, v%s.\n", currentVersion)
	}
	return nil
}
