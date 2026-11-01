# Debugging a Proton title from Visual Studio

Needs a Windows machine with Visual Studio and its Remote Debugger, plus `client/devkit-msvsmon` from the [official client](https://gitlab.steamos.cloud/devkit/steamos-devkit). `YEAR` is 2017, 2019 or 2022, and `VS` is the Visual Studio install directory from `vswhere -latest -property installationPath`.

```sh
sdev push <host> devkit-msvsmon devkit-msvsmon
sdev push <host> "VS/Common7/IDE/Remote Debugger" devkit-msvsmon/msvsmonYEAR
# A Proton bug needs these too.
sdev push <host> C:/Windows/SysWOW64/webservices.dll devkit-msvsmon/msvsmonYEAR/x86
sdev push <host> C:/Windows/System32/webservices.dll devkit-msvsmon/msvsmonYEAR/x64
sdev shell <host> 'python3 ~/devkit-msvsmon/msvsmoninstall.py'
sdev deploy <host> --dir DIR --gameid ID --set steam_play=1 --set steam_play_debug=1 --set steam_play_debug_version=YEAR -- game.exe
```

`steam_play_debug=2` waits for the debugger to attach. That mode also needs `ChildProcessDebuggerBackend.dll` from the Child Process Debugging Power Tool extension, in `devkit-msvsmon/msvsmonYEAR/x86` or `x64`.
