---
name: sdev
description: Sends a build to a Steam Deck or Steam Frame in developer mode, registers it in Steam, launches it, and captures the device's screen, using the sdev CLI. Use when deploying, installing, sideloading or running a game on a Steam Deck or Steam Frame, or when checking what is on its screen.
---

# sdev

If `sdev` is not on PATH, install it with `go install github.com/neguse/sdev@latest`. Run `sdev` with no arguments for usage.

```sh
sdev deploy <host> --dir DIR --gameid ID [--kill NAME] [--run] -- COMMAND [ARG...]
sdev shot <host> FILE
```

Take the host, game ID, build directory and launch command from the project's docs or build files. If they are not recorded, ask for the host, and record what you learn in the project's docs.

## Before running

- `--dir` is mirrored: files on the device that are not in it are deleted, so saves must not live in the game's directory.
- `--kill` and `--run` interrupt whatever the person is doing on the device. Take a `shot` first unless they asked for it or are waiting on it.
- The first connection asks for approval on the device. Tell the person before you run it.

## Reading the results

- "no devkit service": the device is probably asleep. Ask the person to wake it, then retry.
- A `403` while pairing means the approval did not arrive in time. Ask the person to watch for the prompt, and run again.
- `--run` only reports that Steam took the request. To confirm the game started, look at a `shot` or at its process over SSH.
- On Steam Frame, `shot` needs VR to be active.

## Steam Frame

ARM64 titles need the runtime set: `--set steam_play=0 --set compat_tool=SteamLinuxRuntime_4-arm64`. Settings persist on the device between deploys.
