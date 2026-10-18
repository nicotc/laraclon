// Package ports checks which TCP ports are free, who owns a busy one, and which ranges Windows
// reserves for Hyper-V, WSL or Docker (binding there fails with "access forbidden").
package ports

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Owner is the process listening on a port.
type Owner struct {
	PID uint32
	Exe string
}

func (o Owner) String() string {
	if o.Exe == "" {
		return fmt.Sprintf("PID %d", o.PID)
	}
	return fmt.Sprintf("%s (PID %d)", o.Exe, o.PID)
}

// Busy reports whether something listens on port, on any local address, and who it is.
func Busy(port int) (Owner, bool) {
	for _, l := range listeners() {
		if l.port == port {
			return Owner{PID: l.pid, Exe: processPath(l.pid)}, true
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return Owner{}, true
	}
	ln.Close()
	return Owner{}, false
}

type listener struct {
	port int
	pid  uint32
}

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

const tcpTableOwnerPIDListener = 3

// listeners reads the IPv4 and IPv6 TCP listener tables with their owning processes.
func listeners() []listener {
	var out []listener
	for _, af := range []uint32{windows.AF_INET, windows.AF_INET6} {
		var size uint32
		getExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0)
		if size == 0 {
			continue
		}
		buf := make([]byte, size+1024)
		size = uint32(len(buf))
		r, _, _ := getExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0)
		if r != 0 {
			continue
		}
		n := *(*uint32)(unsafe.Pointer(&buf[0]))
		// MIB_TCPROW_OWNER_PID is 24 bytes; MIB_TCP6ROW_OWNER_PID is 56 bytes.
		rowSize, portOff, pidOff := uintptr(24), uintptr(8), uintptr(20)
		if af == windows.AF_INET6 {
			rowSize, portOff, pidOff = 56, 20, 52
		}
		for i := uintptr(0); i < uintptr(n); i++ {
			row := unsafe.Pointer(&buf[4+i*rowSize])
			raw := *(*uint32)(unsafe.Add(row, portOff))
			port := int(uint16(raw>>8&0xff) | uint16(raw&0xff)<<8)
			pid := *(*uint32)(unsafe.Add(row, pidOff))
			out = append(out, listener{port: port, pid: pid})
		}
	}
	return out
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// Range is an inclusive port range.
type Range struct{ Start, End int }

var (
	excludedOnce sync.Once
	excluded     []Range
	rangeRe      = regexp.MustCompile(`^\s*(\d+)\s+(\d+)`)
)

// Excluded returns the TCP port ranges Windows has reserved (netsh int ipv4 show excludedportrange).
func Excluded() []Range {
	excludedOnce.Do(func() {
		cmd := exec.Command("netsh", "int", "ipv4", "show", "excludedportrange", "protocol=tcp")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		out, err := cmd.Output()
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			if m := rangeRe.FindStringSubmatch(line); m != nil {
				a, _ := strconv.Atoi(m[1])
				b, _ := strconv.Atoi(m[2])
				excluded = append(excluded, Range{a, b})
			}
		}
	})
	return excluded
}

// Reserved reports whether port falls in a range Windows keeps for itself.
func Reserved(port int) bool {
	for _, r := range Excluded() {
		if port >= r.Start && port <= r.End {
			return true
		}
	}
	return false
}

// Check explains why a port cannot be used, or returns nil.
func Check(port int) error {
	if Reserved(port) {
		return fmt.Errorf("el puerto %d está reservado por Windows (Hyper-V, WSL o Docker)", port)
	}
	if owner, busy := Busy(port); busy {
		if owner.PID != 0 {
			return fmt.Errorf("el puerto %d lo usa %s", port, owner)
		}
		return fmt.Errorf("el puerto %d está ocupado", port)
	}
	return nil
}

// FirstFree returns the first usable port in [from, to] that is not in taken.
func FirstFree(from, to int, taken map[int]bool) (int, error) {
	for p := from; p <= to; p++ {
		if taken[p] || Reserved(p) {
			continue
		}
		if _, busy := Busy(p); !busy {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no hay puertos libres entre %d y %d", from, to)
}
