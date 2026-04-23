package main

import (
	"github.com/charmbracelet/huh"
)

type Choice struct {
	Label string
	Value string
}

type Prompt struct{}

func (Prompt) Confirm(q string, def bool) (bool, error) {
	v := def
	err := huh.NewConfirm().
		Title(q).
		Value(&v).
		Run()
	return v, err
}

func (Prompt) Choose(header string, choices []Choice, multi bool, defaults []string) ([]string, error) {
	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(c.Label, c.Value)
	}
	if multi {
		v := append([]string(nil), defaults...)
		err := huh.NewMultiSelect[string]().
			Title(header).
			Options(opts...).
			Value(&v).
			Run()
		return v, err
	}
	v := ""
	if len(defaults) > 0 {
		v = defaults[0]
	}
	err := huh.NewSelect[string]().
		Title(header).
		Options(opts...).
		Value(&v).
		Run()
	if err != nil {
		return nil, err
	}
	if v == "" {
		return nil, nil
	}
	return []string{v}, nil
}

func (Prompt) Input(prompt, def string) (string, error) {
	v := def
	err := huh.NewInput().
		Title(prompt).
		Value(&v).
		Run()
	return v, err
}
