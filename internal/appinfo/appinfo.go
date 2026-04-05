package appinfo

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	Name        = "cyber-memory"
	Summary     = "Persistent local memory for MCP agents."
	RepoURL     = "https://github.com/RamboRogers/cyber-memory"
	ReleasesURL = RepoURL + "/releases"
)

// Version is injected at build time via ldflags for tagged releases.
var Version = "dev"

// CurrentVersion returns the build version with a safe fallback.
func CurrentVersion() string {
	return normalizedVersion(Version)
}

// SupportsColor enables ANSI output only for interactive terminals.
func SupportsColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return false
	}

	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// FormatVersion renders a compact one-line version string suitable for CLI output.
func FormatVersion(version, goVersion, goos, goarch string, color bool) string {
	return fmt.Sprintf(
		"%s %s (%s %s/%s) %s",
		style(Name, "1;36", color),
		style(normalizedVersion(version), "1;32", color),
		goVersion,
		goos,
		goarch,
		style(RepoURL, "2", color),
	)
}

// FormatAbout renders a human-facing build and repo summary.
func FormatAbout(version, goVersion, goos, goarch string, color bool) string {
	lines := []string{
		style(Name, "1;36", color),
		style(Summary, "2", color),
		"",
		fmt.Sprintf("%s %s", style("Version:", "1", color), style(normalizedVersion(version), "1;32", color)),
		fmt.Sprintf("%s %s %s/%s", style("Runtime:", "1", color), goVersion, goos, goarch),
		fmt.Sprintf("%s %s", style("Repo:", "1", color), style(RepoURL, "4;36", color)),
		fmt.Sprintf("%s %s", style("Releases:", "1", color), style(ReleasesURL, "4;36", color)),
		fmt.Sprintf("%s %s", style("Transport:", "1", color), "MCP over STDIO (server mode keeps STDOUT protocol-clean)"),
	}

	return strings.Join(lines, "\n")
}

func normalizedVersion(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return "dev"
	}

	return v
}

func style(text, code string, enabled bool) string {
	if !enabled || text == "" {
		return text
	}

	return "\033[" + code + "m" + text + "\033[0m"
}
