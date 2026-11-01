# sdev

An unofficial command line alternative to the SteamOS Devkit Client, for Steam Deck and Steam Frame.

- **Compatible.** It uses the official client's key and device tools, so a device works with both.
- **Easy to install.** One binary, with no `ssh`, `rsync` or Python needed on the host.
- **Agent-native.** No GUI and no prompts on the host, plain exit codes, and a Claude Code plugin that teaches an agent to use it.

Not affiliated with or endorsed by Valve. Steam and related marks are trademarks of Valve Corporation.

## Install

```sh
go install github.com/neguse/sdev@latest
```

For Claude Code, the repository is also a plugin with a skill that drives the CLI:

```
/plugin install sdev --marketplace neguse/sdev
```

## Usage

```sh
# Send dist/game, add it to Steam, stop the previous run and launch.
sdev deploy 192.168.1.20 --dir dist/game --gameid game --kill game --run -- run.sh

# ARM64 title on Steam Frame.
sdev deploy 192.168.1.21 --dir dist/game --gameid game \
  --set steam_play=0 --set compat_tool=SteamLinuxRuntime_4-arm64 -- run.sh

# Save the current screen. On Steam Frame, VR must be active.
sdev shot 192.168.1.20 out.png

# Device and Steam state, as JSON.
sdev status 192.168.1.20

# Anything else runs on the device, and its exit code comes back.
sdev shell 192.168.1.20 'pgrep -x game'
```

`sdev help` lists every command. For work that reaches outside the CLI, the [skill](skills/sdev/SKILL.md) tells an agent what to do.

## Supported

Confirmed working on a real Steam Deck: `pair`, `deploy`, `run`, `ls`, `rm`, `status`, `shot`, `logs`, `shell`, `pull`, `push`, `perf overlay`, `gpu-trace`, `renderdoc-server`, `flags`, `controller --appid`, `restart`, `session`, `client`.

The first connection to a device asks for approval on the device. The key is the one the official SteamOS Devkit Client uses, so a device paired with either tool works with both.

Game IDs follow the official client's rule, `^[A-Za-z_][A-Za-z0-9_.]+$`, and may not be a name it reserves for sideloaded Steam clients. Words after `--` reach the game as given; there is no shell expansion. `--run` reports that Steam took the request, not that the game started. To set environment variables, export them in the launch script: Steam on Steam Deck did not pass on ones set through the devkit tools.

`deploy` deletes files on the device that are not in `--dir`. Files with the same size and mtime are skipped. Every file is sent as executable, like the official client does.

## Third-party code

`devkit-utils/` is copied unchanged from [SteamOS Devkit Client](https://gitlab.steamos.cloud/devkit/steamos-devkit) (`client/devkit-utils`, commit `e091c17`), MIT licensed: see [devkit-utils/LICENSE](devkit-utils/LICENSE). sdev embeds it and installs it on the device when it needs it. `sdev licenses` prints the licenses of everything in the binary.
