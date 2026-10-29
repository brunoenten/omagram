package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	APIID    int     `toml:"api_id"`
	APIHash  string  `toml:"api_hash"`
	Font     string  `toml:"font"`
	FontSize float32 `toml:"font_size"`
	Notify   *bool   `toml:"notifications"`
}

func (c Config) HasCredentials() bool { return c.CredentialsError() == "" }

// CredentialsError describes what is wrong with the configured credentials,
// or returns "" if they look valid. Telegram reports a bad hash as
// API_ID_INVALID, so the format is checked locally first.
func (c Config) CredentialsError() string {
	switch {
	case c.APIID == 0 && c.APIHash == "":
		return "missing"
	case c.APIID <= 0:
		return "api_id must be a positive number"
	case len(c.APIHash) != 32 || strings.Trim(strings.ToLower(c.APIHash), "0123456789abcdef") != "":
		return fmt.Sprintf("api_hash must be 32 hex characters (got %d: %q…)", len(c.APIHash), c.APIHash[:min(4, len(c.APIHash))])
	}
	return ""
}

func (c Config) NotificationsEnabled() bool { return c.Notify == nil || *c.Notify }

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func Path() string { return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "omagram", "config.toml") }

func DataDir() string { return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "omagram") }

func CacheDir() string { return filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), "omagram") }

// DownloadsDir is where saved files land. Prefer XDG_DOWNLOAD_DIR, then the
// user-dirs Downloads folder, then ~/Downloads.
func DownloadsDir() string {
	if v := os.Getenv("XDG_DOWNLOAD_DIR"); v != "" {
		return filepath.Join(v, "omagram")
	}
	if out, err := exec.Command("xdg-user-dir", "DOWNLOAD").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" && d != os.Getenv("HOME") {
			return filepath.Join(d, "omagram")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads", "omagram")
}

func StateDir() string { return xdg("XDG_STATE_HOME", ".local/state") }

// Load reads the config file and applies OMAGRAM_API_ID / OMAGRAM_API_HASH overrides.
func Load() Config {
	var c Config
	_, _ = toml.DecodeFile(Path(), &c)
	if v := os.Getenv("OMAGRAM_API_ID"); v != "" {
		c.APIID, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("OMAGRAM_API_HASH"); v != "" {
		c.APIHash = v
	}
	c.APIHash = strings.TrimSpace(c.APIHash)
	if c.Font == "" {
		c.Font = systemMonospace()
	}
	if c.FontSize <= 0 {
		c.FontSize = 14
	}
	return c
}

// fallbackFonts cover glyphs that terminal fonts usually lack (arrows, emoji).
var fallbackFonts = []string{"Adwaita Mono", "DejaVu Sans Mono", "Noto Sans Symbols 2", "Noto Color Emoji"}

// Typeface returns a Gio typeface list for the configured font. Fonts are
// indexed by their legacy family name (e.g. "JetBrainsMono NF" rather than
// "JetBrainsMono Nerd Font"), so every alias fontconfig knows is included.
func (c Config) Typeface() string {
	families := []string{c.Font}
	if out, err := exec.Command("fc-match", "-f", "%{family}", c.Font).Output(); err == nil {
		for _, f := range strings.Split(string(out), ",") {
			if f = strings.TrimSpace(f); f != "" {
				families = append(families, f)
			}
		}
	}
	return strings.Join(append(families, fallbackFonts...), ", ")
}

func systemMonospace() string {
	out, err := exec.Command("fc-match", "-f", "%{family[0]}", "monospace").Output()
	if err != nil || len(out) == 0 {
		return "monospace"
	}
	return strings.TrimSpace(string(out))
}
