package main

import (
	"encoding/json/v2"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

type preferences struct {
	Autoplay bool `json:"autoplay"`
	Volume   int  `json:"volume"`
}

func loadPreferences(path string) (preferences, error) {
	defaults := preferences{Volume: 100}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	var stored *struct {
		Autoplay *bool `json:"autoplay"`
		Volume   *int  `json:"volume"`
	}
	if err := json.Unmarshal(data, &stored, json.RejectUnknownMembers(true)); err != nil {
		return defaults, err
	}
	if stored == nil || stored.Autoplay == nil || stored.Volume == nil || *stored.Volume < 0 || *stored.Volume > 100 {
		return defaults, errors.New("preferences require autoplay and a volume from 0 to 100")
	}
	return preferences{Autoplay: *stored.Autoplay, Volume: *stored.Volume}, nil
}

func savePreferences(path string, settings preferences) error {
	if path == "" {
		return errors.New("preference writes are disabled")
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()

	tmp, err := os.CreateTemp(dir, ".preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	err = tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	// The rename has committed the new value. Keep memory in step with the visible file.
	if err := parent.Sync(); err != nil {
		slog.Warn("couldn't sync preferences directory", "error", err)
	}
	return nil
}

// savePreferenceChange runs with the player's mutex held.
func (p *player) savePreferenceChange(settings preferences) bool {
	if err := savePreferences(p.preferencesPath, settings); err != nil {
		slog.Warn("couldn't save preferences", "guild", p.guild, "error", err)
		return false
	}
	return true
}
