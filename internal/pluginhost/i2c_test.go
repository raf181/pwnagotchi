//go:build linux

package pluginhost

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestI2CRejectsInvalidBus(t *testing.T) {
	if _, err := (I2C{}).Open(-1, 0x32); err == nil {
		t.Fatal("expected negative bus number to be rejected")
	}
}

func TestI2CUsesConfiguredDeviceRoot(t *testing.T) {
	root := t.TempDir()
	_, err := (I2C{DeviceRoot: root}).Open(7, 0x32)
	if err == nil {
		t.Fatal("expected missing test device to fail")
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "i2c-7")) {
		t.Fatalf("error does not identify expected device path: %v", err)
	}
}
