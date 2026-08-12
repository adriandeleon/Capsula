// Command capsula is a terminal UI for managing ~/.ssh/config.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adriandeleon/Capsula/internal/sshconf"
	"github.com/adriandeleon/Capsula/internal/ui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "", "path to the ssh config file (default ~/.ssh/config)")
		sshBin      = flag.String("ssh", "ssh", "ssh binary to use for resolving effective configuration")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("capsula", version)
		return
	}

	path := *configPath
	if path == "" {
		p, err := sshconf.DefaultPath()
		if err != nil {
			fatal("could not determine home directory: %v", err)
		}
		path = p
	}

	// A parse failure is shown inside the UI rather than killing the process:
	// the user is more likely to want to see which file failed than a bare
	// error on a closed terminal.
	set, loadErr := sshconf.Load(path)
	if set == nil {
		set = &sshconf.Set{}
	}

	p := tea.NewProgram(
		ui.New(set, path, *sshBin, loadErr),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "capsula: "+format+"\n", args...)
	os.Exit(1)
}
