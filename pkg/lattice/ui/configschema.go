package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// FieldSpec describes one reflectively-discovered config field for the
// v0.4.1 form generator. The frontend renders an <input> matching Kind
// and PUT-backs a flat {path: value} map; the server reassembles the
// updated Config struct and marshals it back to YAML.
//
// Reflection is over config.Config so adding a new field anywhere in
// the config package automatically surfaces in the UI form with no
// template change — the schema-driven goal from the design doc.
type FieldSpec struct {
	Path     string      `json:"path"`               // dotted: "agentic.llm.enabled"
	Label    string      `json:"label"`              // human-readable
	Kind     string      `json:"kind"`               // bool | string | int | duration | string_slice | struct
	Value    interface{} `json:"value"`
	Children []FieldSpec `json:"children,omitempty"` // only for kind=struct
}

// apiConfigSchema returns the form descriptor for the current
// configuration. GET only — mutation goes through PUT /api/v1/config
// (with raw YAML) which is what apiConfigFields produces.
func (s *Server) apiConfigSchema(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load(s.ws.LatticeDir)
	spec := buildFieldSpec(reflect.ValueOf(cfg), reflect.TypeOf(cfg), "")
	writeJSON(w, spec)
}

// apiConfigFields accepts {paths: {"a.b.c": value}} and writes back an
// updated config.yaml. The server reassembles a full Config struct
// from the current YAML, sets the requested fields, re-marshals.
// Validation is the same KnownFields-strict pipeline /api/v1/config
// uses, so a typo or wrong-type still gets HTTP 422 with a useful
// message before any file is touched.
func (s *Server) apiConfigFields(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	var req struct {
		Paths map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, err, http.StatusBadRequest)
		return
	}
	if len(req.Paths) == 0 {
		writeJSONError(w, errStr("no fields to update"), http.StatusBadRequest)
		return
	}

	// Read the current file so we can re-marshal a full Config with
	// the same shape — preserves every untouched field.
	currentRaw, _ := os.ReadFile(s.ws.ConfigPath())
	var cfg config.Config
	if len(currentRaw) > 0 {
		dec := yaml.NewDecoder(strings.NewReader(string(currentRaw)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			writeJSONError(w, err, http.StatusUnprocessableEntity)
			return
		}
	}
	for path, val := range req.Paths {
		if err := setField(&cfg, path, val); err != nil {
			writeJSONError(w, err, http.StatusUnprocessableEntity)
			return
		}
	}
	newYAML, err := yaml.Marshal(cfg)
	if err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(s.ws.ConfigPath(), newYAML, 0o644); err != nil {
		writeJSONError(w, err, http.StatusInternalServerError)
		return
	}
	// Echo the updated state so the client doesn't need a follow-up GET.
	payload, _ := s.loadConfigPayload()
	writeJSON(w, payload)
}

// buildFieldSpec walks a config.Config value and emits one FieldSpec
// per terminal field plus a parent FieldSpec per nested struct.
func buildFieldSpec(v reflect.Value, t reflect.Type, prefix string) []FieldSpec {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
		t = t.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	var out []FieldSpec
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		yamlTag := yamlFieldName(field)
		if yamlTag == "-" {
			continue
		}
		path := yamlTag
		if prefix != "" {
			path = prefix + "." + yamlTag
		}
		fv := v.Field(i)
		spec := FieldSpec{Path: path, Label: humaniseLabel(yamlTag)}
		switch fv.Kind() {
		case reflect.Bool:
			spec.Kind = "bool"
			spec.Value = fv.Bool()
		case reflect.String:
			spec.Kind = "string"
			spec.Value = fv.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			spec.Kind = "int"
			spec.Value = fv.Int()
		case reflect.Float32, reflect.Float64:
			spec.Kind = "float"
			spec.Value = fv.Float()
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.String {
				spec.Kind = "string_slice"
				vals := make([]string, fv.Len())
				for j := 0; j < fv.Len(); j++ {
					vals[j] = fv.Index(j).String()
				}
				spec.Value = vals
			} else {
				// Heterogeneous slice (e.g. []ImportCoverage) — render
				// as JSON for now; the textarea covers the edit path.
				spec.Kind = "json"
				spec.Value = fv.Interface()
			}
		case reflect.Struct:
			spec.Kind = "struct"
			spec.Children = buildFieldSpec(fv, fv.Type(), path)
		case reflect.Map:
			// Maps are rare in config; surface as opaque JSON.
			spec.Kind = "json"
			spec.Value = fv.Interface()
		default:
			continue
		}
		out = append(out, spec)
	}
	return out
}

