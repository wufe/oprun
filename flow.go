package main

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Flow struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description,omitempty"`
	Vars        []VarDecl `yaml:"vars,omitempty"`
	Nodes       []Node    `yaml:"nodes"`

	// FromRepoRoot, when true, makes relative `dir:` values and the default
	// cwd for `exec` nodes resolve from the nearest ancestor containing a
	// `.git` entry instead of the process's cwd. The walk is capped at 10
	// levels and refuses to ascend past well-known system directories.
	FromRepoRoot bool `yaml:"from_repo_root,omitempty"`
}

type VarDecl struct {
	Name    string `yaml:"name"`
	Prompt  string `yaml:"prompt,omitempty"`
	Default string `yaml:"default,omitempty"`
}

// Node is a discriminated union keyed on Type. Only fields relevant to the
// chosen Type are read; the rest are ignored.
type Node struct {
	ID          string `yaml:"id,omitempty"`
	Type        string `yaml:"type"`
	Description string `yaml:"description,omitempty"`

	// when, if set, gates the whole node: the string is run through {var}
	// substitution and evaluated as truthy ("" / "no" / "false" / "0" / "off"
	// = skip, anything else = run). Applies to every node type.
	When string `yaml:"when,omitempty"`

	// exec
	Run     string `yaml:"run,omitempty"`
	Dir     string `yaml:"dir,omitempty"`
	Capture string `yaml:"capture,omitempty"`

	// confirm
	Prompt string `yaml:"prompt,omitempty"`
	OnYes  []Node `yaml:"on_yes,omitempty"`
	OnNo   []Node `yaml:"on_no,omitempty"`

	// choose
	Options    []Option `yaml:"options,omitempty"`
	Multi      bool     `yaml:"multi,omitempty"`
	OptionsCmd string   `yaml:"options_cmd,omitempty"`
	Store      string   `yaml:"store,omitempty"`

	// goto
	Goto string `yaml:"goto,omitempty"`

	// foreach
	Var string `yaml:"var,omitempty"`
	As  string `yaml:"as,omitempty"`
	Do  []Node `yaml:"do,omitempty"`
}

type Option struct {
	Label string `yaml:"label"`
	Do    []Node `yaml:"do,omitempty"`
	Goto  string `yaml:"goto,omitempty"`
}

func LoadFlow(data []byte) (*Flow, error) {
	var f Flow
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse flow: %w", err)
	}
	if f.Name == "" {
		return nil, fmt.Errorf("flow must have a name")
	}
	if len(f.Nodes) == 0 {
		return nil, fmt.Errorf("flow %q has no nodes", f.Name)
	}
	defaultNodeTypes(f.Nodes)
	return &f, nil
}

// defaultNodeTypes recurses through every nested node list and fills in
// Type="exec" when omitted. Makes the yaml shorter since exec is the common case.
func defaultNodeTypes(nodes []Node) {
	for i := range nodes {
		n := &nodes[i]
		if n.Type == "" {
			n.Type = "exec"
		}
		defaultNodeTypes(n.OnYes)
		defaultNodeTypes(n.OnNo)
		defaultNodeTypes(n.Do)
		for j := range n.Options {
			defaultNodeTypes(n.Options[j].Do)
		}
	}
}
