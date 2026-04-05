package appinfo

import (
	"bytes"
	"strings"
	"testing"
)

func TestCurrentVersionFallsBackToDev(t *testing.T) {
	original := Version
	t.Cleanup(func() {
		Version = original
	})

	Version = ""

	if got := CurrentVersion(); got != "dev" {
		t.Fatalf("CurrentVersion() = %q, want %q", got, "dev")
	}
}

func TestFormatVersionIsSingleLineAndIncludesRepo(t *testing.T) {
	got := FormatVersion("", "go1.24.0", "darwin", "arm64", false)

	if strings.Contains(got, "\n") {
		t.Fatalf("FormatVersion returned multiline output: %q", got)
	}
	if !strings.Contains(got, "dev") {
		t.Fatalf("FormatVersion missing fallback version: %q", got)
	}
	if !strings.Contains(got, RepoURL) {
		t.Fatalf("FormatVersion missing repo URL: %q", got)
	}
	if strings.Contains(got, "\033[") {
		t.Fatalf("FormatVersion emitted ANSI escapes without color: %q", got)
	}
}

func TestFormatAboutIncludesRepoReleaseAndTransportNote(t *testing.T) {
	got := FormatAbout("v1.2.3", "go1.24.0", "linux", "amd64", true)

	for _, want := range []string{"v1.2.3", RepoURL, ReleasesURL, "STDOUT protocol-clean"} {
		if !strings.Contains(got, want) {
			t.Fatalf("FormatAbout missing %q in %q", want, got)
		}
	}
	if !strings.Contains(got, "\033[") {
		t.Fatalf("FormatAbout missing ANSI escapes when color is enabled: %q", got)
	}
}

func TestSupportsColorRejectsNonTTYWriter(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")

	if SupportsColor(&bytes.Buffer{}) {
		t.Fatal("SupportsColor accepted a non-TTY writer")
	}
}

func TestSupportsColorHonorsNoColor(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")

	if SupportsColor(nil) {
		t.Fatal("SupportsColor ignored NO_COLOR")
	}
}
