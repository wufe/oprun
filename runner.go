package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var varRE = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// truthy decides whether a `when:` expression (after substitution) lets the
// node run. Falsy values mirror common shell/yaml conventions.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "no", "false", "0", "off":
		return false
	}
	return true
}

type gotoSignal struct{ id string }

func (g *gotoSignal) Error() string { return "goto " + g.id }

type Runner struct {
	flow      *Flow
	vars      map[string]any // string or []string
	state     *State
	statePath string
	prompt    Prompt
	shell     string
}

func NewRunner(f *Flow) *Runner {
	shell := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shell = "sh"
	}

	sp, _ := statePath(f.Name)
	s := loadState(sp)

	r := &Runner{
		flow:      f,
		vars:      map[string]any{},
		state:     s,
		statePath: sp,
		shell:     shell,
	}
	// seed vars from persisted state so {var} substitution and foreach see them
	for k, v := range s.StringVars {
		r.vars[k] = v
	}
	for k, v := range s.ListVars {
		r.vars[k] = v
	}
	return r
}

func (r *Runner) Run() error {
	defer r.persist()

	for _, v := range r.flow.Vars {
		p := v.Prompt
		if p == "" {
			p = v.Name
		}
		def := v.Default
		if saved, ok := r.state.StringVars[v.Name]; ok && saved != "" {
			def = saved
		}
		val, err := r.prompt.Input(p, def)
		if err != nil {
			return err
		}
		r.vars[v.Name] = val
	}

	i := 0
	for i < len(r.flow.Nodes) {
		err := r.runNode(&r.flow.Nodes[i])
		var g *gotoSignal
		if errors.As(err, &g) {
			idx, ok := r.findTopLevel(g.id)
			if !ok {
				return fmt.Errorf("goto: unknown id %q", g.id)
			}
			i = idx
			continue
		}
		if err != nil {
			return err
		}
		i++
	}
	return nil
}

// persist snapshots r.vars into state.StringVars/ListVars (by runtime type) and writes to disk.
// Runs on every Run() exit, including errors, so a partial run still seeds the next run's defaults.
func (r *Runner) persist() {
	for k, v := range r.vars {
		switch vv := v.(type) {
		case string:
			r.state.StringVars[k] = vv
			delete(r.state.ListVars, k)
		case []string:
			r.state.ListVars[k] = vv
			delete(r.state.StringVars, k)
		}
	}
	if r.statePath == "" {
		return
	}
	if err := saveState(r.statePath, r.state); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save state to %s: %v\n", r.statePath, err)
	}
}

