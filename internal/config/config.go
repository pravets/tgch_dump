package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the full application configuration.
type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Channels []string       `yaml:"channels"`
	Output   OutputConfig   `yaml:"output"`
	Dump     DumpConfig     `yaml:"dump"`
}

// TelegramConfig holds Telegram API credentials and TDLib settings.
type TelegramConfig struct {
	// API ID from https://my.telegram.org
	APIID int32 `yaml:"api_id"`
	// API Hash from https://my.telegram.org
	APIHash string `yaml:"api_hash"`
	// Phone number for authentication (optional; prompted interactively if empty)
	Phone string `yaml:"phone"`
	// Directory to store TDLib database files
	DatabaseDir string `yaml:"database_dir"`
	// Directory to store TDLib files (downloads cache)
	FilesDir string `yaml:"files_dir"`
}

// OutputConfig controls where and what is written to disk.
type OutputConfig struct {
	// Root directory for all outputs
	Dir string `yaml:"dir"`
	// Whether to download and save media files
	DownloadMedia bool `yaml:"download_media"`
	// Which media types to download: photo, video, audio, voice_note, document, animation
	MediaTypes []string `yaml:"media_types"`
	// Whether to include reaction counts in frontmatter
	IncludeReactions bool `yaml:"include_reactions"`
	// Whether to include comment counts in frontmatter
	IncludeComments bool `yaml:"include_comments"`
	// Whether to include poll data in frontmatter
	IncludePolls bool `yaml:"include_polls"`
}

// DumpConfig controls dump behaviour.
type DumpConfig struct {
	// Only dump messages newer than the last saved state (default: true)
	Incremental *bool `yaml:"incremental"`
	// Messages requested per GetChatHistory call (max 100)
	MessagesPerBatch int32 `yaml:"messages_per_batch"`
	// Milliseconds to sleep between GetChatHistory requests
	RequestDelayMs int `yaml:"request_delay_ms"`
	// Maximum number of concurrent media downloads
	MaxConcurrentDownloads int `yaml:"max_concurrent_downloads"`
}

// Load reads and validates a Config from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables inside the YAML.
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Telegram.DatabaseDir == "" {
		c.Telegram.DatabaseDir = ".tdlib/db"
	}
	if c.Telegram.FilesDir == "" {
		c.Telegram.FilesDir = ".tdlib/files"
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "./output"
	}
	if c.Dump.MessagesPerBatch <= 0 || c.Dump.MessagesPerBatch > 100 {
		c.Dump.MessagesPerBatch = 100
	}
	if c.Dump.RequestDelayMs <= 0 {
		c.Dump.RequestDelayMs = 150
	}
	if c.Dump.MaxConcurrentDownloads <= 0 {
		c.Dump.MaxConcurrentDownloads = 3
	}
	if len(c.Output.MediaTypes) == 0 {
		c.Output.MediaTypes = []string{"photo", "video", "audio", "voice_note", "document"}
	}
}

func (c *Config) validate() error {
	if c.Telegram.APIID == 0 {
		return fmt.Errorf("telegram.api_id is required")
	}
	if c.Telegram.APIHash == "" {
		return fmt.Errorf("telegram.api_hash is required")
	}
	if len(c.Channels) == 0 {
		return fmt.Errorf("at least one channel must be listed under 'channels'")
	}
	for i, ch := range c.Channels {
		if strings.TrimSpace(ch) == "" {
			return fmt.Errorf("channels[%d] must not be empty", i)
		}
	}
	return nil
}

// MediaTypeEnabled reports whether the given media type (e.g. "photo") should be downloaded.
func (c *OutputConfig) MediaTypeEnabled(t string) bool {
	t = strings.ToLower(t)
	for _, mt := range c.MediaTypes {
		if strings.ToLower(mt) == t {
			return true
		}
	}
	return false
}
