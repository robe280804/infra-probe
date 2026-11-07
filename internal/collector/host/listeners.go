package host

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Listener is a socket waiting for connections (TCP) or bound to a port (UDP).
type Listener struct {
	Protocol string `json:"protocol"` // tcp, tcp6, udp, udp6
	Address  string `json:"address"`
	Port     int    `json:"port"`

	// AllInterfaces is true when the socket is bound to 0.0.0.0 or ::, that is
	// reachable from every network the machine is on (the firewall permitting).
	AllInterfaces bool `json:"all_interfaces"`
	Loopback      bool `json:"loopback"`

	// PID and Process identify the owner. Empty when the probe is not allowed
	// to look at that process (it needs root for processes of other users).
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
}

// socketTables are the kernel tables of sockets, with the protocol name used
// in the report. TCP lists LISTEN sockets, UDP the bound ones.
var socketTables = []struct{ file, proto string }{
	{"proc/net/tcp", "tcp"},
	{"proc/net/tcp6", "tcp6"},
	{"proc/net/udp", "udp"},
	{"proc/net/udp6", "udp6"},
}

type socketEntry struct {
	proto string
	ip    net.IP
	port  int
	inode uint64
}

func (c *Collector) collectListeners() ([]Listener, []string, error) {
	var (
		errs    []error
		sockets []socketEntry
	)
	for _, t := range socketTables {
		data, err := c.readFile(t.file)
		if errors.Is(err, fs.ErrNotExist) {
			continue // e.g. no IPv6 on this kernel
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("listeners: %w", err))
			continue
		}
		sockets = append(sockets, parseSocketTable(data, t.proto)...)
	}

	want := map[uint64]bool{}
	for _, s := range sockets {
		if s.inode != 0 {
			want[s.inode] = true
		}
	}
	owners, unreadable := c.socketOwners(want)

	listeners := []Listener{}
	index := map[string]int{}
	for _, s := range sockets {
		l := Listener{
			Protocol: s.proto, Address: s.ip.String(), Port: s.port,
			AllInterfaces: s.ip.IsUnspecified(), Loopback: s.ip.IsLoopback(),
		}
		if o, ok := owners[s.inode]; ok {
			l.PID, l.Process = o.pid, o.comm
		}
		// Several sockets can share a port (SO_REUSEPORT): list it once.
		key := fmt.Sprintf("%s|%s|%d", l.Protocol, l.Address, l.Port)
		if i, dup := index[key]; dup {
			if listeners[i].PID == 0 && l.PID != 0 {
				listeners[i] = l
			}
			continue
		}
		index[key] = len(listeners)
		listeners = append(listeners, l)
	}

	unresolved := 0
	for _, l := range listeners {
		if l.PID == 0 {
			unresolved++
		}
	}
	sort.Slice(listeners, func(i, j int) bool {
		a, b := listeners[i], listeners[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		return a.Address < b.Address
	})

	var notes []string
	switch {
	case unresolved > 0 && unreadable > 0:
		notes = append(notes, fmt.Sprintf(
			"%d of %d listening sockets have no process: the probe cannot inspect %d processes of other users. Run it as root to see them.",
			unresolved, len(listeners), unreadable))
	case unresolved > 0:
		// Nothing was off limits, so the owner is somewhere the probe cannot
		// look: the kernel itself, or a process in another PID namespace.
		notes = append(notes, fmt.Sprintf(
			"%d of %d listening sockets have no process the probe can see (kernel services, or processes in another namespace such as a container or a relay).",
			unresolved, len(listeners)))
	}
	return listeners, notes, errors.Join(errs...)
}

// parseSocketTable parses /proc/net/{tcp,tcp6,udp,udp6}. Only sockets that
// accept traffic are kept: TCP in LISTEN state (0A), UDP bound and not
// connected (07).
func parseSocketTable(data []byte, proto string) []socketEntry {
	wantState := "0A"
	if strings.HasPrefix(proto, "udp") {
		wantState = "07"
	}

	var out []socketEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != wantState {
			continue
		}
		ip, port, ok := parseSocketAddr(f[1])
		if !ok {
			continue
		}
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		out = append(out, socketEntry{proto: proto, ip: ip, port: port, inode: inode})
	}
	return out
}

// parseSocketAddr decodes "0100007F:1F90" (127.0.0.1:8080). The kernel prints
// the address as 32-bit words in host (little endian) order, 1 word for IPv4
// and 4 for IPv6.
func parseSocketAddr(s string) (net.IP, int, bool) {
	hexIP, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	raw, err := hex.DecodeString(hexIP)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, 0, false
	}
	ip := make(net.IP, len(raw))
	for w := 0; w < len(raw); w += 4 {
		for b := 0; b < 4; b++ {
			ip[w+b] = raw[w+3-b]
		}
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return nil, 0, false
	}
	return ip, int(port), true
}

type socketOwner struct {
	pid  int
	comm string
}

// socketOwners finds which process holds each wanted socket inode, by looking
// for "socket:[inode]" links in /proc/<pid>/fd. It also returns how many
// processes it was not allowed to inspect.
func (c *Collector) socketOwners(want map[uint64]bool) (map[uint64]socketOwner, int) {
	owners := map[uint64]socketOwner{}
	if len(want) == 0 {
		return owners, 0
	}
	entries, err := fs.ReadDir(c.fsys, "proc")
	if err != nil {
		return owners, 0
	}

	unreadable := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a process
		}
		fdDir := "proc/" + e.Name() + "/fd"
		fds, err := fs.ReadDir(c.fsys, fdDir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				unreadable++
			}
			continue // otherwise the process is gone
		}
		for _, fd := range fds {
			target, err := c.readlink(fdDir + "/" + fd.Name())
			if err != nil {
				continue
			}
			inode, ok := parseSocketLink(target)
			if !ok || !want[inode] {
				continue
			}
			if _, taken := owners[inode]; !taken {
				owners[inode] = socketOwner{pid: pid, comm: c.processName(pid)}
			}
		}
		if len(owners) == len(want) {
			break
		}
	}
	return owners, unreadable
}

// parseSocketLink extracts the inode from "socket:[12345]".
func parseSocketLink(target string) (uint64, bool) {
	rest, ok := strings.CutPrefix(target, "socket:[")
	if !ok {
		return 0, false
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(rest, "]"), 10, 64)
	return inode, err == nil
}

// processName is the command name of a process (/proc/<pid>/comm).
func (c *Collector) processName(pid int) string {
	data, err := c.readFile("proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
