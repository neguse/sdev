---
name: sdev
description: Drives a Steam Deck or Steam Frame in developer mode through the sdev CLI, covering what the SteamOS Devkit Client does. Use when deploying or running a game on one, checking its screen, reading its logs, profiling or debugging a game on it, or changing its Steam or session state.
---

# sdev

If `sdev` is not on PATH, install it with `go install github.com/neguse/sdev@latest`. `sdev help` lists the commands.

Take the host, game ID, build directory and launch command from the project's docs or build files. If they are not recorded, run `sdev devices` or ask for the host, and record what you learn in the project's docs.

The CLI covers connecting, deploying, launching, screenshots and copying files. Everything else is a command on the device, run with `sdev shell <host> '...'`. Read the guide for the task:

| Task | Guide |
|---|---|
| Frame rates, GPU traces, RGP, RenderDoc | [references/performance.md](references/performance.md) |
| Steam, crash, SteamVR and Proton logs | [references/logs.md](references/logs.md) |
| Listing and deleting titles, controller configs | [references/titles.md](references/titles.md) |
| Device status, restarting Steam, sessions, launch flags, CEF, passwords, reset | [references/device.md](references/device.md) |
| Proton remote debugging, sideloaded Steam clients | [references/advanced.md](references/advanced.md) |

[references/coverage.md](references/coverage.md) maps each feature of the official client to its command, and says what has been tried on a real device.

## Before acting

- `deploy` mirrors `--dir`: files on the device that are not in it are deleted, so saves must not live in the game's directory.
- `--kill`, `--run`, restarting Steam and switching sessions interrupt whatever the person is doing on the device. Take a `shot` first unless they asked for it or are waiting on it.
- The first connection asks for approval on the device. Tell the person before you run it.
- Device tools live in `~/devkit-utils`. If they are missing, `sdev pair <host>` installs them.

## Reading the results

- "no devkit service": the device is probably asleep. Ask the person to wake it, then retry.
- A `403` while pairing means the approval did not arrive in time. Ask the person to watch for the prompt, and run again.
- `--run` and `run` only report that Steam took the request. To confirm the game started, look at a `shot` or find its process with `pgrep -x`. `pgrep -f` also matches the shell running your command.
- `sdev shell` returns the remote exit code.
- In `pull`, a glob such as `'/tmp/config_*.vdf'` looks at one directory. `--include` walks the whole tree, so do not use it on the home or `/tmp`: they are large and partly unreadable.

## Steam Frame

ARM64 titles need the runtime set: `--set steam_play=0 --set compat_tool=SteamLinuxRuntime_4-arm64`. Settings persist on the device between deploys. `shot` needs VR to be active.
