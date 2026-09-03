// host_auth.go wraps the host's auth-store RPC (host.auth.list / get /
// get_bundle). These are the only paths the plugin uses to read auth files;
// writes go through hostAuthPersist / hostAuthPersistMigrate in lifecycle.go.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// rpcHostAuthListResponse mirrors the host's host.auth.list envelope result.
type rpcHostAuthListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type rpcHostAuthGetResponse struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	JSON      json.RawMessage `json:"json"`
}

// hostAuthList returns all workbuddy credentials known to the host.
func hostAuthList() ([]pluginapi.HostAuthFileEntry, error) {
	raw, err := hostCall(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		return nil, fmt.Errorf("host.auth.list: bad envelope")
	}
	var resp rpcHostAuthListResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil, err
	}
	// The host manager can temporarily retain two runtime records for one
	// physical file after a credential is refreshed/migrated. Those records
	// share auth_index and path but may have different IDs, so the dashboard
	// must collapse them before fetching account details.
	out := make([]pluginapi.HostAuthFileEntry, 0, len(resp.Files))
	seen := make(map[string]int, len(resp.Files))
	prefix := "wbglobal-"
	for _, f := range resp.Files {
		lowerName := strings.ToLower(strings.TrimSpace(f.Name))
		if !strings.HasPrefix(lowerName, prefix) && lowerName != strings.ToLower(authFileName) {
			continue
		}
		key := strings.TrimSpace(f.AuthIndex)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(f.Path))
		}
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(f.Name))
		}
		if i, exists := seen[key]; exists {
			if strings.EqualFold(strings.TrimSpace(f.ID), strings.TrimSpace(f.Name)) {
				out[i] = f
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, f)
	}
	return out, nil
}

// hostAuthGet fetches the credential JSON for one auth index.
func hostAuthGet(authIndex string) (*storedAuth, error) {
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return nil, err
	}
	return parseStored(phys.JSON)
}

// hostAuthGetBundle is one host.auth.get for both storage and physical metadata
// (avoids the previous double-RPC in dashboard: get + getPhysical).
func hostAuthGetBundle(authIndex string) (*storedAuth, *hostAuthPhysical, error) {
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return nil, nil, err
	}
	sa, err := parseStored(phys.JSON)
	if err != nil {
		return nil, phys, err
	}
	return sa, phys, nil
}
