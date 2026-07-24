package bttether

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jayofelony/pwnagotchi/internal/config"
	"github.com/jayofelony/pwnagotchi/internal/pluginmanager"
)

// recordedCmd/fakeRunner mirror the established pattern (see switcher's
// own test file) for verifying exact argv sequences without touching a
// real shell/bluetooth/network stack.
type recordedCmd struct {
	name string
	args []string
}

type fakeRunner struct {
	mu       sync.Mutex
	cmds     []recordedCmd
	handlers map[string]func(args []string) ([]byte, error)
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{handlers: map[string]func([]string) ([]byte, error){}}
}

func (f *fakeRunner) on(name string, fn func(args []string) ([]byte, error)) {
	f.handlers[name] = fn
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, recordedCmd{name, args})
	f.mu.Unlock()
	if h, ok := f.handlers[name]; ok {
		return h(args)
	}
	return nil, nil
}

func (f *fakeRunner) snapshot() []recordedCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCmd(nil), f.cmds...)
}

type fakeView struct {
	mu     sync.Mutex
	values map[string]string
	added  map[string]bool
}

func newFakeView() *fakeView { return &fakeView{values: map[string]string{}, added: map[string]bool{}} }
func (f *fakeView) Set(key, value string) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) Update(bool)  {}
func (f *fakeView) Kind() string { return "dummydisplay" }
func (f *fakeView) HasElement(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.added[key]
}
func (f *fakeView) RemoveElement(key string) {
	f.mu.Lock()
	delete(f.added, key)
	f.mu.Unlock()
}
func (f *fakeView) AddText(key, value string, x, y int, font pluginmanager.FontStyle, wrap bool, maxLength int) {
	f.mu.Lock()
	f.added[key] = true
	f.values[key] = value
	f.mu.Unlock()
}
func (f *fakeView) AddLabeledValue(key, label, value string, x, y int, labelFont, valueFont pluginmanager.FontStyle, labelSpacing int) {
	f.mu.Lock()
	f.added[key] = true
	f.mu.Unlock()
}
func (f *fakeView) OnUploading(string) {}
func (f *fakeView) OnNormal()          {}
func (f *fakeView) Width() int         { return 250 }
func (f *fakeView) Height() int        { return 122 }

func (f *fakeView) get(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[key]
}

var _ pluginmanager.ViewCapability = (*fakeView)(nil)
var _ pluginmanager.CommandRunner = (*fakeRunner)(nil)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

var _ pluginmanager.Clock = (*fakeClock)(nil)

func newTestPlugin(t *testing.T, runner *fakeRunner, view *fakeView, httpClient *http.Client) (*Plugin, *fakeClock) {
	t.Helper()
	p := New()
	clock := &fakeClock{now: time.Now()}
	if err := p.OnLoad(pluginmanager.Capabilities{
		Config:     config.Map{"auto_reconnect": true},
		Exec:       runner,
		View:       view,
		HTTPClient: httpClient,
		Clock:      clock,
	}); err != nil {
		t.Fatal(err)
	}
	return p, clock
}

func TestOnLoadAddsOnScreenElements(t *testing.T) {
	view := newFakeView()
	newTestPlugin(t, newFakeRunner(), view, nil)
	if !view.HasElement("bt_tether_mini") || !view.HasElement("bt_tether_detail") {
		t.Fatalf("expected both on-screen elements added, got %+v", view.added)
	}
}

func TestOnLoadRespectsShowOnScreenFalse(t *testing.T) {
	view := newFakeView()
	p := New()
	_ = p.OnLoad(pluginmanager.Capabilities{Config: config.Map{"show_on_screen": false}, View: view})
	if view.HasElement("bt_tether_mini") || view.HasElement("bt_tether_detail") {
		t.Fatal("expected no on-screen elements when show_on_screen is false")
	}
}

