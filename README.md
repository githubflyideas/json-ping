

![window: a 40-minute congestion event — smoke spreads, bursts marked ◆](docs/hero8.png)

A smokeping-like network tool

PingPing is a lightweight network latency and link quality visualization tool.
It may not be as powerful or feature-rich as Smokeping, but it's lightweight.
Just scp and run


```bash
#install
cd /home/pingping/
wget https://github.com/githubflyideas/pingping/releases/download/v2.11.2/pingping-v2.11.2-linux-amd64.tar.gz
tar -zxvf pingping-v2.11.2-linux-amd64.tar.gz

#run
./pingping user=admin passwd=admin
```
Open http://localhost:8517 and watch your first puff of network smoke.

-----------------------------------------------------------
Add target host 
```
 echo "1.2.3.4 myhost pace=fast"    >> targets/ping.list
 echo "10.0.0.5:443 ads-api"        >> targets/tcp.list
```
Data cleanup
Retention is fixed at 300 days. To purge earlier by hand, just find+delete):

```
# clean.sh [N] — delete pingping probe data older than N days (default 30).
# Data files are plain per-day JSONL under ./data/<target>/YYYY-MM-DD.jsonl,
# so cleanup is just find+delete. Run from the pingping directory.
days="${1:-30}"
find ./data -type f -name '20*.jsonl' -mtime +"$days" -print -delete    
```
Latest [Releases](https://github.com/githubflyideas/pingping/releases)   







apache 2.0
