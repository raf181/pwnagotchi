//go:build linux

package pluginhost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
	"golang.org/x/sys/unix"
)

// GPIO is the production Linux sysfs GPIO capability. Raspberry Pi kernels
// used by the image still expose this interface alongside /dev/gpiochip0.
type GPIO struct {
	Root string
}

func (g GPIO) Line(pin int) (pluginmanager.GPIOLine, error) {
	if pin < 0 || pin > 4095 {
		return nil, fmt.Errorf("pluginhost: invalid GPIO pin %d", pin)
	}
	root := g.Root
	if root == "" {
		root = "/sys/class/gpio"
	}
	lineDir := filepath.Join(root, "gpio"+strconv.Itoa(pin))
	if _, err := os.Stat(lineDir); os.IsNotExist(err) {
		if err := writeControl(filepath.Join(root, "export"), strconv.Itoa(pin)); err != nil {
			if _, statErr := os.Stat(lineDir); statErr != nil {
				return nil, fmt.Errorf("pluginhost: exporting GPIO %d: %w", pin, err)
			}
		}
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if _, err := os.Stat(filepath.Join(lineDir, "value")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("pluginhost: GPIO %d did not appear after export", pin)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(lineDir, "direction"), []byte("in"), 0); err != nil {
		return nil, fmt.Errorf("pluginhost: setting GPIO %d input: %w", pin, err)
	}
	value, err := os.OpenFile(filepath.Join(lineDir, "value"), os.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: opening GPIO %d value: %w", pin, err)
	}
	return &gpioLine{pin: pin, dir: lineDir, value: value}, nil
}

type gpioLine struct {
	mu    sync.Mutex
	pin   int
	dir   string
	value *os.File
}

func (l *gpioLine) Read() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.value == nil {
		return false, fmt.Errorf("pluginhost: GPIO %d is closed", l.pin)
	}
	if _, err := l.value.Seek(0, 0); err != nil {
		return false, err
	}
	var buf [2]byte
	n, err := l.value.Read(buf[:])
	if err != nil {
		return false, err
	}
	if n == 0 || (buf[0] != '0' && buf[0] != '1') {
		return false, fmt.Errorf("pluginhost: GPIO %d returned an invalid value", l.pin)
	}
	return buf[0] == '1', nil
}

func (l *gpioLine) Write(high bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.value == nil {
		return fmt.Errorf("pluginhost: GPIO %d is closed", l.pin)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "direction"), []byte("out"), 0); err != nil {
		return err
	}
	if _, err := l.value.Seek(0, 0); err != nil {
		return err
	}
	value := []byte("0")
	if high {
		value[0] = '1'
	}
	_, err := l.value.Write(value)
	return err
}

func (l *gpioLine) WaitEdge(ctx context.Context) error {
	l.mu.Lock()
	if l.value == nil {
		l.mu.Unlock()
		return fmt.Errorf("pluginhost: GPIO %d is closed", l.pin)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "direction"), []byte("in"), 0); err != nil {
		l.mu.Unlock()
		return err
	}
	if err := os.WriteFile(filepath.Join(l.dir, "edge"), []byte("falling"), 0); err != nil {
		l.mu.Unlock()
		return err
	}
	fd := int32(l.value.Fd())
	_, _ = l.value.Seek(0, 0)
	var clear [2]byte
	_, _ = l.value.Read(clear[:])
	l.mu.Unlock()

	pollFDs := []unix.PollFd{{Fd: fd, Events: unix.POLLPRI | unix.POLLERR}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := unix.Poll(pollFDs, 250)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		if n > 0 {
			l.mu.Lock()
			if l.value == nil {
				l.mu.Unlock()
				return fmt.Errorf("pluginhost: GPIO %d is closed", l.pin)
			}
			_, _ = l.value.Seek(0, 0)
			_, _ = l.value.Read(clear[:])
			l.mu.Unlock()
			return nil
		}
	}
}

func (l *gpioLine) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.value == nil {
		return nil
	}
	err := l.value.Close()
	l.value = nil
	return err
}

func writeControl(path, value string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

var _ pluginmanager.GPIOCapability = GPIO{}
var _ pluginmanager.GPIOLine = (*gpioLine)(nil)
