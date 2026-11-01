# Coverage of the official client

**Tried** means run on a real Steam Deck. Nothing has been tried on a real Steam Frame.

| Official feature | In sdev | Tried |
|---|---|---|
| Device discovery | `devices` | No |
| Pairing | `pair` | Yes |
| Upload a title, with deletes, newer files kept, checksums, filters, AppID, runtime | `deploy` flags | Upload, deletes and runtime settings: yes. Other flags: no |
| Launch a title | `run`, `deploy --run` | Yes |
| Environment variables in the start command | Export them in the launch script | Steam on Deck ignored them when tried |
| List and delete titles | [titles.md](titles.md) | Yes, except delete all |
| Controller configuration | [titles.md](titles.md) | By AppID: yes |
| Screenshot, four modes | `shot --mode` | Default mode: yes |
| Status | [device.md](device.md) | Yes |
| Restart Steam | [device.md](device.md) | Yes |
| Switch session | [device.md](device.md) | No |
| Choose the Steam client | [device.md](device.md) | No |
| Launch flags | [device.md](device.md) | Yes |
| CEF console | [device.md](device.md) | The setting: yes. The console did not answer at port 8081 about 20 seconds after restarting |
| Remote shell | `shell` | With a command: yes. Terminal: no |
| Set password | [device.md](device.md) | No |
| Browse files | `pull`, `push`, FileZilla | `pull`: yes. `push`: no |
| Reset Steam | [device.md](device.md) | No, on purpose |
| Sync logs | [logs.md](logs.md) | Steam logs and crash dumps: yes |
| Proton logging | [logs.md](logs.md) | The flag: yes |
| Performance overlay | [performance.md](performance.md) | The commands ran, with no game open |
| Frame time capture | [performance.md](performance.md) | The commands ran, with no game open, so no CSV |
| GPUVis trace | [performance.md](performance.md) | Yes |
| Radeon GPU Profiler | [performance.md](performance.md) | No |
| RenderDoc replay server | [performance.md](performance.md) | Yes |
| RenderDoc capture layer | [performance.md](performance.md) | No |
| Proton remote debugging | [advanced.md](advanced.md) | No |
| Sideloaded Steam client | [advanced.md](advanced.md) | No |

Not reproduced: passing arbitrary rsync arguments, delta transfer within a file, and compression. sdev does not use rsync.