// yamlFieldName returns the field's yaml-tag name, falling back to a
// lowercase-with-underscores form of the Go field name.
func yamlFieldName(f reflect.StructField) string {
	if tag, ok := f.Tag.Lookup("yaml"); ok {
		if comma := strings.Index(tag, ","); comma >= 0 {
			tag = tag[:comma]
		}
		if tag != "" {
			return tag
		}
	}
	return camelToSnake(f.Name)
}

// camelToSnake converts MixedCase to snake_case for fallback labels.
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// humaniseLabel turns "max_tokens" into "Max tokens" — readable in a
// form without redundant per-field annotations.
func humaniseLabel(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// setField navigates a dotted path on cfg via reflection and sets the
// terminal field's value. Strict on type — a string supplied where a
// bool is expected returns an HTTP-422-friendly error.
func setField(cfg *config.Config, path string, val interface{}) error {
	parts := strings.Split(path, ".")
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i, part := range parts {
		idx := findFieldByYAMLName(t, part)
		if idx < 0 {
			return errStr("unknown field: " + path)
		}
		if i == len(parts)-1 {
			return assignField(v.Field(idx), val, path)
		}
		v = v.Field(idx)
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return errStr("cannot descend into non-struct on path: " + path)
		}
		t = v.Type()
	}
	return nil
}

func findFieldByYAMLName(t reflect.Type, name string) int {
	for i := 0; i < t.NumField(); i++ {
		if yamlFieldName(t.Field(i)) == name {
			return i
		}
	}
	return -1
}

// assignField writes val into the reflected field, coercing JSON's
// loose types (float64 from a JSON number, []interface{} for slices)
// into the field's Go type.
func assignField(fv reflect.Value, val interface{}, path string) error {
	switch fv.Kind() {
	case reflect.Bool:
		b, ok := val.(bool)
		if !ok {
			return errStr(path + ": expected bool, got " + reflect.TypeOf(val).String())
		}
		fv.SetBool(b)
	case reflect.String:
		s, ok := val.(string)
		if !ok {
			return errStr(path + ": expected string, got " + reflect.TypeOf(val).String())
		}
		fv.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch n := val.(type) {
		case float64:
			fv.SetInt(int64(n))
		case int:
			fv.SetInt(int64(n))
		case int64:
			fv.SetInt(n)
		default:
			return errStr(path + ": expected int, got " + reflect.TypeOf(val).String())
		}
	case reflect.Float32, reflect.Float64:
		switch n := val.(type) {
		case float64:
			fv.SetFloat(n)
		case int:
			fv.SetFloat(float64(n))
		default:
			return errStr(path + ": expected float, got " + reflect.TypeOf(val).String())
		}
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return errStr(path + ": only []string slices are settable via the form (use the YAML textarea for other slices)")
		}
		arr, ok := val.([]interface{})
		if !ok {
			return errStr(path + ": expected array, got " + reflect.TypeOf(val).String())
		}
		out := reflect.MakeSlice(fv.Type(), len(arr), len(arr))
		for i, item := range arr {
			s, ok := item.(string)
			if !ok {
				return errStr(path + ": array item must be string")
			}
			out.Index(i).SetString(s)
		}
		fv.Set(out)
	default:
		return errStr(path + ": field type " + fv.Kind().String() + " not supported by the form")
	}
	return nil
}
