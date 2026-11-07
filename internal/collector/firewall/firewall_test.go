package firewall

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// permFS is an fs.FS where some files cannot be read, like the ufw rules of
// a probe that does not run as root.
type permFS struct {
	fs.FS
	denied map[string]bool
}

func (p permFS) Open(name string) (fs.File, error) {
	if p.denied[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return p.FS.Open(name)
}

// machine is a fake Linux host.
type machine struct {
	files  fstest.MapFS
	denied map[string]bool
	cmds   map[string]string // output by command name
	fail   map[string]error  // error by command name
	runs   []string
}

func (m *machine) collector() *Collector {
	return &Collector{
		linux: true,
		fsys:  permFS{FS: m.files, denied: m.denied},
		run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			m.runs = append(m.runs, name)
			if err := m.fail[name]; err != nil {
				return nil, err
			}
			out, ok := m.cmds[name]
			if !ok {
				return nil, errNotInstalled
			}
			return []byte(out), nil
		},
	}
}

func file(content string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(content)} }

const (
	statusRoot = "Name:\tinfra-probe\nCapEff:\t000001ffffffffff\n"
	statusUser = "Name:\tinfra-probe\nCapEff:\t0000000000000000\n"
)

func newMachine(status string) *machine {
	return &machine{
		files: fstest.MapFS{
			"proc/self/status":   file(status),
			"etc/ufw/ufw.conf":   file("# /etc/ufw/ufw.conf\nENABLED=yes\nLOGLEVEL=low\n"),
			"etc/default/ufw":    file("IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\nDEFAULT_FORWARD_POLICY=\"DROP\"\n"),
			"etc/ufw/user.rules": file(ufwUserRules),
		},
		denied: map[string]bool{},
		cmds: map[string]string{
			"iptables-save": iptablesSave,
			"nft":           nftRuleset,
		},
		fail: map[string]error{},
	}
}

func collect(t *testing.T, m *machine) (Info, error) {
	t.Helper()
	got, err := m.collector().Collect(context.Background())
	info, ok := got.(Info)
	if !ok {
		t.Fatalf("Collect returned %T", got)
	}
	return info, err
}

func TestCollect_Root(t *testing.T) {
	m := newMachine(statusRoot)
	info, err := collect(t, m)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !info.CanReadRules {
		t.Error("CanReadRules = false with CAP_NET_ADMIN")
	}
	u := info.UFW
	if u == nil || !u.Enabled || u.DefaultInput != "DROP" || u.DefaultForward != "DROP" || !u.IPv6 {
		t.Fatalf("UFW = %+v", u)
	}
	if !u.RulesReadable || len(u.Rules) != 5 {
		t.Errorf("rules: readable=%v, %d rules", u.RulesReadable, len(u.Rules))
	}
	if u.Loaded == nil || !*u.Loaded {
		t.Error("ufw chains are in iptables: Loaded must be true")
	}
	if info.Iptables == nil || info.Iptables.Chain("filter", "INPUT") == nil {
		t.Errorf("iptables = %+v", info.Iptables)
	}
	// ip6tables-save is not installed on this machine: no data, no error.
	if info.IP6tables != nil {
		t.Errorf("ip6tables = %+v", info.IP6tables)
	}
	if info.Nftables == nil || len(info.Nftables.Tables) != 2 {
		t.Errorf("nftables = %+v", info.Nftables)
	}
	if len(info.Notes) != 0 {
		t.Errorf("notes = %q", info.Notes)
	}
}

func TestCollect_UnprivilegedRunsNoCommand(t *testing.T) {
	m := newMachine(statusUser)
	m.denied["etc/ufw/user.rules"] = true

	info, err := collect(t, m)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(m.runs) != 0 {
		t.Errorf("commands run without CAP_NET_ADMIN: %v", m.runs)
	}
	if info.CanReadRules || info.Iptables != nil || info.Nftables != nil {
		t.Errorf("rules without permission: %+v", info)
	}

	// ufw is still described from its world readable files.
	u := info.UFW
	if u == nil || !u.Enabled || u.DefaultInput != "DROP" {
		t.Fatalf("UFW = %+v", u)
	}
	if u.RulesReadable || u.Rules == nil || len(u.Rules) != 0 || u.Loaded != nil {
		t.Errorf("unreadable rules: %+v", u)
	}

	joined := strings.Join(info.Notes, "\n")
	for _, want := range []string{"CAP_NET_ADMIN", "/etc/ufw/user.rules"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes do not mention %q: %q", want, info.Notes)
		}
	}
}

func TestCollect_NoUFW(t *testing.T) {
	m := newMachine(statusRoot)
	delete(m.files, "etc/ufw/ufw.conf")
	info, err := collect(t, m)
	if err != nil {
		t.Fatal(err)
	}
	if info.UFW != nil {
		t.Errorf("UFW = %+v, want nil when ufw.conf does not exist", info.UFW)
	}
}

func TestCollect_CommandFailures(t *testing.T) {
	m := newMachine(statusRoot)
	m.fail["iptables-save"] = errors.New("exit status 1: iptables-save v1.8.7 (legacy): Cannot initialize: Permission denied (you must be root)")
	m.fail["nft"] = errors.New("exit status 1: netlink: Error: cache initialization failed")

	info, err := collect(t, m)

	// The refusal is a note, the other failure an error; ufw is kept.
	if err == nil || !strings.Contains(err.Error(), "nft:") || strings.Contains(err.Error(), "iptables-save") {
		t.Errorf("error = %v", err)
	}
	if len(info.Notes) != 1 || !strings.Contains(info.Notes[0], "iptables-save was refused") {
		t.Errorf("notes = %q", info.Notes)
	}
	if info.UFW == nil || info.UFW.Loaded != nil {
		t.Errorf("UFW = %+v (Loaded unknown without iptables)", info.UFW)
	}
}

func TestCollect_NotLinux(t *testing.T) {
	c := &Collector{linux: false}
	got, err := c.Collect(context.Background())
	info := got.(Info)
	if err != nil || len(info.Notes) != 1 || info.UFW != nil {
		t.Errorf("got %+v, %v", info, err)
	}
}

func TestRunCommand_RefusesOtherPrograms(t *testing.T) {
	if _, err := runCommand(context.Background(), "iptables", "-F"); err == nil || errors.Is(err, errNotInstalled) {
		t.Errorf("runCommand(iptables -F) error = %v, want a refusal", err)
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "firewall" {
		t.Errorf("Name() = %q", got)
	}
}
