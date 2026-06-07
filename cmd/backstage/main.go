// Command backstage is a declarative terminal screencast recorder.
//
// Lights, camera... Automation!
//
// This is the scaffold entrypoint. Verbs (setup/rehearse/play/kill) are wired
// onto cobra in a later task; for now it only answers --version so the build
// and toolchain can be verified.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/This-Is-NPC/backstage/internal/cli"
	"github.com/This-Is-NPC/backstage/internal/prompter"
)

// version is the build version. Overridable via -ldflags in releases.
var version = "0.0.0-dev"

func main() {
	// __type is an internal mode: the popup terminal re-execs the binary to type
	// a file char-by-char, so the typewriter needs no external interpreter.
	//   backstage __type <file> <cps>
	if len(os.Args) >= 3 && os.Args[1] == "__type" {
		cps := 32.0
		var err error
		if len(os.Args) >= 4 {
			cps, err = strconv.ParseFloat(os.Args[3], 64)
		}
		if err != nil || cps <= 0 {
			cps = 32
		}
		if err := prompter.RunType(os.Args[2], cps); err != nil {
			fmt.Fprintln(os.Stderr, "backstage __type:", err)
			os.Exit(1)
		}
		return
	}
	if err := cli.Execute(version); err != nil {
		os.Exit(1)
	}
}
