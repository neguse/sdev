// Command sdev sends builds to SteamOS devices in developer mode.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const usage = `usage: sdev COMMAND ...

  devices [--timeout D]             Find devices on this network, as JSON
  pair <host>                       Pair with a device and install its tools;
                                    asks for approval on the device
  deploy <host> --dir DIR --gameid ID [options] -- COMMAND [ARG...]
                                    Send DIR, deleting extra files there, and add
                                    it to Steam. COMMAND is relative to DIR, and
                                    each word reaches the game as given.
      --set K=V       title setting (repeatable), for example --set steam_play=0
                      --set compat_tool=SteamLinuxRuntime_4-arm64
      --appid N       write steam_appid.txt with this AppID
      --exclude GLOB  skip matching files and directories (repeatable); on the
                      device they are deleted unless --no-delete is given
      --no-delete     keep extra files on the device
      --update        keep files that are newer on the device
      --checksum      compare by content instead of size and time
      --kill NAME     with --run, stop this process first
      --run           ask Steam to launch it afterwards
  run <host> ID                     Ask Steam to launch a title
  shot <host> FILE [--mode M]       Save the screen as a PNG. M is baseplane,
                                    layers, composition or screen (Steam Deck only)
  shell <host> [COMMAND...]         Run a command and return its exit code, or
                                    open a terminal without one
  pull <host> REMOTE LOCAL [--include GLOB]...
                                    Copy a file, a directory, or what a glob
                                    matches, to the directory LOCAL
  push <host> LOCAL REMOTE          Copy a file or directory's contents into the
                                    directory REMOTE. Relative REMOTE paths start
                                    at home, and a copied file is made executable
`

// exitCodeError carries a remote command's exit code to sdev's own.
type exitCodeError int

func (e exitCodeError) Error() string { return fmt.Sprintf("exit code %d", int(e)) }

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

type list []string

func (l *list) String() string { return "" }

func (l *list) Set(s string) error {
	*l = append(*l, s)
	return nil
}

// parseFlags parses flags that may follow positional arguments, as in
// "shot HOST FILE --mode M", and returns the positional ones.
func parseFlags(f *flag.FlagSet, args []string) ([]string, error) {
	f.SetOutput(io.Discard)
	var words []string
	for {
		if err := f.Parse(args); err != nil {
			return nil, usageError{err}
		}
		args = f.Args()
		if len(args) == 0 {
			return words, nil
		}
		words = append(words, args[0])
		args = args[1:]
	}
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
	var exclude list
	f.StringVar(&c.dir, "dir", "", "")
	f.StringVar(&c.gameid, "gameid", "", "")
	f.Var(&c.settings, "set", "")
	f.StringVar(&c.appid, "appid", "", "")
	f.Var(&exclude, "exclude", "")
	f.BoolVar(&c.keepExtra, "no-delete", false, "")
	f.BoolVar(&c.update, "update", false, "")
	f.BoolVar(&c.checksum, "checksum", false, "")
	f.StringVar(&c.kill, "kill", "", "")
	f.BoolVar(&c.run, "run", false, "")
	if err := f.Parse(args[1:]); err != nil {
		return c, usageError{err}
	}
	c.exclude = exclude
	c.argv = f.Args()
	return c, nil
}

// withDevice connects to the host and hands over the device.
func withDevice(host string, do func(*device) error) error {
	d, err := connect(defaultEndpoint(host), defaultKeyPath())
	if err != nil {
		return err
	}
	defer d.Close()
	return do(d)
}

// positional parses args and checks how many positional words there are.
func positional(name string, args []string, want int, f *flag.FlagSet) ([]string, error) {
	if f == nil {
		f = flag.NewFlagSet(name, flag.ContinueOnError)
	}
	words, err := parseFlags(f, args)
	if err != nil {
		return nil, err
	}
	if want >= 0 && len(words) != want {
		return nil, usageError{fmt.Errorf("%s takes %d arguments, got %d", name, want, len(words))}
	}
	return words, nil
}

func printResult(out string, err error) error {
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(out))
	return nil
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError{fmt.Errorf("no command")}
	}
	cmd, args := args[0], args[1:]
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "help":
		fmt.Print(usage)
		return nil
	case "devices":
		timeout := f.Duration("timeout", 3*time.Second, "")
		if _, err := positional(cmd, args, 0, f); err != nil {
			return err
		}
		found, err := discover(*timeout)
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(found, "", "  ")
		return printResult(string(out), err)
	case "deploy":
		c, err := parseDeploy(args)
		if err != nil {
			return err
		}
		if _, err := c.validate(); err != nil {
			return err
		}
		return withDevice(c.host, func(d *device) error { return d.deploy(c.deployOptions) })
	case "shell":
		// Everything after the host belongs to the remote command.
		if len(args) == 0 {
			return usageError{fmt.Errorf("shell needs a host")}
		}
		return withDevice(args[0], func(d *device) error { return d.shell(args[1:]) })
	case "pull":
		var include list
		f.Var(&include, "include", "")
		w, err := positional(cmd, args, 3, f)
		if err != nil {
			return err
		}
		return withDevice(w[0], func(d *device) error {
			n, err := d.pull(w[1], w[2], include)
			if err == nil {
				fmt.Printf("%d copied\n", n)
			}
			return err
		})
	case "shot":
		mode := f.String("mode", "", "")
		w, err := positional(cmd, args, 2, f)
		if err != nil {
			return err
		}
		return withDevice(w[0], func(d *device) error { return d.shot(w[1], *mode) })
	}

	// The rest take only positional words.
	simple := map[string]struct {
		words int
		do    func(*device, []string) error
	}{
		"pair": {1, func(d *device, _ []string) error {
			fmt.Printf("paired as %s\n", d.user)
			return d.syncTools()
		}},
		"run":  {2, func(d *device, w []string) error { return d.runGame(w[1]) }},
		"push": {3, func(d *device, w []string) error { return d.push(w[1], w[2]) }},
	}
	c, ok := simple[cmd]
	if !ok {
		return usageError{fmt.Errorf("unknown command %q", cmd)}
	}
	w, err := positional(cmd, args, c.words, f)
	if err != nil {
		return err
	}
	return withDevice(w[0], func(d *device) error { return c.do(d, w) })
}

func main() {
	err := run(os.Args[1:])
	var code exitCodeError
	if errors.As(err, &code) {
		os.Exit(int(code))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdev: %v\n", err)
		if errors.As(err, &usageError{}) {
			fmt.Fprint(os.Stderr, "\n"+usage)
		}
		os.Exit(1)
	}
}
