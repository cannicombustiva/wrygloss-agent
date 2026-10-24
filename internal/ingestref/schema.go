package ingestref

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/cannicombustiva/wrygloss-agent/internal/contract"
	"github.com/cannicombustiva/wrygloss-agent/spec"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validator validates JSON documents against one of the spec's schemas.
type Validator struct{ schema *jsonschema.Schema }

// NewValidator compiles the schema named by file name, e.g. "sync-request.schema.json".
func NewValidator(name string) (*Validator, error) {
	b, err := spec.FS.ReadFile("ingest/v1/" + name)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(name, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(name)
	if err != nil {
		return nil, err
	}
	return &Validator{schema: s}, nil
}

// Validate returns nil if the document conforms, otherwise the leaf errors with their JSON Pointer.
func (v *Validator) Validate(raw []byte) []contract.FieldError {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []contract.FieldError{{Pointer: "", Message: err.Error()}}
	}
	err = v.schema.Validate(inst)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []contract.FieldError{{Pointer: "", Message: err.Error()}}
	}
	var out []contract.FieldError
	var walk func(u *jsonschema.OutputUnit)
	walk = func(u *jsonschema.OutputUnit) {
		if u.Error != nil && len(u.Errors) == 0 {
			out = append(out, contract.FieldError{Pointer: u.InstanceLocation, Message: u.Error.String()})
		}
		for i := range u.Errors {
			walk(&u.Errors[i])
		}
	}
	walk(ve.BasicOutput())
	if len(out) == 0 {
		out = append(out, contract.FieldError{Pointer: "", Message: ve.Error()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pointer < out[j].Pointer })
	return out
}
