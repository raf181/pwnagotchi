//go:build !linux

package pluginhost

import (
	"fmt"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

type I2C struct {
	DeviceRoot string
}

func (I2C) Open(bus int, addr uint8) (pluginmanager.I2CDevice, error) {
	return nil, fmt.Errorf("pluginhost: I2C is only supported on Linux")
}

var _ pluginmanager.I2CCapability = I2C{}
