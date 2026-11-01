# Logs

```sh
# Steam: console_log.txt and friends. The directory holds many, so ask for what you need.
sdev pull <host> .local/share/Steam/logs OUT/steam --include 'console_log*.txt'
# Crash dumps.
sdev pull <host> /tmp/dumps OUT/dumps
# Proton, once logging is on (below).
sdev pull <host> 'steam-*.log' OUT/proton
```

SteamVR logs may live elsewhere. `steamvr_logpath` in the status names the directory (see [device.md](device.md)).

## Proton logging

Takes effect at the next launch. The value is `1` or a `WINEDEBUG` channel list.

```sh
sdev shell <host> 'mkdir -p "$XDG_RUNTIME_DIR/steam/env"; rm -f "$XDG_RUNTIME_DIR/steam/env/WINEDEBUG"; echo 1 > "$XDG_RUNTIME_DIR/steam/env/PROTON_LOG"'
sdev shell <host> 'rm -f "$XDG_RUNTIME_DIR/steam/env/PROTON_LOG"'
```
