# json-ping on MikroTik RouterOS

Run json-ping on the router itself as a RouterOS `/app`: the probes leave from the
router, so the graph shows each uplink as the router sees it.

## Requirements

- RouterOS **7.22** or newer (custom `/app` YAML appeared in 7.22)
- **arm64 or x86** hardware (RB5009, hAP ax², hAP ax³, CCR2004/2116/2216, CHR…);
  arm32 and MIPS models cannot run containers
- The `container` package installed and container mode enabled — this needs
  physical access once:

  ```
  /system/device-mode/update container=yes
  # then press the reset button or power-cycle the router when asked
  ```

- Storage for images and data. On devices with small flash, use a USB/NVMe disk.

## Install

**Before enabling, change the password.** The app ships with login on
(`admin` / `admin`) because RouterOS publishes the web port through dst-nat.

1. Download [`json-ping.tikapp.yaml`](json-ping.tikapp.yaml), change `passwd=admin`
   in the `command:` line, and upload it to the router (Files).
2. Add it:

   ```
   /app/add yaml=[/file/get json-ping.tikapp.yaml contents]
   ```

3. Enable **json-ping** in WinBox/WebFig → **Apps**. The web UI link appears there
   once the container is up (port 8517).

Quick trial with the default password (lab routers only):

```
/app/add yaml-url=https://raw.githubusercontent.com/githubflyideas/json-ping/main/mikrotik/json-ping.tikapp.yaml
```

Or add the whole store, which lists json-ping in the Apps catalog:

```
/app/settings/set app-store-urls=https://raw.githubusercontent.com/githubflyideas/json-ping/main/mikrotik/json-ping.tikappstore.yaml
```

## Edit targets

The first start creates `targets/ping.list` with one demo target. The image
includes busybox, so edit it from a container shell:

```
/container/print                 # find the json-ping entry number
/container/shell 0               # use that number
vi targets/ping.list             # save: changes apply within 3 seconds
```

Format, one target per line:

```
8.8.8.8          google-dns
1.1.1.1          cloudflare   pace=fast
```

TCP targets go in `targets/tcp.list` as `host:port [name]`.

Data lives in the app's `data` volume (`<disk>/apps/json-ping/data/`) and survives
restarts and upgrades. `/app cleanup json-ping` deletes it permanently.

## Notes

- The container runs as root (`user: "0:0"`) so it can write the volume RouterOS
  creates and fall back to a raw ICMP socket when unprivileged ICMP is not allowed.
- The image tag is pinned to `3.1`, which follows 3.1.x patch releases.
