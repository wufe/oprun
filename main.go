package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "-h", "--help", "help":
		usage()
	case "list", "ls":
		listFlows()
	case "run":
		if len(os.Args) < 3 {
			die("error: 'run' needs a flow name or path to a yaml file")
		}
		runByName(os.Args[2])
	default:
		runByName(os.Args[1])
	}
}

// flowSearchDirs lists directories searched for flow files, highest priority first.
// Specificity order:
//  1. cwd-local (./.oprun/flows, ./.flows, ./flows)
//  2. monorepo root, when cwd is inside a git repo — same three subdirs under
//     the nearest `.git` ancestor (bounded walk; see findRepoRoot)
//  3. user-global (~/.oprun/flows)
//
// Duplicates are skipped, so when cwd is itself the repo root the (1) entries
// cover (2). Steps (2) and (3) are silently omitted if the repo root or home
// dir cannot be resolved.
func flowSearchDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" {
			return
		}
		c := filepath.Clean(d)
		if seen[c] {
			return
		}
		seen[c] = true
		dirs = append(dirs, c)
	}

	cwd, err := os.Getwd()
	if err == nil {
		add(filepath.Join(cwd, ".oprun", "flows"))
		add(filepath.Join(cwd, ".flows"))
		add(filepath.Join(cwd, "flows"))
		if root, ok := findRepoRoot(cwd); ok {
			add(filepath.Join(root, ".oprun", "flows"))
			add(filepath.Join(root, ".flows"))
			add(filepath.Join(root, "flows"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".oprun", "flows"))
	}
	return dirs
}

// findFlow resolves a flow name to file contents, trying each search dir in order.
func findFlow(name string) ([]byte, string, error) {
	dirs := flowSearchDirs()
	for _, d := range dirs {
		for _, ext := range []string{".yaml", ".yml"} {
			p := filepath.Join(d, name+ext)
			if data, err := os.ReadFile(p); err == nil {
				return data, p, nil
			}
		}
	}
	return nil, "", fmt.Errorf("flow %q not found in: %s", name, strings.Join(dirs, ", "))
}

func runByName(nameOrPath string) {
	var (
		data []byte
		src  string
		err  error
	)
	if strings.HasSuffix(nameOrPath, ".yaml") ||
		strings.HasSuffix(nameOrPath, ".yml") ||
		strings.ContainsRune(nameOrPath, '/') {
		data, err = os.ReadFile(nameOrPath)
		src = nameOrPath
	} else {
		data, src, err = findFlow(nameOrPath)
	}
	if err != nil {
		die(err.Error())
	}

	f, err := LoadFlow(data)
	if err != nil {
		die(err.Error())
	}
	fmt.Printf("running flow %s  [%s]\n", f.Name, src)

	r := NewRunner(f)
	if err := r.Run(); err != nil {
		die(err.Error())
	}
}

type flowEntry struct {
	name   string
	desc   string
	source string
}

func listFlows() {
	seen := map[string]bool{}
	var entries []flowEntry
	for _, d := range flowSearchDirs() {
		files, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, f := range files {
			n := f.Name()
			var base string
			switch {
			case strings.HasSuffix(n, ".yaml"):
				base = strings.TrimSuffix(n, ".yaml")
			case strings.HasSuffix(n, ".yml"):
				base = strings.TrimSuffix(n, ".yml")
			default:
				continue
			}
			if seen[base] {
				continue
			}
			seen[base] = true
			p := filepath.Join(d, n)
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			desc := ""
			if fl, err := LoadFlow(data); err == nil {
				desc = fl.Description
			}
			entries = append(entries, flowEntry{name: base, desc: desc, source: p})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	if len(entries) == 0 {
		fmt.Println("no flows found. Searched:")
		for _, d := range flowSearchDirs() {
			fmt.Printf("  %s\n", d)
		}
		return
	}
	fmt.Println("available flows:")
	for _, e := range entries {
		fmt.Printf("  %-24s %-60s  [%s]\n", e.name, e.desc, e.source)
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func usage() {
	fmt.Println("oprun - configurable task runner")
	fmt.Println()
	fmt.Println("usage:")
	fmt.Println("  oprun list                  list flows discovered in search paths")
	fmt.Println("  oprun run <flow|file>       run a flow by name, or a yaml file by path")
	fmt.Println("  oprun <flow|file>           shorthand for 'run'")
	fmt.Println()
	fmt.Println("flow search order (first match wins):")
	fmt.Println("  ./.oprun/flows/<name>.yaml")
	fmt.Println("  ./.flows/<name>.yaml")
	fmt.Println("  ./flows/<name>.yaml")
	fmt.Println("  <repo-root>/.oprun/flows/<name>.yaml   (when cwd is in a git repo)")
	fmt.Println("  <repo-root>/.flows/<name>.yaml")
	fmt.Println("  <repo-root>/flows/<name>.yaml")
	fmt.Println("  ~/.oprun/flows/<name>.yaml")
}
