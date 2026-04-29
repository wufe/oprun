package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

type Choice struct {
	Label    string
	Value    string
	IsHeader bool
	Depth    int // visual nesting (depth*4 spaces of indent)
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

// Choose renders a select / multi-select. When any choice has IsHeader=true,
// we use a custom Bubble Tea model that skips headers on cursor movement and
// rejects toggles on them. Otherwise we fall through to huh's polished widgets
// so plain choose flows look unchanged.
func (Prompt) Choose(header string, choices []Choice, multi bool, defaults []string) ([]string, error) {
	hasHeaders := false
	for _, c := range choices {
		if c.IsHeader {
			hasHeaders = true
			break
		}
	}
	if hasHeaders {
		return chooseWithHeaders(header, choices, multi, defaults)
	}

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

// --- custom Bubble Tea selector for header-aware choose ---

var (
	headerLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
	cursorLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212"))
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			MarginBottom(1)
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)
)

type chooseModel struct {
	title     string
	items     []Choice
	multi     bool
	cursor    int
	selected  map[int]bool
	done      bool
	cancelled bool
}

func newChooseModel(title string, items []Choice, multi bool, defaults []string) chooseModel {
	defSet := make(map[string]bool, len(defaults))
	for _, d := range defaults {
		defSet[d] = true
	}
	sel := map[int]bool{}
	cursor := -1
	for i, it := range items {
		if it.IsHeader {
			continue
		}
		if cursor < 0 {
			cursor = i
		}
		if defSet[it.Value] {
			sel[i] = true
		}
	}
	if cursor < 0 {
		cursor = 0
	}
	return chooseModel{
		title:    title,
		items:    items,
		multi:    multi,
		cursor:   cursor,
		selected: sel,
	}
}

func (m chooseModel) Init() tea.Cmd { return nil }

// nextSelectable scans forward (dir=+1) or backward (dir=-1) from `from`,
// returning the index of the next non-header item. Stops at the bounds.
func (m chooseModel) nextSelectable(from, dir int) int {
	i := from + dir
	for i >= 0 && i < len(m.items) {
		if !m.items[i].IsHeader {
			return i
		}
		i += dir
	}
	return from
}

func (m chooseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c", "esc", "q":
		m.cancelled = true
		return m, tea.Quit
	case "down", "j":
		m.cursor = m.nextSelectable(m.cursor, +1)
	case "up", "k":
		m.cursor = m.nextSelectable(m.cursor, -1)
	case " ", "x":
		if m.multi && m.cursor >= 0 && m.cursor < len(m.items) && !m.items[m.cursor].IsHeader {
			m.selected[m.cursor] = !m.selected[m.cursor]
		}
	case "enter":
		if m.multi {
			m.done = true
			return m, tea.Quit
		}
		if m.cursor >= 0 && m.cursor < len(m.items) && !m.items[m.cursor].IsHeader {
			m.selected = map[int]bool{m.cursor: true}
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m chooseModel) View() string {
	var b strings.Builder
	if m.title != "" {
		b.WriteString(titleStyle.Render(m.title))
		b.WriteString("\n")
	}
	for i, c := range m.items {
		indent := strings.Repeat(" ", c.Depth*4)
		if c.IsHeader {
			b.WriteString(indent)
			b.WriteString(headerLineStyle.Render(c.Label))
			b.WriteString("\n")
			continue
		}
		var line strings.Builder
		line.WriteString(indent)
		if i == m.cursor {
			line.WriteString("> ")
		} else {
			line.WriteString("  ")
		}
		if m.multi {
			if m.selected[i] {
				line.WriteString("[x] ")
			} else {
				line.WriteString("[ ] ")
			}
		}
		line.WriteString(c.Label)
		text := line.String()
		if i == m.cursor {
			b.WriteString(cursorLineStyle.Render(text))
		} else {
			b.WriteString(text)
		}
		b.WriteString("\n")
	}
	if m.multi {
		b.WriteString(helpStyle.Render("space: toggle  ·  enter: confirm  ·  esc: cancel"))
	} else {
		b.WriteString(helpStyle.Render("enter: select  ·  esc: cancel"))
	}
	return b.String()
}

func (m chooseModel) result() []string {
	if !m.multi {
		for i, ok := range m.selected {
			if ok {
				return []string{m.items[i].Value}
			}
		}
		return nil
	}
	out := make([]string, 0, len(m.selected))
	for i := range m.items {
		if m.selected[i] && !m.items[i].IsHeader {
			out = append(out, m.items[i].Value)
		}
	}
	return out
}

func chooseWithHeaders(title string, items []Choice, multi bool, defaults []string) ([]string, error) {
	m := newChooseModel(title, items, multi, defaults)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	fm, ok := final.(chooseModel)
	if !ok {
		return nil, fmt.Errorf("choose: unexpected model type")
	}
	if fm.cancelled {
		return nil, fmt.Errorf("cancelled")
	}
	return fm.result(), nil
}
