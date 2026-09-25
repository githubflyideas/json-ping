package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func helpOutput() string {
	var b bytes.Buffer
	usage(&b)
	return b.String()
}

// Every flag the program defines must be documented in --help.
func TestHelpDocumentsEveryFlag(t *testing.T) {
	out := helpOutput()
	flag.VisitAll(func(f *flag.Flag) {
		if !strings.HasPrefix(f.Name, "test.") && !strings.Contains(out, "--"+f.Name) {
			t.Errorf("--%s missing from --help", f.Name)
		}
	})
	if strings.Contains(out, "%!") {
		t.Fatal("broken format verb in help text")
	}
}

// Every ./json-ping example in --help must be accepted by the real parsers, so
// the help cannot drift from the code.
func TestHelpExamplesParse(t *testing.T) {
	n := 0
	for _, line := range strings.Split(helpOutput(), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "nohup ")
		if !strings.HasPrefix(line, "./json-ping") || strings.HasPrefix(line, "./json-ping [") {
			continue
		}
		n++
		fs := flag.NewFlagSet("example", flag.ContinueOnError)
		listen := fs.String("listen", defaultConfig().Listen, "")
		local := fs.Bool("localhost", false, "")
		fs.Bool("version", false, "")
		args := strings.Fields(strings.SplitN(strings.SplitN(line, "#", 2)[0], ">", 2)[0])[1:]
		if err := fs.Parse(args); err != nil {
			t.Errorf("%q: %v", line, err)
			continue
		}
		if _, err := parseAuthArgs(fs.Args()); err != nil {
			t.Errorf("%q: %v", line, err)
		}
		if _, err := listenAddr(*listen, *local); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
	if n < 5 {
		t.Fatalf("only %d examples found", n)
	}
}

// Every echo line in --help must be a valid target line for its list.
func TestHelpTargetLinesParse(t *testing.T) {
	n := 0
	for _, line := range strings.Split(helpOutput(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "echo \"") {
			continue
		}
		n++
		body := line[len(`echo "`) : strings.Index(line[len(`echo "`):], `"`)+len(`echo "`)]
		typ := "icmp"
		if strings.HasSuffix(line, "tcp.list") {
			typ = "tcp"
		}
		if _, err := parseListLine(body, typ); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
	if n < 4 {
		t.Fatalf("only %d target examples found", n)
	}
}

func TestWantsHelp(t *testing.T) {
	for _, a := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--listen", "0.0.0.0:1", "-help"}} {
		if !wantsHelp(a) {
			t.Errorf("%v: want help", a)
		}
	}
	if wantsHelp([]string{"user=help", "passwd=x"}) {
		t.Error("a user named help is not a help request")
	}
}

func TestFlagAfterAuthArgsIsExplained(t *testing.T) {
	_, err := parseAuthArgs([]string{"user=a", "passwd=b", "--listen", "0.0.0.0:9000"})
	if err == nil || !strings.Contains(err.Error(), "put flags first") {
		t.Fatalf("want a put-flags-first hint, got %v", err)
	}
}
