package bttether

import (
	"context"
	"net/http"
)

// currentStatus ports _get_current_status: check for an active PAN
// interface first (fastest real indicator of a working connection),
// falling back to a bluetoothctl info query.
func (p *Plugin) currentStatus(ctx context.Context, mac string) (ConnectionStatus, error) {
	if iface, err := p.findPANInterface(ctx); err == nil && iface != "" {
		if ip, err := p.interfaceIP(ctx, iface); err == nil && ip != "" {
			return ConnectionStatus{
				Paired: true, Trusted: true, Connected: true, PANActive: true,
				Interface: iface, IPAddress: ip,
			}, nil
		}
	}

	info, err := p.deviceInfo(ctx, mac)
	if err != nil {
		return ConnectionStatus{}, err
	}
	return ConnectionStatus{Paired: info.Paired, Trusted: info.Trusted, Connected: info.Connected}, nil
}

// fullConnectionStatus ports _get_full_connection_status: currentStatus
// plus the default route interface, for the web UI's extra display.
func (p *Plugin) fullConnectionStatus(ctx context.Context, mac string) (ConnectionStatus, error) {
	status, err := p.currentStatus(ctx, mac)
	if err != nil {
		return status, err
	}
	if iface, rerr := p.defaultRouteInterface(ctx); rerr == nil {
		status.DefaultRouteInterface = iface
	}
	return status, nil
}

// httpGetRequest builds a real GET request against url with ctx applied.
func httpGetRequest(ctx context.Context, url string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}
