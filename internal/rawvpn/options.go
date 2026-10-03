package rawvpn

import (
	"fmt"
	"strconv"
	"strings"
)

// ClientOptions are split out before the existing fturn configuration parser.
type ClientOptions struct {
	TUN              string
	Address          string
	MTU              int
	KeyFile          string
	BypassFile       string
	StateDir         string
	ControlInterface int
}

func ParseClientOptions(args []string) (ClientOptions, []string, error) {
	o := ClientOptions{TUN: "ftraw0", Address: "10.77.0.2/24", MTU: DefaultMTU}
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") || (flag != "tun" && flag != "raw-address" && flag != "raw-mtu" && flag != "obf-key-file" && flag != "control-interface" && flag != "bypass-file" && flag != "state-dir") {
			rest = append(rest, arg)
			continue
		}
		if !hasValue {
			i++
			if i == len(args) {
				return o, nil, fmt.Errorf("-%s requires a value", flag)
			}
			value = args[i]
		}
		switch flag {
		case "tun":
			o.TUN = value
		case "raw-address":
			o.Address = value
		case "raw-mtu":
			n, err := strconv.Atoi(value)
			if err != nil {
				return o, nil, fmt.Errorf("-raw-mtu: %w", err)
			}
			o.MTU = n
		case "control-interface":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return o, nil, fmt.Errorf("-control-interface must be a nonnegative interface index")
			}
			o.ControlInterface = n
		case "state-dir":
			o.StateDir = value
		case "bypass-file":
			o.BypassFile = value
		case "obf-key-file":
			o.KeyFile = value
		}
	}
	return o, rest, nil
}
