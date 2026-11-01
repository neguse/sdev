# sdev

An unofficial command line tool that sends builds to SteamOS devkit devices (Steam Deck, Steam Frame) in developer mode. One binary with no dependencies on the host: no `ssh`, `rsync` or Python needed.

Not affiliated with or endorsed by Valve. Steam and related marks are trademarks of Valve Corporation.

## Install

```sh
go install github.com/neguse/sdev@latest
```

## Usage

```sh
# Send dist/game, add it to Steam, stop the previous run and launch.
sdev deploy 192.168.1.20 --dir dist/game --gameid game --kill game --run -- run.sh

# ARM64 title on Steam Frame.
sdev deploy 192.168.1.21 --dir dist/game --gameid game \
  --set steam_play=0 --set compat_tool=SteamLinuxRuntime_4-arm64 -- run.sh

# Save the current screen.
sdev shot 192.168.1.20 out.png
```

The first connection to a device asks for approval on the device. The key is the one the official SteamOS Devkit Client uses, so a device paired with either tool works with both.

Steam on Steam Deck refuses game IDs containing a hyphen, so sdev does too. `--run` reports that Steam took the request, not that the game started. To set environment variables, export them in the launch script.

`deploy` deletes files on the device that are not in `--dir`. Files with the same size and mtime are skipped. On Windows every file is sent as executable, because Windows has no Unix permissions to read.

## Third-party code

`devkit-utils/` is copied unchanged from [SteamOS Devkit Client](https://gitlab.steamos.cloud/devkit/steamos-devkit) (`client/devkit-utils`, commit `e091c17`), MIT licensed: see [devkit-utils/LICENSE](devkit-utils/LICENSE). sdev embeds it and installs it on the device on each deploy.
