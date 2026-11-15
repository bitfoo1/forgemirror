package config_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.bitfoo.de/forgemirror/internal/config"
)

// The JSON Schema is written by hand, because descriptions, examples and
// rules such as "token xor token_file" carry most of its value for editor
// users and a struct reflector cannot produce them. These tests keep it from
// drifting away from the Go types, the runtime defaults and the shipped
// example config.

const schemaFile = "../../docs/static/schema/v1/forgemirror.schema.json"

type schemaNode struct {
	Ref                  string                 `json:"$ref"`
	Description          string                 `json:"description"`
	Type                 string                 `json:"type"`
	Enum                 []string               `json:"enum"`
	Default              any                    `json:"default"`
	Properties           map[string]*schemaNode `json:"properties"`
	AdditionalProperties *bool                  `json:"additionalProperties"`
	Items                *schemaNode            `json:"items"`
}

type schemaDoc struct {
	schemaNode

	Defs map[string]*schemaNode `json:"$defs"`
}

func readSchema(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}

	return data
}

func parseSchema(t *testing.T) *schemaDoc {
	t.Helper()

	var doc schemaDoc
	if err := json.Unmarshal(readSchema(t), &doc); err != nil {
		t.Fatalf("decoding schema: %v", err)
	}

	return &doc
}

// resolve follows a local "#/$defs/<name>" reference.
func (d *schemaDoc) resolve(t *testing.T, path string, n *schemaNode) *schemaNode {
	t.Helper()

	if n.Ref == "" {
		return n
	}
	name, ok := strings.CutPrefix(n.Ref, "#/$defs/")
	def := d.Defs[name]
	if !ok || def == nil {
		t.Fatalf("%s: unresolvable $ref %q", path, n.Ref)
	}

	return def
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(readSchema(t)))
	if err != nil {
		t.Fatalf("decoding schema: %v", err)
	}
	const url = "forgemirror.schema.json"
	c := jsonschema.NewCompiler()
	if addErr := c.AddResource(url, doc); addErr != nil {
		t.Fatalf("adding schema: %v", addErr)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatalf("compiling schema: %v", err)
	}

	return sch
}

// yamlInstance converts YAML into the JSON data model the validator expects.
func yamlInstance(t *testing.T, src string) any {
	t.Helper()

	m, err := yaml.Parser().Unmarshal([]byte(src))
	if err != nil {
		t.Fatalf("parsing YAML: %v", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encoding JSON: %v", err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding JSON: %v", err)
	}

	return v
}

// TestSchemaMatchesConfigTypes catches keys that exist on one side only and
// type mismatches, e.g. after adding a field to a config struct.
func TestSchemaMatchesConfigTypes(t *testing.T) {
	t.Parallel()

	doc := parseSchema(t)
	checkSchemaType(t, doc, "", &doc.schemaNode, reflect.TypeFor[config.Config]())
}

func checkSchemaType(t *testing.T, doc *schemaDoc, path string, n *schemaNode, typ reflect.Type) {
	t.Helper()

	// Array items share the array's description.
	if path != "" && !strings.HasSuffix(path, "[]") && n.Description == "" {
		t.Errorf("%s: missing description (shown on hover in editors)", path)
	}
	n = doc.resolve(t, path, n)

	// Pointer fields mean "unset" in the config; the schema describes the value.
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ {
	case reflect.TypeFor[time.Duration]():
		if n != doc.Defs["duration"] {
			t.Errorf(`%s: duration fields must use "$ref": "#/$defs/duration"`, path)
		}

		return
	case reflect.TypeFor[slog.Level]():
		if n.Type != "string" || len(n.Enum) == 0 {
			t.Errorf("%s: log level must be a string enum", path)
		}

		return
	}

	// Only the kinds used by the config structs are mapped; the default case
	// fails the test when a new kind appears.
	//exhaustive:ignore
	switch typ.Kind() {
	case reflect.String:
		expectSchemaType(t, path, n, "string", typ)
	case reflect.Bool:
		expectSchemaType(t, path, n, "boolean", typ)
	case reflect.Int, reflect.Int64:
		expectSchemaType(t, path, n, "integer", typ)
	case reflect.Slice:
		expectSchemaType(t, path, n, "array", typ)
		if n.Items == nil {
			t.Errorf("%s: array without items", path)

			return
		}
		checkSchemaType(t, doc, path+"[]", n.Items, typ.Elem())
	case reflect.Struct:
		expectSchemaType(t, path, n, "object", typ)
		checkSchemaObject(t, doc, path, n, typ)
	default:
		t.Fatalf("%s: Go type %s has no schema mapping; extend this test", path, typ)
	}
}

