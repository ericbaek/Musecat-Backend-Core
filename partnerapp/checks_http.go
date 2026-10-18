package partnerapp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// CheckRunner's default client resolves and pins public addresses at dial time.
// A supplied client is a trusted executable/test dependency, never browser input.
type CheckRunner struct{ client *http.Client }

func publicCheckIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "64:ff9b::/96", "64:ff9b:1::/48", "fec0::/10", "::/96", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}

func NewCheckRunner(client *http.Client) *CheckRunner {
	if client == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 15 * time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errCheckNetwork
			}
			for _, ip := range ips {
				if !publicCheckIP(ip) {
					return nil, errCheckNetwork
				}
			}
			var last error
			for _, ip := range ips {
				conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return conn, nil
				}
				last = e
			}
			return nil, last
		}
		client = &http.Client{Transport: transport}
	}
	copy := *client
	copy.Timeout = 10 * time.Second
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CheckRunner{client: &copy}
}

// Responses and error strings are never saved: only bounded protocol result codes.
func (r *CheckRunner) call(ctx context.Context, method, target, bearer string, form url.Values, body any, requestID string, basic *Credentials) (int, []byte, error) {
	if !httpsURL(target) {
		return 0, nil, errCheckNetwork
	}
	var reader io.Reader
	contentType := "application/json"
	if form != nil {
		reader = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if reader != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if basic != nil {
		req.SetBasicAuth(url.QueryEscape(basic.ClientID), url.QueryEscape(basic.ClientSecret))
	}
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	response, err := r.client.Do(req)
	if err != nil {
		return 0, nil, errCheckNetwork
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		return response.StatusCode, nil, errCheckResponse
	}
	return response.StatusCode, data, nil
}
