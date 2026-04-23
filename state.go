package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type State struct {
	StringVars map[string]string   `json:"string_vars,omitempty"`
	ListVars   map[string][]string `json:"list_vars,omitempty"`
	Confirms   map[string]bool     `json:"confirms,omitempty"`
	Choices    map[string][]string `json:"choices,omitempty"`
}

func newState() *State {
	return &State{
		StringVars: map[string]string{},
		ListVars:   map[string][]string{},
		Confirms:   map[string]bool{},
		Choices:    map[string][]string{},
	}
}

// statePath returns the JSON file used to persist a flow's answers between runs.
// Follows XDG_STATE_HOME, falling back to ~/.local/state.
func statePath(flowName string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "oprun", flowName+".json"), nil
}

func loadState(path string) *State {
	s := newState()
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	if s.StringVars == nil {
		s.StringVars = map[string]string{}
	}
	if s.ListVars == nil {
		s.ListVars = map[string][]string{}
	}
	if s.Confirms == nil {
		s.Confirms = map[string]bool{}
	}
	if s.Choices == nil {
		s.Choices = map[string][]string{}
	}
	return s
}

func saveState(path string, s *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
