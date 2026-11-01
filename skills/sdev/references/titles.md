# Titles

```sh
sdev shell <host> 'python3 ~/devkit-utils/steamos-list-games'
```

## Running as another AppID

`deploy --appid N` makes the title run as that app, and Steam refuses unless it is installed. For the Steamworks sample app 480, which the store does not list, start the install and have the person follow the prompt on the device:

```sh
sdev shell <host> 'steam steam://install/480'
```

## Deleting

`steamos-delete` puts the name into a shell command, so use only names `sdev` accepts as game IDs. Its leftover settings files need removing too.

```sh
sdev shell <host> 'python3 ~/devkit-utils/steamos-delete --delete-title ID; cd ~/devkit-game && rm -f ID-argv.json ID-env.json ID-settings.json'
```

`--delete-all-titles` removes every title. Confirm with the person first.

## Controller configuration

```sh
sdev shell <host> 'rm -f /tmp/config_*.vdf; python3 ~/devkit-utils/steamos-dump-controller-config --appid APPID'
sdev pull <host> '/tmp/config_*.vdf' OUT
```

The script prints JSON and exits 0 either way, so check for `error`. `--gameid ID` also exists, but it refused a registered devkit title on Steam Deck.
