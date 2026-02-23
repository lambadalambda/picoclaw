package tools

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var globalArgAliases = map[string]string{
	"file_path":  "path",
	"filepath":   "path",
	"filePath":   "path",
	"chatId":     "chat_id",
	"chatID":     "chat_id",
	"workingDir": "working_dir",
	"oldText":    "old_text",
	"newText":    "new_text",
	"startLine":  "start_line",
	"maxLines":   "max_lines",
	"max_chars":  "maxChars",
}

var toolSpecificArgAliases = map[string]map[string]string{
	"message": {
		"text":      "content",
		"message":   "content",
		"target":    "chat_id",
		"target_id": "chat_id",
	},
	"exec": {
		"cwd": "working_dir",
	},
	"web_fetch": {
		"max_chars_to_extract": "maxChars",
	},
	"memory_store": {
		"text": "content",
	},
	"subagent_report": {
		"text": "content",
	},
}

func normalizeAndValidateToolArgs(tool Tool, args map[string]interface{}) (map[string]interface{}, error) {
	schema := tool.Parameters()
	properties := extractSchemaProperties(schema)
	required := extractSchemaRequired(schema)

	normalized := copyArgs(args)
	applyConfiguredAliases(tool.Name(), normalized)
	applyPropertyNameAliases(properties, normalized)

	if err := coerceArgsToSchemaTypes(properties, normalized); err != nil {
		return nil, err
	}

	if missing := findMissingRequired(required, normalized); len(missing) > 0 {
		noun := "parameter"
		if len(missing) > 1 {
			noun = "parameters"
		}
		return nil, fmt.Errorf("Missing required %s: %s. Supply correct parameters before retrying.", noun, strings.Join(missing, ", "))
	}

	return normalized, nil
}

func copyArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func extractSchemaProperties(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		return properties
	}
	return nil
}

