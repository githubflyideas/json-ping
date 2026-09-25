> "I like pure JSON, and I enjoy editing configs in vim. But if you want a Web UI, go check out fogping or pingping!"

![window: a 40-minute congestion event — smoke spreads, bursts marked ◆](docs/hero8.png)

A smokeping-like network tool

json-ping is a lightweight network latency and link quality visualization tool.
It may not be as powerful or feature-rich as Smokeping, but it's lightweight.

Just scp and run


```bash
#install (linux-amd64; also linux-arm64, darwin-amd64, darwin-arm64)
mkdir -p /home/json-ping && cd /home/json-ping
wget https://github.com/githubflyideas/json-ping/releases/latest/download/json-ping-linux-amd64.tar.gz
tar -zxvf json-ping-linux-amd64.tar.gz

#run
./json-ping user=admin passwd=admin

Open http://localhost:8517 and watch your first puff of network smoke.

#run in background
nohup ./json-ping user=admin passwd=admin > json-ping.log 2>&1 &
```


### Docker

Image: `githubflyideas/json-ping` (also `ghcr.io/githubflyideas/json-ping`) — amd64 / arm64 / arm/v7.
Inside the container the working directory is `/data`, so `targets/` and `data/` live there.

```bash
mkdir -p ~/json-ping
docker run -d --name json-ping --restart unless-stopped \
  --user $(id -u):$(id -g) -p 8517:8517 \
  -v ~/json-ping:/data githubflyideas/json-ping

# with login: replace the default command (flags first, user=/passwd= last)
docker run -d --name json-ping --user $(id -u):$(id -g) -p 8517:8517 \
  -v ~/json-ping:/data githubflyideas/json-ping \
  --listen 0.0.0.0:8517 user=admin passwd=admin
```

Edit targets on the host (`vi ~/json-ping/targets/ping.list`) or inside the container
(`docker exec -it json-ping vi targets/ping.list`). Saved changes apply within 3 seconds.

`--user $(id -u):$(id -g)` makes the container write as you, so the mounted directory stays writable.

### MikroTik RouterOS

Run it on the router as a RouterOS `/app` (7.22+, arm64/x86): see [mikrotik/](mikrotik/README.md).

-----------------------------------------------------------
Add target host 
```
vi targets/ping.list
vi targets/tcp.list
```
Data cleanup
Retention is fixed at 300 days. To purge earlier by hand, just find+delete):

```

# Data files are plain per-day JSONL under ./data/<target>/YYYY-MM-DD.jsonl,
# so cleanup is just find+delete. Run from the json-ping directory.
days="${1:-30}"
find ./data -type f -name '202[6-9]*.jsonl' -mtime +"$days" -print -delete    
```
Latest [Releases](https://github.com/githubflyideas/json-ping/releases)   



### ✨ Quick Comparison   [json-ping]  VS [fogping] VS [pingping] 
| Feature / Project | **json-ping** 🏷️  | **fogping** 🌫️ <br>*(not json, use sqlite)* | **pingping** 💨 *(v0.3.2)* <br>*(Formerly SmokeTrail)* |
| :--- | :--- | :--- | :--- |
| **Migration / Lineage** | 🔄old Pingping   | ➡️ Replaced JSON with SQLite | 🚀 **Flagship successor** (SmokeTrail ➔ pingping) |
| **Core Concept** | Lightweight SmokePing-like monitor | Lightweight SmokePing-like monitor | **An enhanced successor to fogping** |
| **Binary Type** | Single Binary | Single Binary | Single Binary |
| **Storage Engine** | Plain JSON File | Embedded SQLite | Embedded SQLite (Optimized) |
| **Target Management** | 📝 File-based *(via `vim`/`nano`)* | 🌐 **Web UI** *(Add/Edit targets)* | 🌐 **Web UI** *(Add/Edit targets)* |
| **Platform Compatibility** | Linux Only | Linux Only | 🌐 **6 Architectures** *(Win/Linux/macOS)* |

apache 2.0


## Parameters

`./json-ping --help` prints all of this with copy-ready examples.

```
./json-ping [flags] [user=NAME[,NAME2...] passwd=PASS[,PASS2...]]
```

| Parameter | Default | Meaning |
|---|---|---|
| `--listen host:port` | `0.0.0.0:8517` | Web UI address. `0.0.0.0` = all interfaces; an IP binds one interface only |
| `--localhost` | off | Bind `127.0.0.1` only (this machine only), keeping the `--listen` port |
| `--version` | | Print version and exit |
| `--help` | | Help with examples (also `./json-ping help`) |
| `user=a,b passwd=x,y` | no login | Turn the login page on. Users and passwords pair by position and the counts must match. A login lasts 2 hours; a restart logs everyone out |

Flags come first, `user=` / `passwd=` last:

```
./json-ping                                              # 0.0.0.0:8517, no login
./json-ping --listen 0.0.0.0:9000 user=admin passwd=admin
./json-ping --localhost                                  # 127.0.0.1:8517
nohup ./json-ping user=admin passwd=admin > json-ping.log 2>&1 &   # background; stop with: pkill -x json-ping
```


