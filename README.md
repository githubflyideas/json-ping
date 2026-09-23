

![window: a 40-minute congestion event — smoke spreads, bursts marked ◆](docs/hero8.png)

A smokeping-like network tool

PingPing is a lightweight network latency and link quality visualization tool.
It may not be as powerful or feature-rich as Smokeping, but it's lightweight.

Just scp and run


```bash
#install
mkdir -p /home/pingping && cd /home/pingping
wget https://github.com/githubflyideas/pingping/releases/download/v2.12.0/pingping-v2.12.0-linux-amd64.tar.gz
tar -zxvf pingping-v2.12.0-linux-amd64.tar.gz

#run
./pingping user=admin passwd=admin

Open http://localhost:8517 and watch your first puff of network smoke.

#run in background
nohup ./pingping user=admin passwd=admin > pingping.log 2>&1 &
```


-----------------------------------------------------------
Add target host 
```
 echo "1.2.3.4 myhost pace=fast"    >> targets/ping.list
 echo "10.0.0.5:443 ads-api"        >> targets/tcp.list

or
vi targets/ping.list
vi targets/tcp.list
```
Data cleanup
Retention is fixed at 300 days. To purge earlier by hand, just find+delete):

```

# Data files are plain per-day JSONL under ./data/<target>/YYYY-MM-DD.jsonl,
# so cleanup is just find+delete. Run from the pingping directory.
days="${1:-30}"
find ./data -type f -name '202[6-9]*.jsonl' -mtime +"$days" -print -delete    
```
Latest [Releases](https://github.com/githubflyideas/pingping/releases)   



### ✨ Quick Comparison    [PINGPING] VS [FOGPING] VS [SmokeTrail]
<img width="1410" height="731" alt="image" src="https://github.com/user-attachments/assets/7e7a4018-5eef-41b7-8896-2205136da49b" />



apache 2.0


## Parameters

`./pingping --help` prints all of this with copy-ready examples.

```
./pingping [flags] [user=NAME[,NAME2...] passwd=PASS[,PASS2...]]
```

| Parameter | Default | Meaning |
|---|---|---|
| `--listen host:port` | `0.0.0.0:8517` | Web UI address. `0.0.0.0` = all interfaces; an IP binds one interface only |
| `--localhost` | off | Bind `127.0.0.1` only (this machine only), keeping the `--listen` port |
| `--version` | | Print version and exit |
| `--help` | | Help with examples (also `./pingping help`) |
| `user=a,b passwd=x,y` | no login | Turn the login page on. Users and passwords pair by position and the counts must match. A login lasts 2 hours; a restart logs everyone out |

Flags come first, `user=` / `passwd=` last:

```
./pingping                                              # 0.0.0.0:8517, no login
./pingping --listen 0.0.0.0:9000 user=admin passwd=admin
./pingping --localhost                                  # 127.0.0.1:8517
nohup ./pingping user=admin passwd=admin > pingping.log 2>&1 &   # background; stop with: pkill -x pingping
```

Everything else is fixed: `targets/` and `data/` sit in the directory you start it in (`/home/pingping`), the default pace
probes every 60 s with 20 packets, full samples are kept 30 days and data is deleted after 300 days.
Targets are not parameters — edit `targets/ping.list` / `targets/tcp.list`; changes apply within 3 seconds.

