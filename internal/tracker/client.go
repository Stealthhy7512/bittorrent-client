package tracker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) Announce(
	ctx context.Context,
	trackerURL string,
	r AnnounceRequest,
) (AnnounceResponse, error) {
	announceURL, err := buildTrackerURL(trackerURL, r)
	if err != nil {
		return AnnounceResponse{}, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		announceURL,
		nil,
	)
	if err != nil {
		return AnnounceResponse{}, err
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	res, err := client.Do(req)
	if err != nil {
		return AnnounceResponse{}, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return AnnounceResponse{}, fmt.Errorf(
			"tracker returned %d: %s",
			res.StatusCode,
			http.StatusText(res.StatusCode),
		)
	}

	return decodeAnnounceResponse(io.LimitReader(res.Body, 2<<20))
}

func buildTrackerURL(trackerURL string, r AnnounceRequest) (string, error) {
	base, err := url.Parse(trackerURL)
	if err != nil {
		return "", err
	}

	keys := base.Query()
	keys.Set("info_hash", string(r.InfoHash[:]))
	keys.Set("peer_id", string(r.PeerID[:]))
	keys.Set("port", strconv.FormatUint(uint64(r.Port), 10))
	keys.Set("uploaded", strconv.FormatUint(uint64(r.Uploaded), 10))
	keys.Set("downloaded", strconv.FormatUint(uint64(r.Downloaded), 10))
	keys.Set("left", strconv.FormatUint(uint64(r.Left), 10))

	if r.Compact {
		keys.Set("compact", "1")
	} else {
		keys.Set("compact", "0")
	}
	if r.Event != "" {
		keys.Set("event", string(r.Event))
	}

	base.RawQuery = keys.Encode()
	return base.String(), nil
}