func extractSchemaRequired(schema map[string]interface{}) []string {
	if schema == nil {
		return nil
	}
	raw, ok := schema["required"]
	if !ok {
		return nil
	}

	switch req := raw.(type) {
	case []string:
		out := make([]string, 0, len(req))
		for _, item := range req {
			name := strings.TrimSpace(item)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(req))
		for _, item := range req {
			name, ok := item.(string)
			if !ok {
				continue
			}
			name = strings.TrimSpace(name)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func applyConfiguredAliases(toolName string, args map[string]interface{}) {
	for alias, canonical := range globalArgAliases {
		applyAlias(args, alias, canonical)
	}
	if toolAliases, ok := toolSpecificArgAliases[toolName]; ok {
		for alias, canonical := range toolAliases {
			applyAlias(args, alias, canonical)
		}
	}
}

func applyPropertyNameAliases(properties map[string]interface{}, args map[string]interface{}) {
	if len(properties) == 0 {
		return
	}

	for name := range properties {
		for _, alias := range generatedPropertyAliases(name) {
			applyAlias(args, alias, name)
		}
	}
}

func generatedPropertyAliases(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	aliasSet := map[string]struct{}{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || v == name {
			return
		}
		aliasSet[v] = struct{}{}
	}

	add(snakeToCamel(name))
	add(strings.ReplaceAll(name, "_", ""))
	add(camelToSnake(name))

	aliases := make([]string, 0, len(aliasSet))
	for alias := range aliasSet {
		aliases = append(aliases, alias)
	}
	return aliases
}

func applyAlias(args map[string]interface{}, alias, canonical string) {
	if alias == "" || canonical == "" {
		return
	}
	if _, exists := args[canonical]; exists {
		return
	}

	actualKey, value, ok := findArgValue(args, alias)
	if !ok {
		return
	}
	args[canonical] = value
	if actualKey != canonical {
		delete(args, actualKey)
	}
}

func findArgValue(args map[string]interface{}, key string) (string, interface{}, bool) {
	if value, ok := args[key]; ok {
		return key, value, true
	}
	for existingKey, value := range args {
		if strings.EqualFold(existingKey, key) {
			return existingKey, value, true
		}
	}
	return "", nil, false
}

func snakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	parts := strings.Split(s, "_")
	if len(parts) == 0 {
		return s
	}
	out := strings.ToLower(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		out += strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return out
}

func camelToSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func coerceArgsToSchemaTypes(properties map[string]interface{}, args map[string]interface{}) error {
	if len(properties) == 0 || len(args) == 0 {
		return nil
	}

	for key, value := range args {
		propertyRaw, ok := properties[key]
		if !ok {
			continue
		}
		property, ok := propertyRaw.(map[string]interface{})
		if !ok {
			continue
		}
		typeName, _ := property["type"].(string)
		typeName = strings.TrimSpace(typeName)
		if typeName == "" {
			continue
		}

		coerced, changed, err := coerceArgValue(value, typeName)
		if err != nil {
			return fmt.Errorf("Invalid parameter '%s': expected %s. Supply correct parameters before retrying.", key, typeName)
		}
		if changed {
			args[key] = coerced
		}
	}

	return nil
}

func coerceArgValue(value interface{}, typeName string) (interface{}, bool, error) {
	switch typeName {
	case "string":
		switch v := value.(type) {
		case string:
			return v, false, nil
		case []byte:
			return string(v), true, nil
		case bool:
			if v {
				return "true", true, nil
			}
			return "false", true, nil
		case int:
			return strconv.Itoa(v), true, nil
		case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return fmt.Sprintf("%v", v), true, nil
		default:
			return nil, false, fmt.Errorf("unsupported string coercion")
		}
	case "integer":
		n, changed, err := coerceNumber(value)
		if err != nil {
			return nil, false, err
		}
		if math.Trunc(n) != n {
			return nil, false, fmt.Errorf("not an integer")
		}
		return n, changed, nil
	case "number":
		n, changed, err := coerceNumber(value)
		if err != nil {
			return nil, false, err
		}
		return n, changed, nil
	case "boolean":
		switch v := value.(type) {
		case bool:
			return v, false, nil
		case string:
			s := strings.ToLower(strings.TrimSpace(v))
			switch s {
			case "true", "1", "yes", "y", "on":
				return true, true, nil
			case "false", "0", "no", "n", "off":
				return false, true, nil
			default:
				return nil, false, fmt.Errorf("invalid boolean string")
			}
		case float64:
			if v == 1 {
				return true, true, nil
			}
			if v == 0 {
				return false, true, nil
			}
			return nil, false, fmt.Errorf("invalid boolean number")
		case int:
			if v == 1 {
				return true, true, nil
			}
			if v == 0 {
				return false, true, nil
			}
			return nil, false, fmt.Errorf("invalid boolean number")
		default:
			return nil, false, fmt.Errorf("unsupported boolean coercion")
		}
	case "array":
		switch v := value.(type) {
		case []interface{}:
			return v, false, nil
		case []string:
			out := make([]interface{}, 0, len(v))
			for _, item := range v {
				out = append(out, item)
			}
			return out, true, nil
		case string:
			if strings.TrimSpace(v) == "" {
				return []interface{}{}, true, nil
			}
			return []interface{}{v}, true, nil
		default:
			return nil, false, fmt.Errorf("unsupported array coercion")
		}
	default:
		return value, false, nil
	}
}

func coerceNumber(value interface{}) (float64, bool, error) {
	switch v := value.(type) {
	case float64:
		return v, false, nil
	case float32:
		return float64(v), true, nil
	case int:
		return float64(v), true, nil
	case int8:
		return float64(v), true, nil
	case int16:
		return float64(v), true, nil
	case int32:
		return float64(v), true, nil
	case int64:
		return float64(v), true, nil
	case uint:
		return float64(v), true, nil
	case uint8:
		return float64(v), true, nil
	case uint16:
		return float64(v), true, nil
	case uint32:
		return float64(v), true, nil
	case uint64:
		return float64(v), true, nil
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false, err
		}
		return n, true, nil
	default:
		return 0, false, fmt.Errorf("unsupported number coercion")
	}
}

func findMissingRequired(required []string, args map[string]interface{}) []string {
	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0)
	for _, field := range required {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		value, ok := args[field]
		if !ok || value == nil {
			missing = append(missing, field)
		}
	}
	return missing
}
