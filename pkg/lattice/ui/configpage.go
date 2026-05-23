package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// configPayload bundles the parsed config + raw YAML so the UI can show
// both a structured view (for the agentic/tone block) and the
// underlying file (textarea for everything else). PUT accepts raw YAML
// for either file.
type configPayload struct {
	Config        config.Config `json:"config"`
	ConfigRaw     string        `json:"config_raw"`
	WorkspaceRaw  string        `json:"workspace_raw"`
	ConfigPath    string        `json:"config_path"`
	WorkspacePath string        `json:"workspace_path"`
}

func (s *Server) apiConfig(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadConfigPayload()
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, payload)
}

// apiConfigPut accepts either a JSON object with `config_raw` /
// `workspace_raw` strings (the user is editing the YAML directly) or a
// structured `{config: {...}, workspace: {...}}` payload. Validation
// runs through the same config.Load / workspace.ParseYAMLFile parsers
// the CLI uses — a malformed YAML never overwrites a valid file.
func (s *Server) apiConfigPut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	var incoming struct {
		ConfigRaw    *string `json:"config_raw"`
		WorkspaceRaw *string `json:"workspace_raw"`
	}
	if err := json.Unmarshal(body, &incoming); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	if incoming.ConfigRaw != nil {
		// Validate by unmarshalling into a Config — exactly what
		// config.Load does after reading the file. Strict on duplicate
		// keys so a typo can't silently shadow a real field.
		var probe config.Config
		dec := yaml.NewDecoder(strings.NewReader(*incoming.ConfigRaw))
		dec.KnownFields(true)
		if err := dec.Decode(&probe); err != nil {
			writeJSONError(w, err, http.StatusUnprocessableEntity)
			return
		}
		if err := os.WriteFile(s.ws.ConfigPath(), []byte(*incoming.ConfigRaw), 0o644); err != nil {
			writeJSONError(w, err, http.StatusInternalServerError)
			return
		}
	}
	if incoming.WorkspaceRaw != nil {
		var probe map[string]interface{}
		if err := yaml.Unmarshal([]byte(*incoming.WorkspaceRaw), &probe); err != nil {
			writeJSONError(w, err, http.StatusUnprocessableEntity)
			return
		}
		if err := os.WriteFile(s.ws.WorkspacePath(), []byte(*incoming.WorkspaceRaw), 0o644); err != nil {
			writeJSONError(w, err, http.StatusInternalServerError)
			return
		}
	}
	// Return the new state so the UI updates without a refresh round trip.
	payload, err := s.loadConfigPayload()
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, payload)
}

func (s *Server) loadConfigPayload() (configPayload, error) {
	cfg, _ := config.Load(s.ws.LatticeDir)
	configRaw, _ := os.ReadFile(s.ws.ConfigPath())
	workspaceRaw, _ := os.ReadFile(s.ws.WorkspacePath())
	return configPayload{
		Config:        cfg,
		ConfigRaw:     string(configRaw),
		WorkspaceRaw:  string(workspaceRaw),
		ConfigPath:    s.ws.ConfigPath(),
		WorkspacePath: s.ws.WorkspacePath(),
	}, nil
}

// avoid unused-import warning when io is only referenced by ReadAll.
var _ = io.EOF

// pageConfig renders the editor. It shows the raw YAML in a textarea
// with a save button — for v0.4.0-γ this is the dependable shape; the
// schema-driven form generator is a v0.4.1 follow-up.
func (s *Server) pageConfig(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadConfigPayload()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "config.html", pageData{
		Title:    "Configuration",
		Active:   "config",
		JSONHref: "/api/v1/config",
		Breadcrumbs: []crumb{{Label: "Overview", Href: "/"}, {Label: "Configuration"}},
		Body: map[string]interface{}{
			"ConfigPath":     payload.ConfigPath,
			"ConfigRaw":      payload.ConfigRaw,
			"WorkspacePath":  payload.WorkspacePath,
			"WorkspaceRaw":   payload.WorkspaceRaw,
			"Tone":           payload.Config.Agentic.Tone,
			"LLM":            payload.Config.Agentic.LLM,
		},
	})
}

// configRelPath returns the lattice/-relative path for display.
func (s *Server) configRelPath() string {
	rel, err := filepath.Rel(s.ws.LatticeDir, s.ws.ConfigPath())
	if err != nil {
		return s.ws.ConfigPath()
	}
	return rel
}
