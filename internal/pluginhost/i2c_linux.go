//go:build linux

package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

const (
	i2cRDWR        = 0x0707
	i2cMRead       = 0x0001
	maxI2CTransfer = 65535
)

// I2C is the production Linux i2c-dev capability.
type I2C struct {
	// DeviceRoot is empty in production and exists to make path handling
	// testable without opening real hardware.
	DeviceRoot string
}

func (i I2C) Open(bus int, addr uint8) (pluginmanager.I2CDevice, error) {
	if bus < 0 {
		return nil, fmt.Errorf("pluginhost: invalid I2C bus %d", bus)
	}
	root := i.DeviceRoot
	if root == "" {
		root = "/dev"
	}
	path := filepath.Join(root, fmt.Sprintf("i2c-%d", bus))
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: opening %s: %w", path, err)
	}
	return &i2cDevice{file: file, addr: uint16(addr)}, nil
}

type i2cDevice struct {
	mu   sync.Mutex
	file *os.File
	addr uint16
}

type i2cMessage struct {
	Addr  uint16
	Flags uint16
	Len   uint16
	_     uint16
	Buf   uintptr
}

type i2cTransfer struct {
	Messages uintptr
	Count    uint32
}

func (d *i2cDevice) ReadReg(reg uint8, n int) ([]byte, error) {
	if n < 0 || n > maxI2CTransfer {
		return nil, fmt.Errorf("pluginhost: invalid I2C read length %d", n)
	}
	if n == 0 {
		return []byte{}, nil
	}
	register := []byte{reg}
	out := make([]byte, n)
	messages := []i2cMessage{
		{Addr: d.addr, Len: 1, Buf: uintptr(unsafe.Pointer(&register[0]))},
		{Addr: d.addr, Flags: i2cMRead, Len: uint16(n), Buf: uintptr(unsafe.Pointer(&out[0]))},
	}
	if err := d.transfer(messages); err != nil {
		return nil, err
	}
	runtime.KeepAlive(register)
	runtime.KeepAlive(out)
	return out, nil
}

func (d *i2cDevice) WriteReg(reg uint8, data []byte) error {
	if len(data)+1 > maxI2CTransfer {
		return fmt.Errorf("pluginhost: I2C write exceeds %d bytes", maxI2CTransfer)
	}
	payload := make([]byte, len(data)+1)
	payload[0] = reg
	copy(payload[1:], data)
	messages := []i2cMessage{{
		Addr: d.addr,
		Len:  uint16(len(payload)),
		Buf:  uintptr(unsafe.Pointer(&payload[0])),
	}}
	err := d.transfer(messages)
	runtime.KeepAlive(payload)
	return err
}

func (d *i2cDevice) transfer(messages []i2cMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return fmt.Errorf("pluginhost: I2C device is closed")
	}
	request := i2cTransfer{
		Messages: uintptr(unsafe.Pointer(&messages[0])),
		Count:    uint32(len(messages)),
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, d.file.Fd(), i2cRDWR, uintptr(unsafe.Pointer(&request)))
	runtime.KeepAlive(messages)
	if errno != 0 {
		return fmt.Errorf("pluginhost: I2C transfer: %w", errno)
	}
	return nil
}

func (d *i2cDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return nil
	}
	err := d.file.Close()
	d.file = nil
	return err
}

var _ pluginmanager.I2CCapability = I2C{}
var _ pluginmanager.I2CDevice = (*i2cDevice)(nil)