func (r *Runner) findTopLevel(id string) (int, bool) {
	for i, n := range r.flow.Nodes {
		if n.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (r *Runner) runSeq(nodes []Node) error {
	for i := range nodes {
		if err := r.runNode(&nodes[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runNode(n *Node) error {
	if n.When != "" {
		v, err := r.subst(n.When)
		if err != nil {
			return err
		}
		if !truthy(v) {
			return nil
		}
	}
	switch n.Type {
	case "exec":
		return r.runExec(n)
	case "confirm":
		q, err := r.subst(n.Prompt)
		if err != nil {
			return err
		}
		def := false
		if n.ID != "" {
			if v, ok := r.state.Confirms[n.ID]; ok {
				def = v
			}
		}
		ok, err := r.prompt.Confirm(q, def)
		if err != nil {
			return err
		}
		if n.ID != "" {
			r.state.Confirms[n.ID] = ok
		}
		if ok {
			return r.runSeq(n.OnYes)
		}
		return r.runSeq(n.OnNo)
	case "choose":
		return r.runChoose(n)
	case "input":
		q, err := r.subst(n.Prompt)
		if err != nil {
			return err
		}
		if q == "" {
			q = n.Store
		}
		if n.Store == "" {
			return fmt.Errorf("input node missing 'store'")
		}
		def := ""
		if v, ok := r.state.StringVars[n.Store]; ok {
			def = v
		}
		val, err := r.prompt.Input(q, def)
		if err != nil {
			return err
		}
		r.vars[n.Store] = val
		return nil
	case "foreach":
		list, err := r.toList(n.Var)
		if err != nil {
			return err
		}
		as := n.As
		if as == "" {
			as = n.Var
		}
		prev, had := r.vars[as]
		defer func() {
			if had {
				r.vars[as] = prev
			} else {
				delete(r.vars, as)
			}
		}()
		for _, item := range list {
			r.vars[as] = item
			if err := r.runSeq(n.Do); err != nil {
				return err
			}
		}
		return nil
	case "goto":
		if n.Goto == "" {
			return fmt.Errorf("goto node missing target id")
		}
		return &gotoSignal{id: n.Goto}
	case "":
		return fmt.Errorf("node missing type")
	default:
		return fmt.Errorf("unknown node type %q", n.Type)
	}
}

func (r *Runner) toList(name string) ([]string, error) {
	v, ok := r.vars[name]
	if !ok {
		return nil, fmt.Errorf("foreach: var %q not set", name)
	}
	switch v := v.(type) {
	case []string:
		return v, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return []string{v}, nil
	}
	return nil, fmt.Errorf("foreach: var %q has unsupported type", name)
}

func (r *Runner) runChoose(n *Node) error {
	header, err := r.subst(n.Prompt)
	if err != nil {
		return err
	}

	var (
		choices []Choice
		dynamic bool
	)
	if n.OptionsCmd != "" {
		dynamic = true
		cmdStr, err := r.subst(n.OptionsCmd)
		if err != nil {
			return err
		}
		out, err := r.shellCapture(cmdStr, "")
		if err != nil {
			return fmt.Errorf("options_cmd failed: %w", err)
		}
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if line == "" {
				continue
			}
			label, value := line, line
			if idx := strings.IndexByte(line, '\t'); idx >= 0 {
				label, value = line[:idx], line[idx+1:]
			}
			choices = append(choices, Choice{Label: label, Value: value})
		}
		if len(choices) == 0 {
			return fmt.Errorf("options_cmd produced no options")
		}
	} else {
		for _, o := range n.Options {
			choices = append(choices, Choice{Label: o.Label, Value: o.Label})
		}
	}

	// gather saved defaults, then filter to values still present in choices
	var defaults []string
	if dynamic {
		if n.Store != "" {
			if v, ok := r.state.ListVars[n.Store]; ok {
				defaults = v
			} else if v, ok := r.state.StringVars[n.Store]; ok && v != "" {
				defaults = []string{v}
			}
		}
	} else if n.ID != "" {
		if v, ok := r.state.Choices[n.ID]; ok {
			defaults = v
		}
	}
	if len(defaults) > 0 {
		valid := make(map[string]struct{}, len(choices))
		for _, c := range choices {
			valid[c.Value] = struct{}{}
		}
		filtered := defaults[:0]
		for _, d := range defaults {
			if _, ok := valid[d]; ok {
				filtered = append(filtered, d)
			}
		}
		defaults = filtered
	}

	sel, err := r.prompt.Choose(header, choices, n.Multi, defaults)
	if err != nil {
		return err
	}
	if len(sel) == 0 {
		return fmt.Errorf("no selection made")
	}

	if dynamic {
		if n.Store != "" {
			if n.Multi {
				r.vars[n.Store] = sel
			} else {
				r.vars[n.Store] = sel[0]
			}
		}
		return nil
	}

	if n.ID != "" {
		r.state.Choices[n.ID] = sel
	}

	for _, s := range sel {
		var matched *Option
		for i := range n.Options {
			if n.Options[i].Label == s {
				matched = &n.Options[i]
				break
			}
		}
		if matched == nil {
			return fmt.Errorf("unknown choice %q", s)
		}
		if matched.Goto != "" {
			return &gotoSignal{id: matched.Goto}
		}
		if err := r.runSeq(matched.Do); err != nil {
			return err
		}
	}
	if n.Store != "" {
		if n.Multi {
			r.vars[n.Store] = sel
		} else {
			r.vars[n.Store] = sel[0]
		}
	}
	return nil
}

func (r *Runner) runExec(n *Node) error {
	cmdStr, err := r.subst(n.Run)
	if err != nil {
		return err
	}
	dir, err := r.subst(n.Dir)
	if err != nil {
		return err
	}

	fmt.Printf("\n$ %s\n", cmdStr)
	if dir != "" {
		fmt.Printf("  (cwd: %s)\n", dir)
	}

	cmd := exec.Command(r.shell, "-c", cmdStr)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	if n.Capture != "" {
		var buf bytes.Buffer
		cmd.Stdout = io.MultiWriter(&buf, os.Stdout)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("exec failed: %w", err)
		}
		val := strings.TrimSpace(buf.String())
		r.vars[n.Capture] = val
		fmt.Printf("  [captured %s = %q]\n", n.Capture, val)
	} else {
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("exec failed: %w", err)
		}
	}
	return nil
}

func (r *Runner) shellCapture(cmdStr, dir string) (string, error) {
	cmd := exec.Command(r.shell, "-c", cmdStr)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stderr = os.Stderr
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (r *Runner) subst(s string) (string, error) {
	if s == "" {
		return s, nil
	}
	var substErr error
	out := varRE.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := r.vars[name]; ok {
			switch v := v.(type) {
			case string:
				return v
			case []string:
				return strings.Join(v, " ")
			}
		}
		val, err := r.prompt.Input(name, "")
		if err != nil {
			substErr = err
			return m
		}
		r.vars[name] = val
		return val
	})
	return out, substErr
}
