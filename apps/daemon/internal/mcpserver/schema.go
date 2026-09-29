package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addTool is mcp.AddTool with an open output schema (daemon ADR 0007). MCP clients keep the
// tool list they fetched when their session began, and /mcp is stateless, so a client that
// connected before a daemon update checks each result against the schema of that older build.
// The SDK closes every object (additionalProperties false), so a field added since fails that
// client's check although the call worked (issues #251, #247). Every tool goes through here;
// TestOutputSchemasAreOpen fails on one that does not.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	rt := reflect.TypeFor[Out]()
	if t.OutputSchema == nil && rt != reflect.TypeFor[any]() {
		elem := rt
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		// The SDK derives the schema with the same call when a tool declares none.
		schema, err := jsonschema.ForType(elem, &jsonschema.ForOptions{})
		if err != nil {
			panic(fmt.Sprintf("mcpserver: output schema of %s: %v", t.Name, err))
		}
		openSchema(schema)
		t.OutputSchema = schema
		if rt.Kind() == reflect.Pointer {
			// The SDK sends a typed nil as the zero value only when it derived the schema itself.
			inner := h
			h = func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
				res, out, err := inner(ctx, req, in)
				if err == nil && reflect.ValueOf(&out).Elem().IsNil() {
					out = reflect.New(elem).Interface().(Out)
				}
				return res, out, err
			}
		}
	}
	mcp.AddTool(s, t, h)
}

// openSchema drops additionalProperties false from every object schema under s, so a property
// the schema does not list is allowed. Listed properties keep their types and required stays.
func openSchema(s *jsonschema.Schema) {
	walkSchema(s, func(s *jsonschema.Schema) {
		if isFalseSchema(s.AdditionalProperties) {
			s.AdditionalProperties = nil
		}
	})
}

// closedObjects names the paths under s where an object refuses properties it does not list.
func closedObjects(s *jsonschema.Schema) []string {
	var paths []string
	var walk func(s *jsonschema.Schema, path string)
	walk = func(s *jsonschema.Schema, path string) {
		if s == nil {
			return
		}
		if isFalseSchema(s.AdditionalProperties) || isFalseSchema(s.UnevaluatedProperties) {
			paths = append(paths, path)
		}
		eachChild(s, func(name string, c *jsonschema.Schema) { walk(c, path+"/"+name) })
	}
	walk(s, "#")
	return paths
}

// walkSchema calls f on s and on every schema below it.
func walkSchema(s *jsonschema.Schema, f func(*jsonschema.Schema)) {
	if s == nil {
		return
	}
	f(s)
	eachChild(s, func(_ string, c *jsonschema.Schema) { walkSchema(c, f) })
}

// eachChild calls f on every schema s holds directly: properties, items, $defs, combinators
// and the rest, found by type so a field the library adds later is walked too.
func eachChild(s *jsonschema.Schema, f func(name string, c *jsonschema.Schema)) {
	v := reflect.ValueOf(s).Elem()
	for _, sf := range reflect.VisibleFields(v.Type()) {
		fv := v.FieldByIndex(sf.Index)
		switch c := fv.Interface().(type) {
		case *jsonschema.Schema:
			if c != nil {
				f(sf.Name, c)
			}
		case []*jsonschema.Schema:
			for i, e := range c {
				if e != nil {
					f(fmt.Sprintf("%s/%d", sf.Name, i), e)
				}
			}
		case map[string]*jsonschema.Schema:
			for k, e := range c {
				if e != nil {
					f(sf.Name+"/"+k, e)
				}
			}
		}
	}
}

// isFalseSchema reports whether s is the schema false, which matches nothing.
func isFalseSchema(s *jsonschema.Schema) bool {
	if s == nil {
		return false
	}
	b, err := json.Marshal(s)
	return err == nil && string(b) == "false"
}
