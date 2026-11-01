# Performance

Start with the overlay or a frame time log. Reach for a trace once you know which frames are slow.

| Question | Tool |
|---|---|
| Is it holding its frame rate? | MangoHud overlay |
| How do frame times behave over a session? | MangoHud frame time log |
| What is the whole system doing in a slow frame? | GPUVis system trace |
| What is the GPU doing in one frame? (Vulkan on AMD) | Radeon GPU Profiler |
| Why does a draw look wrong? | RenderDoc |

## MangoHud

```sh
sdev shell <host> 'mangohudctl set no_display false'   # show; true hides
sdev shell <host> 'mangohudctl set log_session true'   # start logging, with the game running
sdev shell <host> 'mangohudctl set log_session false'  # stop
sdev pull <host> 'mangoapp_*.csv' OUT
```

## GPUVis system trace

Tracing must be on first and needs a few seconds to fill its buffers. Traces are hundreds of megabytes, so delete them from the device's `/tmp`, and stop tracing afterwards.

```sh
sdev shell <host> 'gpu-trace --start; sleep 5'
sdev shell <host> 'gpu-trace --capture --no-gpuvis -o /tmp/sdev-trace.zip'
sdev pull <host> /tmp/sdev-trace.zip OUT
sdev shell <host> 'rm -f /tmp/sdev-trace.zip; gpu-trace --stop'
```

If the person wants to look at it, open it with `gpuvis OUT/sdev-trace.zip`. Steam Frame may need the gpu-trace package installed and a reboot.

## Radeon GPU Profiler

Vulkan titles only, with graphics profiling enabled in the device's developer settings. The game must be running.

```sh
sdev shell <host> 'rm -f /tmp/*.rgp; touch /tmp/rgp.trigger'
sdev shell <host> 'ls -1t /tmp/*.rgp'   # repeat until a file appears, for about 2 seconds
sdev shell <host> 'stat -c %s /tmp/NAME.rgp'   # repeat until the size stops changing
sdev pull <host> /tmp/NAME.rgp OUT
```

If the person wants to look at it, open it with `RadeonGPUProfiler OUT/NAME.rgp`.

## RenderDoc

```sh
# Capture layer, read by Steam at launch. Unset with rm -f.
sdev shell <host> 'mkdir -p "$XDG_RUNTIME_DIR/steam/env"; echo 1 > "$XDG_RUNTIME_DIR/steam/env/ENABLE_VULKAN_RENDERDOC_CAPTURE"'
# Replay server, on and off.
sdev shell <host> 'killall -9 renderdoccmd; RENDERDOC_TEMP=$HOME renderdoccmd remoteserver -d'
sdev shell <host> 'killall -9 renderdoccmd'
```

The desktop side is `qrenderdoc` on the person's machine.
