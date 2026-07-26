//go:build !linux

package pluginhost

import (
	"fmt"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type GPIO struct {
	Root string
}

func (GPIO) Line(pin int) (pluginmanager.GPIOLine, error) {
	return nil, fmt.Errorf("pluginhost: GPIO is only supported on Linux")
}

var _ pluginmanager.GPIOCapability = GPIO{}
