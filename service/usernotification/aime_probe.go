package usernotification

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// HTTPStatus is the actual response from the source deciding the aggregate,
// not a synthetic success/error code. A maintenance page can be blocked with 200.
type AimeObservation struct {
	Status     string
	HTTPStatus int
	Sources    []AimeSourceObservation
}

type AimeSourceObservation struct {
	URL        string
	FinalURL   string
	Status     string
	HTTPStatus int
}

var aimeProbeURLs = []string{
	"https://my-aime.net/",
	"https://maimaidx-eng.com/maimai-mobile/",
	"https://chunithm-net-eng.com/mobile/",
}

// ProbeAime checks public pages only. Both game entry points must return 200
// without a maintenance path or message. my-aime.net is diagnostic only.
func ProbeAime(ctx context.Context, client *http.Client) (AimeObservation, error) {
	result := AimeObservation{Status: "unknown", Sources: make([]AimeSourceObservation, len(aimeProbeURLs))}
	errs := make([]error, len(aimeProbeURLs))
	var wg sync.WaitGroup
	for i, source := range aimeProbeURLs {
		wg.Add(1)
		go func(i int, source string) {
			defer wg.Done()
			result.Sources[i], errs[i] = probeAimeSource(ctx, client, source)
		}(i, source)
	}
	wg.Wait()
	// A known maintenance page takes precedence over inconclusive sources.
	for _, source := range result.Sources[1:] {
		if source.Status == "blocked" {
			result.Status, result.HTTPStatus = "blocked", source.HTTPStatus
			return result, nil
		}
	}
	for i, source := range result.Sources[1:] {
		if source.Status != "available" {
			result.HTTPStatus = source.HTTPStatus
			return result, errs[i+1]
		}
	}
	result.Status, result.HTTPStatus = "available", result.Sources[1].HTTPStatus
	return result, nil
}

func probeAimeSource(ctx context.Context, client *http.Client, source string) (AimeSourceObservation, error) {
	result := AimeSourceObservation{URL: source, Status: "unknown"}
	entry, _ := url.Parse(source)
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Port() != "" || req.URL.User != nil {
			return http.ErrUseLastResponse
		}
		host := req.URL.Hostname()
		if host != entry.Hostname() && (entry.Hostname() == "my-aime.net" || host != "lng-tgk-aime-gw.am-all.net") {
			return http.ErrUseLastResponse
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return result, err
	}
	response, err := bounded.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.HTTPStatus, result.FinalURL = response.StatusCode, response.Request.URL.String()
	const maxBody = 512 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		if err == nil {
			err = fmt.Errorf("Aime probe body exceeds limit")
		}
		return result, err
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "text/html") {
		return result, nil
	}
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return result, err
	}
	var visible strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		if node.Type == html.TextNode {
			visible.WriteString(node.Data)
			visible.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	text := strings.ToLower(strings.Join(strings.Fields(visible.String()), " "))
	final := response.Request.URL
	maintenance := strings.Contains(strings.ToLower(final.Path), "maintenance")
	for _, marker := range []string{"maintenance", "メンテナンス", "점검"} {
		maintenance = maintenance || strings.Contains(text, marker)
	}
	if maintenance {
		result.Status = "blocked"
		return result, nil
	}
	if response.StatusCode == http.StatusOK {
		result.Status = "available"
	}
	return result, nil
}
