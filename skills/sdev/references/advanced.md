# Advanced

Written from the official client's source. Neither has been tried with sdev.

## Proton remote debugging

Needs a Windows machine with Visual Studio and its Remote Debugger, plus `client/devkit-msvsmon` from the [official client](https://gitlab.steamos.cloud/devkit/steamos-devkit). `YEAR` is 2017, 2019 or 2022.

```sh
sdev push <host> devkit-msvsmon devkit-msvsmon
sdev push <host> "VS_INSTALL/Common7/IDE/Remote Debugger" devkit-msvsmon/msvsmonYEAR
# A Proton bug needs these too.
sdev push <host> C:/Windows/SysWOW64/webservices.dll devkit-msvsmon/msvsmonYEAR/x86
sdev push <host> C:/Windows/System32/webservices.dll devkit-msvsmon/msvsmonYEAR/x64
sdev shell <host> 'python3 ~/devkit-msvsmon/msvsmoninstall.py'
sdev deploy <host> --dir DIR --gameid ID --set steam_play=1 --set steam_play_debug=1 --set steam_play_debug_version=YEAR -- game.exe
```

`steam_play_debug=2` waits for the debugger to attach. That mode also needs `ChildProcessDebuggerBackend.dll` from the Child Process Debugging Power Tool extension, in `devkit-msvsmon/msvsmonYEAR/x86` or `x64`.

## Sideloaded Steam client

For developing the Steam client itself. `sdev deploy` refuses these IDs, because the client is not a title: there is no shortcut to register.

```sh
sdev shell <host> 'python3 ~/devkit-utils/steamos-prepare-upload --gameid steam'
sdev push <host> CLIENT_BUILD devkit-game/steam
sdev shell <host> 'python3 ~/devkit-utils/steamos-set-steam-client --client SteamStatus.SIDELOADED --gameid steam'
```

Then restart Steam (see [device.md](device.md)). The reserved IDs are `steam`, `steamdeckard`, `steamvr` and `steamvrdeckard`. Unlike the official client, `push` does not delete extra files or filter the upload.