func TestParseDeviceInfo(t *testing.T) {
	out := "Device AA:BB:CC:DD:EE:FF (public)\n\tName: MyPhone\n\tPaired: yes\n\tTrusted: yes\n\tConnected: no\n\tUUID: Nap (00001116-0000-1000-8000-00805f9b34fb)\n"
	info := parseDeviceInfo(out)
	if !info.Paired || !info.Trusted || info.Connected {
		t.Fatalf("unexpected parse: %+v", info)
	}
	if !info.HasNAP {
		t.Fatal("expected NAP UUID detected")
	}
	if info.Name != "MyPhone" {
		t.Fatalf("Name = %q, want MyPhone", info.Name)
	}
}

func TestParsePairedDevices(t *testing.T) {
	out := "Device AA:BB:CC:DD:EE:01 Pixel 7\nDevice AA:BB:CC:DD:EE:02 iPhone\n"
	devices := parsePairedDevices(out)
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d: %+v", len(devices), devices)
	}
	if devices[0].MAC != "AA:BB:CC:DD:EE:01" || devices[0].Name != "Pixel 7" {
		t.Fatalf("unexpected first device: %+v", devices[0])
	}
}

func TestScreenStatusLetter(t *testing.T) {
	cases := []struct {
		status ConnectionStatus
		want   string
	}{
		{ConnectionStatus{PANActive: true}, "C"},
		{ConnectionStatus{Connected: true}, "N"},
		{ConnectionStatus{Paired: true}, "P"},
		{ConnectionStatus{}, "D"},
	}
	for _, c := range cases {
		if got := screenStatusLetter(c.status); got != c.want {
			t.Errorf("screenStatusLetter(%+v) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestFindPANInterfaceParsesIPLinkShow(t *testing.T) {
	runner := newFakeRunner()
	runner.on("ip", func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "link" && args[1] == "show" {
			return []byte("1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536\n5: bnep0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n"), nil
		}
		return nil, nil
	})
	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	iface, err := p.findPANInterface(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if iface != "bnep0" {
		t.Fatalf("iface = %q, want bnep0", iface)
	}
}

func TestInterfaceIPSkipsLoopback(t *testing.T) {
	runner := newFakeRunner()
	runner.on("ip", func(args []string) ([]byte, error) {
		return []byte("2: bnep0: <UP>\n    inet 192.168.44.5/24 brd 192.168.44.255 scope global bnep0\n"), nil
	})
	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	ip, err := p.interfaceIP(context.Background(), "bnep0")
	if err != nil || ip != "192.168.44.5" {
		t.Fatalf("ip = %q, err = %v", ip, err)
	}
}

func TestDefaultRouteInterface(t *testing.T) {
	runner := newFakeRunner()
	runner.on("ip", func(args []string) ([]byte, error) {
		return []byte("default via 192.168.44.1 dev bnep0 proto dhcp metric 100\n"), nil
	})
	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	iface, err := p.defaultRouteInterface(context.Background())
	if err != nil || iface != "bnep0" {
		t.Fatalf("iface = %q, err = %v", iface, err)
	}
}

func TestNAPConnectSendsCorrectDbusCall(t *testing.T) {
	runner := newFakeRunner()
	var gotArgs []string
	runner.on("dbus-send", func(args []string) ([]byte, error) {
		gotArgs = args
		return []byte("method return"), nil
	})
	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	if err := p.napConnect(context.Background(), "AA:BB:CC:DD:EE:FF"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF") {
		t.Fatalf("expected device object path in args, got %v", gotArgs)
	}
	if !strings.Contains(joined, NAPUUID) {
		t.Fatalf("expected NAP UUID in args, got %v", gotArgs)
	}
	if !strings.Contains(joined, "org.bluez.Device1.ConnectProfile") {
		t.Fatalf("expected ConnectProfile method in args, got %v", gotArgs)
	}
}

func TestConnectDeviceFullFlowPairsTrustsConnectsAndBringsUpNetwork(t *testing.T) {
	runner := newFakeRunner()
	runner.on("bluetoothctl", func(args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "info" {
			return []byte("Paired: no\nTrusted: no\nConnected: no\n"), nil
		}
		return []byte("ok"), nil
	})
	runner.on("dbus-send", func(args []string) ([]byte, error) { return []byte("method return"), nil })
	linkShown := false
	runner.on("ip", func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "link" && args[1] == "show" {
			if !linkShown {
				linkShown = true
				return []byte(""), nil // not up yet on first check
			}
			return []byte("5: bnep0: <UP>\n"), nil
		}
		if len(args) >= 2 && args[0] == "link" && args[1] == "set" {
			return []byte(""), nil
		}
		return []byte(""), nil
	})
	runner.on("dhclient", func(args []string) ([]byte, error) { return []byte(""), nil })

	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	p.connectDevice(context.Background(), DiscoveredDevice{MAC: "AA:BB:CC:DD:EE:FF", Name: "Test Phone"})

	cmds := runner.snapshot()
	var sawPair, sawTrust, sawConnect, sawDBus, sawDHCP bool
	for _, c := range cmds {
		if c.name == "bluetoothctl" && len(c.args) > 0 {
			switch c.args[0] {
			case "pair":
				sawPair = true
			case "trust":
				sawTrust = true
			case "connect":
				sawConnect = true
			}
		}
		if c.name == "dbus-send" {
			sawDBus = true
		}
		if c.name == "dhclient" {
			sawDHCP = true
		}
	}
	if !sawPair || !sawTrust || !sawConnect || !sawDBus || !sawDHCP {
		t.Fatalf("expected pair+trust+connect+dbus-send+dhclient all invoked, got %+v", cmds)
	}
	p.mu.Lock()
	status := p.status
	p.mu.Unlock()
	if status != StateConnected {
		t.Fatalf("expected StateConnected, got %v", status)
	}
}

func TestOnWebhookServesRootHTML(t *testing.T) {
	p, _ := newTestPlugin(t, newFakeRunner(), newFakeView(), nil)
	req := httptest.NewRequest(http.MethodGet, "/plugins/bt-tether/", nil)
	resp, err := p.OnWebhook("", req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), "Bluetooth Tether") {
		t.Fatalf("unexpected root response: status=%d body-has-title=%v", resp.Status, strings.Contains(string(resp.Body), "Bluetooth Tether"))
	}
}

func TestOnWebhookStatusReturnsCurrentState(t *testing.T) {
	p, _ := newTestPlugin(t, newFakeRunner(), newFakeView(), nil)
	req := httptest.NewRequest(http.MethodGet, "/plugins/bt-tether/status", nil)
	resp, err := p.OnWebhook("status", req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `"status":"IDLE"`) {
		t.Fatalf("unexpected status body: %s", resp.Body)
	}
}

func TestOnWebhookUnknownPathReturns404(t *testing.T) {
	p, _ := newTestPlugin(t, newFakeRunner(), newFakeView(), nil)
	req := httptest.NewRequest(http.MethodGet, "/plugins/bt-tether/nope", nil)
	resp, _ := p.OnWebhook("nope", req)
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Status)
	}
}

