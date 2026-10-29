package schema_test

import (
	"testing"

	"nexora.local/nli-v20/internal/schema"
)

func TestValidate_RequiredFieldMissing(t *testing.T) {
	reg := schema.New()
	reg.Register(&schema.Schema{
		Service: "smtp",
		Fields: []schema.FieldSpec{
			{Name: "host", Type: "string", Required: true},
		},
	})

	result := reg.Validate("smtp", map[string]interface{}{
		"message": "hello",
	})
	if result.Valid {
		t.Fatal("expected invalid result when required field missing")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
}

func TestValidate_AllPresent(t *testing.T) {
	reg := schema.New()
	reg.Register(&schema.Schema{
		Service: "smtp",
		Fields: []schema.FieldSpec{
			{Name: "host", Type: "string", Required: true},
			{Name: "pid", Type: "int", Required: false},
		},
	})

	result := reg.Validate("smtp", map[string]interface{}{
		"host": "mail.example.com",
		"pid":  12345,
	})
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidate_NoSchema(t *testing.T) {
	reg := schema.New()
	result := reg.Validate("unknown_service", map[string]interface{}{"x": 1})
	if !result.Valid {
		t.Fatal("service with no schema should pass validation")
	}
}

func TestValidate_WrongType(t *testing.T) {
	reg := schema.New()
	reg.Register(&schema.Schema{
		Service: "test",
		Fields:  []schema.FieldSpec{{Name: "active", Type: "bool", Required: true}},
	})
	result := reg.Validate("test", map[string]interface{}{"active": "yes"})
	if result.Valid {
		t.Fatal("expected invalid for wrong type")
	}
}
