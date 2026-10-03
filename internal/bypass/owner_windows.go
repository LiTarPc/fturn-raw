//go:build windows

package bypass

import (
	"encoding/binary"
	"golang.org/x/sys/windows"
	"net/netip"
	"unsafe"
)

var iphlp = windows.NewLazySystemDLL("iphlpapi.dll")
var tcpTable = iphlp.NewProc("GetExtendedTcpTable")
var udpTable = iphlp.NewProc("GetExtendedUdpTable")

func lookupOwner(f flow) (owner, bool) {
	proc := udpTable
	class := uintptr(1)
	stride := 12
	if f.Proto == 6 {
		proc = tcpTable
		class = 5
		stride = 24
	}
	var size uint32
	ret, _, _ := proc.Call(0, uintptr(unsafe.Pointer(&size)), 0, 2, class, 0)
	if ret != 122 || size < 4 || size > 4<<20 {
		return owner{}, false
	}
	for retry := 0; retry < 3; retry++ {
		b := make([]byte, size)
		ret, _, _ = proc.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&size)), 0, 2, class, 0)
		if ret == 122 {
			if size > 4<<20 {
				return owner{}, false
			}
			continue
		}
		if ret != 0 {
			return owner{}, false
		}
		pid, ok := tableOwner(b, stride, f)
		if !ok {
			return owner{}, false
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if e != nil {
			return owner{}, false
		}
		defer windows.CloseHandle(h)
		path := make([]uint16, 32768)
		n := uint32(len(path))
		if windows.QueryFullProcessImageName(h, 0, &path[0], &n) != nil {
			return owner{}, false
		}
		return owner{pid, windows.UTF16ToString(path[:n])}, true
	}
	return owner{}, false
}

// Windows table addresses are raw network bytes; ports occupy the first two bytes.
func tableOwner(b []byte, stride int, f flow) (uint32, bool) {
	if len(b) < 4 {
		return 0, false
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n > (len(b)-4)/stride {
		return 0, false
	}
	var pid uint32
	for i := 0; i < n; i++ {
		v := b[4+i*stride : 4+(i+1)*stride]
		off := 0
		if stride == 24 {
			off = 4
		}
		ip := netip.AddrFrom4([4]byte(v[off : off+4]))
		port := binary.BigEndian.Uint16(v[off+4 : off+6])
		if port != f.SP || (!ip.IsUnspecified() && ip != f.Src) {
			continue
		}
		if stride == 24 && (netip.AddrFrom4([4]byte(v[12:16])) != f.Dst || binary.BigEndian.Uint16(v[16:18]) != f.DP) {
			continue
		}
		p := binary.LittleEndian.Uint32(v[stride-4:])
		if p == 0 {
			return 0, false
		}
		if pid != 0 && pid != p {
			return 0, false
		}
		pid = p
	}
	return pid, pid != 0
}
