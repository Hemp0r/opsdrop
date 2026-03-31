package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// FetchCapabilities retrieves server capabilities from the well-known endpoint.
func (c *Client) FetchCapabilities(ctx context.Context) (*ServerCapabilities, error) {
	req, err := c.newRequest(ctx, "GET", "/.well-known/opsdrop-capabilities", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req) // bypass c.do to handle non-JSON 404 gracefully
	if err != nil {
		return nil, fmt.Errorf("fetch capabilities: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("capability endpoint returned %s", resp.Status)
	}
	var caps ServerCapabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return nil, fmt.Errorf("decode capabilities: %w", err)
	}
	return &caps, nil
}

// SaveCapabilities stores fetched capabilities in the config and persists to disk.
func SaveCapabilities(cfg *Config, caps *ServerCapabilities) error {
	cfg.Capabilities = caps
	cfg.CapabilitiesFetchedAt = time.Now().UTC().Format(time.RFC3339)
	return SaveConfig(cfg)
}
