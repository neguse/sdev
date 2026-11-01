// Command sdev sends builds to SteamOS devices in developer mode.
package main

import (
	_ "embed"
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

Finding and pairing
  devices [--timeout D]             Find devices on this network, as JSON
  history                           Devices connected to before, newest first, and
                                    whether each answers now, as JSON
  pair <host>                       Pair with a device and install its tools;
                                    asks for approval on the device

Titles
  deploy <host> --dir DIR --gameid ID [options] -- COMMAND [ARG...]
                                    Send DIR, deleting extra files there, and add
                                    it to Steam. COMMAND is relative to DIR, and
                                    each word reaches the game as given.
      --set K=V       title setting (repeatable), for example --set steam_play=0
                      --set compat_tool=SteamLinuxRuntime_4-arm64
      --appid N       run as this AppID, which must be installed
      --exclude GLOB  skip matching files and directories (repeatable); on the
                      device they are deleted unless --no-delete is given
      --no-delete     keep extra files on the device
      --update        keep files that are newer on the device
      --checksum      compare by content instead of size and time
      --prevent-auto-repair
                      stop SteamOS repairing a client that keeps exiting quickly
      --kill NAME     with --run, stop this process first
      --run           ask Steam to launch it afterwards
  run <host> ID                     Ask Steam to launch a title
  ls <host>                         List titles, as JSON
  rm <host> ID | --all              Delete one title or all of them

Inspecting
  status <host>                     Device and Steam state, as JSON
  shot <host> FILE [--mode M]       Save the screen as a PNG. M is baseplane,
                                    layers, composition or screen (Steam Deck only)
  logs <host> DIR                   Download Steam, crash, SteamVR and Proton logs
  shell <host> [COMMAND...]         Run a command and return its exit code, or
                                    open a terminal without one
  pull <host> REMOTE LOCAL [--include GLOB]...
                                    Copy a file, a directory, or what a glob
                                    matches, to the directory LOCAL
  push <host> LOCAL REMOTE          Copy a file or directory's contents into the
                                    directory REMOTE. Relative REMOTE paths start
                                    at home, and a copied file is made executable

Profiling and debugging
  perf <host> overlay on|off        MangoHud performance overlay
  perf <host> start                 Start logging frame times
  perf <host> stop DIR              Stop logging and download the CSVs
  gpu-trace <host> FILE             Capture a system trace for GPUVis
  rgp <host> DIR                    Capture a Radeon GPU Profiler trace
  renderdoc-server <host> on|off    RenderDoc replay server
  flags <host> [--set K=V] [--unset K]
                                    Steam launch flags, for example
                                    ENABLE_VULKAN_RENDERDOC_CAPTURE=1 or PROTON_LOG=1
  cef <host>                        Enable the Steam CEF console and print its URL
  controller <host> --appid N | --gameid ID DIR
                                    Download a title's controller configuration

Device state
  restart <host>                    Restart the Steam session. This and session
                                    wait until Steam has been up for 60 seconds
  session <host> NAME               Switch session, for example gamescope or plasma-x11
  client <host> os|os-dev|sideloaded [--gameid ID] [--args ARGS] [--gdbserver]
                                    Choose the Steam client, then restart the session
  sideload <host> --dir DIR --gameid ID [--no-delete] [--exclude GLOB]... [-- ARG...]
                                    Send a Steam client build. ID is steam,
                                    steamdeckard, steamvr or steamvrdeckard
  passwd <host>                     Change the device's password, interactively
  reset-steam <host> --yes          Delete Steam and ALL its content from the device

  licenses                          Print the licenses of sdev and what it includes
`

// exitCodeError carries a remote command's exit code to sdev's own.
type exitCodeError int

func (e exitCodeError) Error() string { return fmt.Sprintf("exit code %d", int(e)) }

//go:generate go run ./tools/genlicenses
//go:embed LICENSE
var ownLicense string

//go:embed third_party_licenses.txt
var thirdPartyLicenses string

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
	f.BoolVar(&c.preventRepair, "prevent-auto-repair", false, "")
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
	if file, err := historyPath(); err == nil {
		err = recordConnection(file, d.historyEntry())
		if err != nil {
			fmt.Fprintf(os.Stderr, "sdev: recording this device in the history: %v\n", err)
		}
	}
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

func onOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, usageError{fmt.Errorf("%q is not on or off", s)}
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
	case "licenses":
		fmt.Print(ownLicense + "\n" + thirdPartyLicenses)
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
	case "history":
		if _, err := positional(cmd, args, 0, f); err != nil {
			return err
		}
		file, err := historyPath()
		if err != nil {
			return err
		}
		entries, err := readHistory(file, func(address string) bool {
			_, err := loginName(defaultEndpoint(address))
			return err == nil
		})
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(entries, "", "  ")
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
	case "sideload":
		c, err := parseDeploy(args)
		if err != nil {
			return err
		}
		return withDevice(c.host, func(d *device) error { return d.sideload(c.deployOptions) })
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
	case "rm":
		all := f.Bool("all", false, "")
		w, err := positional(cmd, args, -1, f)
		if err != nil {
			return err
		}
		if len(w) != 2 && !(*all && len(w) == 1) {
			return usageError{fmt.Errorf("rm needs a host and a game id, or a host and --all")}
		}
		return withDevice(w[0], func(d *device) error {
			if *all {
				return d.removeAllTitles()
			}
			return d.removeTitle(w[1])
		})
	case "flags":
		var set kv
		var unset list
		f.Var(&set, "set", "")
		f.Var(&unset, "unset", "")
		w, err := positional(cmd, args, 1, f)
		if err != nil {
			return err
		}
		return withDevice(w[0], func(d *device) error { return d.setFlags(set, unset) })
	case "controller":
		appid := f.String("appid", "", "")
		gameid := f.String("gameid", "", "")
		w, err := positional(cmd, args, 2, f)
		if err != nil {
			return err
		}
		return withDevice(w[0], func(d *device) error { return d.controllerConfig(*appid, *gameid, w[1]) })
	case "client":
		gameid := f.String("gameid", "", "")
		cargs := f.String("args", "", "")
		gdb := f.Bool("gdbserver", false, "")
		w, err := positional(cmd, args, 2, f)
		if err != nil {
			return err
		}
		return withDevice(w[0], func(d *device) error { return d.setClient(w[1], *gameid, *cargs, *gdb) })
	case "reset-steam":
		yes := f.Bool("yes", false, "")
		w, err := positional(cmd, args, 1, f)
		if err != nil {
			return err
		}
		if !*yes {
			return usageError{fmt.Errorf("reset-steam deletes Steam and all its content from the device; pass --yes")}
		}
		return withDevice(w[0], func(d *device) error { return d.resetSteam() })
	case "perf":
		w, err := positional(cmd, args, -1, f)
		if err != nil {
			return err
		}
		if len(w) < 2 {
			return usageError{fmt.Errorf("perf needs a host and overlay, start or stop")}
		}
		switch {
		case w[1] == "overlay" && len(w) == 3:
			on, err := onOff(w[2])
			if err != nil {
				return err
			}
			return withDevice(w[0], func(d *device) error { return d.perfOverlay(on) })
		case w[1] == "start" && len(w) == 2:
			return withDevice(w[0], func(d *device) error { return d.perfStart() })
		case w[1] == "stop" && len(w) == 3:
			return withDevice(w[0], func(d *device) error {
				n, err := d.perfStop(w[2])
				if err == nil {
					fmt.Printf("%d copied\n", n)
				}
				return err
			})
		}
		return usageError{fmt.Errorf("unknown perf command %q", strings.Join(w[1:], " "))}
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
		"status":    {1, func(d *device, _ []string) error { return printResult(d.statusJSON()) }},
		"ls":        {1, func(d *device, _ []string) error { return printResult(d.listGames()) }},
		"run":       {2, func(d *device, w []string) error { return d.runGame(w[1]) }},
		"logs":      {2, func(d *device, w []string) error { return d.logs(w[1]) }},
		"push":      {3, func(d *device, w []string) error { return d.push(w[1], w[2]) }},
		"gpu-trace": {2, func(d *device, w []string) error { return d.gpuTrace(w[1]) }},
		"rgp":       {2, func(d *device, w []string) error { return printResult(d.rgpCapture(w[1])) }},
		"renderdoc-server": {2, func(d *device, w []string) error {
			on, err := onOff(w[1])
			if err != nil {
				return err
			}
			return d.renderdocServer(on)
		}},
		"cef":     {1, func(d *device, _ []string) error { return printResult(d.enableCEF()) }},
		"restart": {1, func(d *device, _ []string) error { return d.restart() }},
		"session": {2, func(d *device, w []string) error { return d.setSession(w[1]) }},
		"passwd":  {1, func(d *device, _ []string) error { return d.setPassword() }},
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