func expectSchemaType(t *testing.T, path string, n *schemaNode, want string, typ reflect.Type) {
	t.Helper()

	if n.Type != want {
		t.Errorf("%s: schema type = %q, want %q (Go type %s)", path, n.Type, want, typ)
	}
}

func checkSchemaObject(t *testing.T, doc *schemaDoc, path string, n *schemaNode, typ reflect.Type) {
	t.Helper()

	// Mirrors ErrorUnused in the loader: unknown keys must be flagged in the
	// editor just as they fail at startup.
	if n.AdditionalProperties == nil || *n.AdditionalProperties {
		t.Errorf("%s: objects must set additionalProperties: false", path)
	}

	fields := make(map[string]reflect.Type)
	for f := range typ.Fields() {
		if key := f.Tag.Get("koanf"); f.IsExported() && key != "" {
			fields[key] = f.Type
		}
	}

	for key, ft := range fields {
		child := strings.TrimPrefix(path+"."+key, ".")
		prop, ok := n.Properties[key]
		if !ok {
			t.Errorf("%s: missing in schema", child)

			continue
		}
		checkSchemaType(t, doc, child, prop, ft)
	}
	for _, key := range slices.Sorted(maps.Keys(n.Properties)) {
		if _, ok := fields[key]; !ok {
			t.Errorf("%s: in schema but not in %s", strings.TrimPrefix(path+"."+key, "."), typ)
		}
	}
}

// TestSchemaDefaultsMatchRuntime loads a config that spells out every schema
// default explicitly and expects the same result as relying on the built-in
// defaults.
func TestSchemaDefaultsMatchRuntime(t *testing.T) {
	t.Parallel()

	doc := parseSchema(t)
	explicit := schemaDefaults(t, doc, &doc.schemaNode)

	targets := doc.resolve(t, "targets[]", doc.Properties["targets"].Items)
	explicit["webhook"].(map[string]any)["secret"] = "s"
	explicit["targets"] = []any{map[string]any{
		"name":     "t",
		"url":      "https://example.com/{{ .FullName }}.git",
		"token":    "x",
		"username": targets.Properties["username"].Default,
	}}

	// JSON is valid YAML, so the loader reads it unchanged.
	data, err := json.Marshal(explicit)
	if err != nil {
		t.Fatalf("encoding defaults: %v", err)
	}
	got, err := config.Load(config.Options{File: writeFile(t, "explicit.yaml", string(data)), Environ: environ()})
	if err != nil {
		t.Fatalf("Load(schema defaults) error = %v", err)
	}
	want, err := config.Load(config.Options{File: writeFile(t, "minimal.yaml", minimalYAML), Environ: environ()})
	if err != nil {
		t.Fatalf("Load(minimal) error = %v", err)
	}

	// Targets hold a compiled template pointer, so they are compared by field.
	gotTargets, wantTargets := got.Targets, want.Targets
	got.Targets, want.Targets = nil, nil
	// "default": [] decodes to an empty slice, the absent key to nil; both
	// mean "no branch rule".
	if len(got.Filter.IncludeBranches) == 0 && len(want.Filter.IncludeBranches) == 0 {
		got.Filter.IncludeBranches, want.Filter.IncludeBranches = nil, nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema defaults differ from runtime defaults:\nschema:  %+v\nruntime: %+v", got, want)
	}
	if gotTargets[0].Username != wantTargets[0].Username {
		t.Errorf("targets[].username default: schema %q, runtime %q", gotTargets[0].Username, wantTargets[0].Username)
	}
}

func schemaDefaults(t *testing.T, doc *schemaDoc, n *schemaNode) map[string]any {
	t.Helper()

	out := make(map[string]any)
	for key, prop := range n.Properties {
		switch {
		case prop.Default != nil:
			out[key] = prop.Default
		case prop.Type == "object":
			out[key] = schemaDefaults(t, doc, doc.resolve(t, key, prop))
		default:
		}
	}

	return out
}

