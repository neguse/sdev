// Command sdev sends builds to SteamOS devices in developer mode.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = `usage:
  sdev deploy <host> --dir DIR --gameid ID [options] -- COMMAND [ARG...]
      Send DIR to the device, deleting extra files there, and add it to Steam.
      COMMAND is relative to DIR.
        --env K=V    environment variable (repeatable)
        --set K=V    title setting (repeatable), for example
                     --set steam_play=0 --set compat_tool=SteamLinuxRuntime_4-arm64
        --kill NAME  with --run, stop this process first
        --run        launch it afterwards
  sdev shot <host> FILE
      Save the device's current screen as a PNG.
`

// usageError is a mistake in the command line, as opposed to a failed operation.
type usageError struct{ error }

func (m *kv) String() string { return "" }

func (m *kv) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return fmt.Errorf("%q is not K=V", s)
	}
	if *m == nil {
		*m = kv{}
	}
	(*m)[k] = v
	return nil
}

type deployCommand struct {
	host string
	deployOptions
}

func parseDeploy(args []string) (deployCommand, error) {
	var c deployCommand
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return c, usageError{fmt.Errorf("deploy needs a host first")}
	}
	c.host = args[0]
	f := flag.NewFlagSet("deploy", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&c.dir, "dir", "", "")
	f.StringVar(&c.gameid, "gameid", "", "")
	f.Var(&c.env, "env", "")
	f.Var(&c.settings, "set", "")
	f.StringVar(&c.kill, "kill", "", "")
	f.BoolVar(&c.run, "run", false, "")
	if err := f.Parse(args[1:]); err != nil {
		return c, usageError{err}
	}
	c.argv = f.Args()
	return c, nil
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError{fmt.Errorf("no command")}
	}
	switch args[0] {
	case "deploy":
		c, err := parseDeploy(args[1:])
		if err != nil {
			return err
		}
		if _, err := c.validate(); err != nil {
			return err
		}
		d, err := connect(defaultEndpoint(c.host), defaultKeyPath())
		if err != nil {
			return err
		}
		defer d.Close()
		return d.deploy(c.deployOptions)
	case "shot":
		if len(args) != 3 {
			return usageError{fmt.Errorf("shot needs a host and a file")}
		}
		d, err := connect(defaultEndpoint(args[1]), defaultKeyPath())
		if err != nil {
			return err
		}
		defer d.Close()
		return d.shot(args[2])
	}
	return usageError{fmt.Errorf("unknown command %q", args[0])}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "sdev: %v\n", err)
		if errors.As(err, &usageError{}) {
			fmt.Fprint(os.Stderr, "\n"+usage)
		}
		os.Exit(1)
	}
}
