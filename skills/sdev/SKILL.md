---
name: sdev
description: Drives a Steam Deck or Steam Frame in developer mode through the sdev CLI, an alternative to the SteamOS Devkit Client. Use when deploying or running a game on one, checking its screen, reading its logs, profiling or debugging a game on it, or changing its Steam or session state.
---

# sdev

If `sdev` is not on PATH, install it with `go install github.com/neguse/sdev@latest`. `sdev help` lists every command, and `sdev shell <host> '...'` runs anything else on the device.

Take the host, game ID, build directory and launch command from the project's docs or build files. If they are not recorded, run `sdev devices` or ask for the host, and record what you learn in the project's docs.

## Before acting

- `deploy` mirrors `--dir`: files on the device that are not in it are deleted, so saves must not live in the game's directory.
- `--kill`, `--run`, `restart`, `session`, `client` and enabling `cef` interrupt whatever the person is doing on the device. Take a `shot` first unless they asked for it or are waiting on it.
- Use `sdev restart` and `sdev session`, not their underlying commands. SteamOS treats three Steam sessions under 60 seconds as a broken client and repairs it, moving `~/.steam` aside and dropping the devkit client setting. These commands wait that window out.
- `reset-steam` and `rm --all` delete things that cannot be restored. Run them only when the person asks, and confirm first.
- The first connection asks for approval on the device. Tell the person before you run it.

## Choosing a profiling tool

| Question | Tool |
|---|---|
| Is it holding its frame rate? | `perf overlay on` |
| How do frame times behave over a session? | `perf start`, `perf stop DIR`, with the game running |
| What is the whole system doing in a slow frame? | `gpu-trace FILE`, then `gpuvis FILE` on the person's machine |
| What is the GPU doing in one frame? | `rgp DIR`, then `RadeonGPUProfiler` on the person's machine. Vulkan only, with graphics profiling enabled in the device's developer settings |
| Why does a draw look wrong? | `flags --set ENABLE_VULKAN_RENDERDOC_CAPTURE=1`, `renderdoc-server on`, then `qrenderdoc` on the person's machine |

Traces are hundreds of megabytes. `gpu-trace` leaves tracing on; `sdev shell <host> 'gpu-trace --stop'` turns it off.

## Reading the results

- "no devkit service": the device is probably asleep. Ask the person to wake it, then retry.
- A `403` while pairing means the approval did not arrive in time. Ask the person to watch for the prompt, and run again.
- `--run` and `run` only report that Steam took the request. To confirm the game started, look at a `shot` or find its process with `pgrep -x`. `pgrep -f` also matches the shell running your command.
- In `pull`, a glob such as `'/tmp/config_*.vdf'` looks at one directory. `--include` walks the whole tree, so do not use it on the home or `/tmp`: they are large and partly unreadable.
- `controller --gameid` refused a registered devkit title on Steam Deck. Use `--appid`.
- `deploy --appid N` needs that app installed. For the Steamworks sample app 480, which the store does not list, run `sdev shell <host> 'steam steam://install/480'` and have the person follow the prompt on the device.

## Steam Frame

ARM64 titles need the runtime set: `--set steam_play=0 --set compat_tool=SteamLinuxRuntime_4-arm64`. Settings persist on the device between deploys. `shot` needs VR to be active.

## Working with other tools

Debugging a Proton title from Visual Studio: [references/proton-debugging.md](references/proton-debugging.md).