func TestOnWebhookDisconnectRejectsInvalidMAC(t *testing.T) {
	p, _ := newTestPlugin(t, newFakeRunner(), newFakeView(), nil)
	req := httptest.NewRequest(http.MethodGet, "/plugins/bt-tether/disconnect?mac=not-a-mac", nil)
	resp, _ := p.OnWebhook("disconnect", req)
	if !strings.Contains(string(resp.Body), `"success":false`) {
		t.Fatalf("expected failure for invalid MAC, got %s", resp.Body)
	}
}

func TestOnWebhookUnpairCallsBluetoothctlRemove(t *testing.T) {
	runner := newFakeRunner()
	runner.on("bluetoothctl", func(args []string) ([]byte, error) { return []byte("Device has been removed"), nil })
	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	req := httptest.NewRequest(http.MethodGet, "/plugins/bt-tether/unpair?mac=AA:BB:CC:DD:EE:FF", nil)
	resp, err := p.OnWebhook("unpair", req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `"success":true`) {
		t.Fatalf("expected success, got %s", resp.Body)
	}
	cmds := runner.snapshot()
	if len(cmds) != 1 || cmds[0].args[0] != "remove" {
		t.Fatalf("expected a single bluetoothctl remove call, got %+v", cmds)
	}
}

func TestTestInternetConnectivityUsesInjectedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	orig := internetCheckURL
	internetCheckURL = srv.URL
	defer func() { internetCheckURL = orig }()

	p, _ := newTestPlugin(t, newFakeRunner(), newFakeView(), srv.Client())
	ok, msg := p.testInternetConnectivity(context.Background())
	if !ok {
		t.Fatalf("expected success, got %q", msg)
	}
}

