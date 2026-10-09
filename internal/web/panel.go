package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type panelDNSController interface {
	PanelDNSAutomatic() bool
	EnsurePanelAlias(context.Context, string, string) error
}

func (s *Server) panelResponse(w http.ResponseWriter, status int, data json.RawMessage) {
	if status != http.StatusOK {
		writeJSON(w, status, data)
		return
	}
	var panel config.PanelStatus
	if err := json.Unmarshal(data, &panel); err != nil {
		jsonError(w, http.StatusBadGateway, "invalid panel supervisor response")
		return
	}
	dns, ok := s.mgr.(panelDNSController)
	writeJSON(w, status, struct {
		config.PanelStatus
		DNSAutomatic bool `json:"dns_automatic"`
	}{panel, ok && dns.PanelDNSAutomatic()})
}

func (s *Server) panelStatus(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	w.Header().Set("Cache-Control", "no-store")
	status, data, err := s.launcherRequest(r.Context(), http.MethodGet, "/panel/status", nil)
	if err != nil {
		writeJSON(w, http.StatusOK, config.PanelStatus{Status: "idle", Error: "panel address editing requires a compatible separately maintained launcher"})
		return
	}
	s.panelResponse(w, status, data)
}

func (s *Server) panelChange(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	var body any
	if path == "/panel/prepare" {
		var input *config.PanelInput
		if err := decodeBody(w, r, &input); err != nil {
			return
		}
		if input == nil || (net.ParseIP(input.Hostname) == nil && !config.ValidPanelHostname(input.Hostname)) || !config.ValidPanelPort(input.Port) {
			jsonError(w, http.StatusBadRequest, "invalid panel hostname or port")
			return
		}
		body = input
	} else {
		var input *config.PanelRevision
		if err := decodeBody(w, r, &input); err != nil {
			return
		}
		if input == nil || len(input.Revision) != 64 {
			jsonError(w, http.StatusBadRequest, "invalid panel revision")
			return
		}
		body = input
		if path == "/panel/apply" {
			status, data, err := s.launcherRequest(r.Context(), http.MethodGet, "/panel/status", nil)
			if err != nil || status != http.StatusOK {
				jsonError(w, http.StatusBadGateway, "panel supervisor unavailable")
				return
			}
			var panel config.PanelStatus
			if json.Unmarshal(data, &panel) != nil || !panel.Supported || panel.Status != "prepared" || panel.Revision != input.Revision {
				jsonError(w, http.StatusConflict, "panel address changed; prepare it again")
				return
			}
			if ip := net.ParseIP(panel.Hostname); ip != nil {
				if !ip.Equal(net.ParseIP(panel.ListenIP)) {
					jsonError(w, http.StatusBadRequest, "panel IP must match the existing local listener")
					return
				}
			} else if dns, ok := s.mgr.(panelDNSController); !ok {
				jsonError(w, http.StatusNotImplemented, "panel DNS verification unavailable")
				return
			} else if err := dns.EnsurePanelAlias(r.Context(), panel.Hostname, panel.ListenIP); err != nil {
				// DNS commands and complete router configurations never reach users.
				operationError(w, err)
				return
			}
		}
	}
	status, data, err := s.launcherRequest(r.Context(), http.MethodPost, path, body)
	if err != nil {
		jsonError(w, http.StatusBadGateway, "panel supervisor unavailable")
		return
	}
	s.panelResponse(w, status, data)
}