func TestSchemaAcceptsShippedConfigs(t *testing.T) {
	t.Parallel()

	sch := compileSchema(t)

	example, err := os.ReadFile("../../forgemirror.example.yaml")
	if err != nil {
		t.Fatalf("reading example: %v", err)
	}
	configMap, err := os.ReadFile("../../deploy/configmap.yaml")
	if err != nil {
		t.Fatalf("reading ConfigMap: %v", err)
	}
	cm, err := yaml.Parser().Unmarshal(configMap)
	if err != nil {
		t.Fatalf("parsing ConfigMap: %v", err)
	}
	data, _ := cm["data"].(map[string]any)
	embedded, _ := data["forgemirror.yaml"].(string)

	tests := []struct {
		name string
		yaml string
	}{
		{name: "forgemirror.example.yaml", yaml: string(example)},
		{name: "deploy/configmap.yaml", yaml: embedded},
		{name: "valid test config", yaml: validYAML},
		{name: "minimal test config", yaml: minimalYAML},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.yaml == "" {
				t.Fatal("empty config")
			}
			if verr := sch.Validate(yamlInstance(t, tc.yaml)); verr != nil {
				t.Errorf("schema rejects %s:\n%v", tc.name, verr)
			}
		})
	}
}

// TestSchemaRejectsInvalid covers the rules that live only in the schema
// (enums, patterns, xor constraints); each case is also rejected by Load.
func TestSchemaRejectsInvalid(t *testing.T) {
	t.Parallel()

	sch := compileSchema(t)
	const target = "\ntargets:\n  - {name: t, url: \"https://example.com/x.git\", token: x}\n"

	tests := []struct {
		name string
		yaml string
	}{
		{name: "unknown top-level key", yaml: "servr: {}" + target},
		{name: "unknown nested key", yaml: "server: {listen_adress: \":1\"}" + target},
		{name: "no targets", yaml: "targets: []"},
		{name: "missing targets", yaml: "log: {level: info}"},
		{name: "bad listen address", yaml: "server: {listen_address: nonsense}" + target},
		{name: "bad duration", yaml: "mirror: {timeout: 10 minutes}" + target},
		{name: "bad log level", yaml: "log: {level: loud}" + target},
		{name: "bad log format", yaml: "log: {format: xml}" + target},
		{name: "zero concurrency", yaml: "mirror: {max_concurrent: 0}" + target},
		{name: "secret and secret_file", yaml: "webhook: {secret: a, secret_file: /b}" + target},
		{name: "source token and token_file", yaml: "source: {token: a, token_file: /b}" + target},
		{name: "target without token", yaml: "targets:\n  - {name: t, url: \"https://example.com/x.git\"}"},
		{
			name: "target with token and token_file",
			yaml: "targets:\n  - {name: t, url: \"https://example.com/x.git\", token: a, token_file: /b}",
		},
		{name: "target with ssh url", yaml: "targets:\n  - {name: t, url: \"ssh://git@example.com/x.git\", token: x}"},
		{
			name: "target url with credentials",
			yaml: "targets:\n  - {name: t, url: \"https://u:p@example.com/x.git\", token: x}",
		},
		{
			name: "settings private false",
			yaml: "targets:\n  - {name: t, type: github, url: \"https://github.com/a/x.git\", token: x, settings: {private: false}}",
		},
		{
			name: "unknown target type",
			yaml: "targets:\n  - {name: t, type: bitbucket, url: \"https://example.com/x.git\", token: x}",
		},
		{
			name: "override without repos",
			yaml: "targets:\n  - {name: t, type: github, url: \"https://github.com/a/x.git\", token: x, overrides: [{settings: {has_wiki: true}}]}",
		},
		{
			name: "unknown settings key",
			yaml: "targets:\n  - {name: t, type: github, url: \"https://github.com/a/x.git\", token: x, settings: {has_issue: true}}",
		},
		{name: "empty branch pattern", yaml: "filter: {include_branches: [\"\"]}" + target},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := sch.Validate(yamlInstance(t, tc.yaml)); err == nil {
				t.Errorf("schema accepts invalid config:\n%s", tc.yaml)
			}
		})
	}
}
