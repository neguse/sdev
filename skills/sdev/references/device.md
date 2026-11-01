# Device state

## Status

```sh
sdev shell <host> 'python3 ~/devkit-utils/steamos-get-status --json'
```

JSON with `is_deckard` (Steam Frame), `session_status`, `session_options`, `session_select`, `steam_status`, `steam_launch_flags`, `cef_debugging_enabled`, `steamvr_logpath`, `renderdoc_replay_server_running` and more.

## Restarting Steam

This ends any running game. Use `session_select` from the status, which is `holo-session-select` on current Steam Decks.

```sh
sdev shell <host> 'holo-session-select gamescope'                                 # Steam Deck
sdev shell <host> 'systemctl --user unmask steam ; systemctl --user restart steam' # Steam Frame
```

## Switching sessions

Names come from `session_options`. `plasma-x11` and `plasma-wayland` both select `plasma`; the latter first writes the session type.

```sh
sdev shell <host> 'rm -f "${XDG_CONF_DIR:-$HOME/.config}/steamos-session-type"; holo-session-select plasma'     # X11
sdev shell <host> 'echo wayland > "${XDG_CONF_DIR:-$HOME/.config}/steamos-session-type"; holo-session-select plasma'
sdev shell <host> 'holo-session-select gamescope'
```

## Steam launch flags

One file per variable, read by Steam's launch wrapper, which only knows a fixed set. They are in `steam_launch_flags`.

```sh
sdev shell <host> 'mkdir -p "$XDG_RUNTIME_DIR/steam/env"; echo VALUE > "$XDG_RUNTIME_DIR/steam/env/NAME"'
sdev shell <host> 'rm -f "$XDG_RUNTIME_DIR/steam/env/NAME"'
```

## CEF console

The Steam UI's web inspector. Enabling it restarts Steam. The console is at `http://<host>:8081`.

```sh
sdev shell <host> 'touch ~/.steam/steam/.cef-enable-remote-debugging; holo-session-select gamescope'
```

Delete that file and restart Steam to turn it off.

## Choosing the Steam client

`SteamStatus.OS`, `SteamStatus.OS_DEV` or `SteamStatus.SIDELOADED`, then restart Steam. `--args=` sets its command line, and `--gdbserver` runs it under gdbserver.

```sh
sdev shell <host> 'python3 ~/devkit-utils/steamos-set-steam-client --client SteamStatus.OS_DEV --gameid steam'
```

## Password

Interactive, so a person has to do it: `sdev shell <host>`, then `~/devkit-utils/steamos-set-password.sh`.

## Files and shells

`sdev shell <host>` with no command opens a terminal. `pull` and `push` copy files. A person who wants a file browser can use FileZilla with `sftp://<user>@<host>` and the key the CLI uses.

## Reset

This deletes Steam and every game on the device, then reinstalls Steam. Run it only when the person explicitly asks for this, and confirm first.

```sh
sdev shell <host> 'python3 ~/devkit-utils/steamos-delete --reset-steam-client'
```
