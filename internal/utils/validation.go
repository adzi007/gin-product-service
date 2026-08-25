package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

type CustomFieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func ParseBindingError(err error) (string, []CustomFieldError) {
	var fieldErrors []CustomFieldError

	// 1. Tag validation failures
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		for _, fe := range ve {
			fieldErrors = append(fieldErrors, CustomFieldError{
				Field:   toSnakeCase(fe.Field()),
				Message: formatValidationMessage(fe),
			})
		}
		return "validation failed", fieldErrors
	}

	// 2. Type mismatches (e.g., string passed to int field)
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		fieldErrors = append(fieldErrors, CustomFieldError{
			Field:   ute.Field,
			Message: fmt.Sprintf("invalid value or format for expected type '%s'", ute.Type.String()),
		})
		return "type mismatch error", fieldErrors
	}

	// 3. Custom Unmarshaler errors (e.g., invalid UUID string passed to NullableUUID)
	if strings.Contains(err.Error(), "invalid UUID") {
		return "invalid UUID format", []CustomFieldError{
			{
				Field:   "uuid",
				Message: err.Error(),
			},
		}
	}

	return "invalid JSON payload", nil
}

func formatValidationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "field is required"
	case "gt":
		return fmt.Sprintf("must be greater than %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	default:
		return fmt.Sprintf("failed on '%s' tag validation", fe.Tag())
	}
}

func toSnakeCase(str string) string {
	// Simple helper to convert StructField to struct_field if needed
	var result strings.Builder
	for i, r := range str {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}