func TestTestInternetConnectivityHandlesFailure(t *testing.T) {
	orig := internetCheckURL
	internetCheckURL = "http://127.0.0.1:1" // nothing listening
	defer func() { internetCheckURL = orig }()

	p, _ := newTestPlugin(t, newFakeRunner(), newFakeView(), &http.Client{Timeout: time.Second})
	ok, msg := p.testInternetConnectivity(context.Background())
	if ok {
		t.Fatalf("expected failure, got success: %q", msg)
	}
}

func TestReconnectTickAttemptsReconnectWhenDisconnected(t *testing.T) {
	runner := newFakeRunner()
	callCount := 0
	runner.on("bluetoothctl", func(args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "info" {
			callCount++
			return []byte("Paired: yes\nTrusted: yes\nConnected: no\n"), nil
		}
		return []byte(""), nil
	})
	runner.on("ip", func(args []string) ([]byte, error) { return []byte(""), nil })
	runner.on("dbus-send", func(args []string) ([]byte, error) { return []byte(""), nil })

	p, _ := newTestPlugin(t, runner, newFakeView(), nil)
	p.mu.Lock()
	p.phoneMAC = "AA:BB:CC:DD:EE:FF"
	p.mu.Unlock()

	p.reconnectTick()

	if callCount == 0 {
		t.Fatal("expected at least one status check during reconnect attempt")
	}
}

func TestReconnectTickSkippedDuringCooldown(t *testing.T) {
	runner := newFakeRunner()
	runner.on("bluetoothctl", func(args []string) ([]byte, error) { return []byte(""), nil })
	p, clock := newTestPlugin(t, runner, newFakeView(), nil)
	p.mu.Lock()
	p.phoneMAC = "AA:BB:CC:DD:EE:FF"
	p.cooldownUntil = clock.Now().Add(time.Hour)
	p.mu.Unlock()

	p.reconnectTick()
	if len(runner.snapshot()) != 0 {
		t.Fatalf("expected no commands run during cooldown, got %+v", runner.snapshot())
	}
}

func TestOnUnloadRemovesElementsAndStopsMonitor(t *testing.T) {
	view := newFakeView()
	p, _ := newTestPlugin(t, newFakeRunner(), view, nil)
	p.onReady() // starts the monitor goroutine
	if err := p.OnUnload(); err != nil {
		t.Fatal(err)
	}
	if view.HasElement("bt_tether_mini") || view.HasElement("bt_tether_detail") {
		t.Fatal("expected on-screen elements removed after OnUnload")
	}
}

func TestValidMAC(t *testing.T) {
	if !validMAC("AA:BB:CC:DD:EE:FF") {
		t.Fatal("expected a well-formed MAC to validate")
	}
	if validMAC("not-a-mac") {
		t.Fatal("expected an invalid MAC to fail validation")
	}
}
