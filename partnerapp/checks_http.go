package partnerapp

import (
	"context"
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/connections"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// CheckRunner's default client resolves and pins public addresses at dial time.
// A supplied client is a trusted executable/test dependency, never browser input.
type CheckRunner struct{ client *http.Client }

func NewCheckRunner(client *http.Client) *CheckRunner {
	return &CheckRunner{client: connections.NewPartnerHTTPClient(client)}
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
