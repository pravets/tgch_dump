// Package state manages the incremental-dump state persisted as .state.json
// inside each channel's output directory.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const stateFileName = ".state.json"

// ChannelState records the progress of a channel dump.
type ChannelState struct {
	ChannelID           int64     `json:"channel_id"`
	ChannelName         string    `json:"channel_name"`
	LastMessageID       int64     `json:"last_message_id"`
	LastDumpTime        time.Time `json:"last_dump_time"`
	TotalMessagesDumped int64     `json:"total_messages_dumped"`
}

// Load reads the state from outDir/.state.json.
// If the file does not exist an empty ChannelState is returned.
func Load(outDir string) (ChannelState, error) {
	path := filepath.Join(outDir, stateFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ChannelState{}, nil
	}
	if err != nil {
		return ChannelState{}, err
	}

	var s ChannelState
	if err := json.Unmarshal(data, &s); err != nil {
		return ChannelState{}, err
	}
	return s, nil
}

// Save writes s to outDir/.state.json atomically (write-then-rename).
func Save(outDir string, s ChannelState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	tmp := filepath.Join(outDir, stateFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(outDir, stateFileName))
}
