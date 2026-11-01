package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// historyEntry is a device sdev has connected to. Reachable is worked out when
// the history is read.
type historyEntry struct {
	Address       string    `json:"address"`
	Hostname      string    `json:"hostname,omitempty"`
	Login         string    `json:"login,omitempty"`
	Variant       string    `json:"variant,omitempty"`
	LastConnected time.Time `json:"last_connected"`
	Reachable     *bool     `json:"reachable,omitempty"`
}

func historyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sdev", "history.json"), nil
}

func loadHistory(file string) ([]historyEntry, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return []historyEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []historyEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// recordConnection puts the entry first, replacing any for the same address.
func recordConnection(file string, e historyEntry) error {
	entries, err := loadHistory(file)
	if err != nil {
		return err
	}
	e.Reachable = nil
	entries = slices.DeleteFunc(entries, func(old historyEntry) bool { return old.Address == e.Address })
	data, err := json.Marshal(append([]historyEntry{e}, entries...))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "history-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

func readHistory(file string, reachable func(address string) bool) ([]historyEntry, error) {
	entries, err := loadHistory(file)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	for i := range entries {
		wg.Go(func() {
			answers := reachable(entries[i].Address)
			entries[i].Reachable = &answers
		})
	}
	wg.Wait()
	return entries, nil
}

func (d *device) historyEntry() historyEntry {
	e := historyEntry{Address: d.host, Login: d.user, LastConnected: time.Now().UTC()}
	// Details only: the connection itself is what is worth recording. Steam Deck
	// has no hostname command.
	if out, _, err := d.run(`uname -n; . /etc/os-release && echo "$VARIANT_ID"`); err == nil {
		lines := strings.Split(out, "\n")
		e.Hostname = strings.TrimSpace(lines[0])
		if len(lines) > 1 {
			e.Variant = strings.TrimSpace(lines[1])
		}
	}
	return e
}
