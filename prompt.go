package main

import (
	"fmt"
	"sort"
	"strconv"
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

// Choose renders a select / multi-select. Multi-select always uses the
// custom Bubble Tea model so we can track and display selection order
// (selected items render `[1]`, `[2]`, … in the order they were toggled on,
// and downstream consumers receive them in that same order). Single-select
// uses the same custom model when headers are present (cursor must skip
// them), and otherwise falls through to huh's default widget.
func (Prompt) Choose(header string, choices []Choice, multi bool, defaults []string) ([]string, error) {
	if multi {
		return chooseTUI(header, choices, multi, defaults)
	}
	for _, c := range choices {
		if c.IsHeader {
			return chooseTUI(header, choices, multi, defaults)
		}
	}

	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(c.Label, c.Value)
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

// --- custom Bubble Tea selector ---
// Used for every multi-select (so selection order can be tracked and shown
// as 1-based numbers next to each choice) and for any single-select that
// contains group headers (so the cursor can skip them and toggles can be
// rejected on them).

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
	title    string
	items    []Choice
	multi    bool
	cursor   int
	selected map[int]int // item index → 1-based selection order (multi); for single-select, the lone selection has order 1
	// orderWidth is the digit width of the largest possible selection order
	// (= number of selectable items). Used so `[ 1]` and `[10]` align in a
	// list that has 10+ options.
	orderWidth int
	done       bool
	cancelled  bool
}

func newChooseModel(title string, items []Choice, multi bool, defaults []string) chooseModel {
	sel := map[int]int{}
	cursor := -1
	selectable := 0
	valueToIdx := make(map[string]int, len(items))
	for i, it := range items {
		if it.IsHeader {
			continue
		}
		selectable++
		if cursor < 0 {
			cursor = i
		}
		// First occurrence wins — static-choose values are unique per
		// option index, dynamic-choose values are user-supplied (caller's
		// problem if they collide).
		if _, dup := valueToIdx[it.Value]; !dup {
			valueToIdx[it.Value] = i
		}
	}
	// Apply defaults in their incoming order so selection order survives
	// across runs. For single-select we honour at most one default and also
	// place the cursor on it so the user lands on their previous choice.
	if multi {
		order := 1
		for _, d := range defaults {
			i, ok := valueToIdx[d]
			if !ok {
				continue
			}
			if _, already := sel[i]; already {
				continue
			}
			sel[i] = order
			order++
		}
	} else if len(defaults) > 0 {
		if i, ok := valueToIdx[defaults[0]]; ok {
			sel[i] = 1
			cursor = i
		}
	}
	if cursor < 0 {
		cursor = 0
	}
	width := max(len(strconv.Itoa(selectable)), 1)
	return chooseModel{
		title:      title,
		items:      items,
		multi:      multi,
		cursor:     cursor,
		selected:   sel,
		orderWidth: width,
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
			if removed, ok := m.selected[m.cursor]; ok {
				// Toggle off: remove and shift later selections down so
				// the displayed numbering stays contiguous.
				delete(m.selected, m.cursor)
				for k, ord := range m.selected {
					if ord > removed {
						m.selected[k] = ord - 1
					}
				}
			} else {
				m.selected[m.cursor] = len(m.selected) + 1
			}
		}
	case "enter":
		if m.multi {
			m.done = true
			return m, tea.Quit
		}
		if m.cursor >= 0 && m.cursor < len(m.items) && !m.items[m.cursor].IsHeader {
			m.selected = map[int]int{m.cursor: 1}
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
			if ord, ok := m.selected[i]; ok {
				fmt.Fprintf(&line, "[%*d] ", m.orderWidth, ord)
			} else {
				fmt.Fprintf(&line, "[%*s] ", m.orderWidth, "")
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
		b.WriteString(helpStyle.Render("space: toggle (numbered in selection order)  ·  enter: confirm  ·  esc: cancel"))
	} else {
		b.WriteString(helpStyle.Render("enter: select  ·  esc: cancel"))
	}
	return b.String()
}

func (m chooseModel) result() []string {
	if !m.multi {
		for i := range m.selected {
			return []string{m.items[i].Value}
		}
		return nil
	}
	type entry struct{ idx, ord int }
	entries := make([]entry, 0, len(m.selected))
	for i, ord := range m.selected {
		if m.items[i].IsHeader {
			continue
		}
		entries = append(entries, entry{i, ord})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ord < entries[j].ord
	})
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = m.items[e.idx].Value
	}
	return out
}

func chooseTUI(title string, items []Choice, multi bool, defaults []string) ([]string, error) {
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
