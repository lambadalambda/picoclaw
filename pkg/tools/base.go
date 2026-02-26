package tools

import "context"

const toolCallDescriptionParameter = "description"

func withToolCallDescriptionParameter(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		schema = map[string]interface{}{}
	}

	clonedSchema := make(map[string]interface{}, len(schema)+1)
	for k, v := range schema {
		clonedSchema[k] = v
	}

	properties, _ := schema["properties"].(map[string]interface{})
	clonedProperties := make(map[string]interface{}, len(properties)+1)
	for k, v := range properties {
		clonedProperties[k] = v
	}

	if _, exists := clonedProperties[toolCallDescriptionParameter]; !exists {
		clonedProperties[toolCallDescriptionParameter] = map[string]interface{}{
			"type":        "string",
			"description": "Very short note about what this tool call is doing (shown to the user in live progress).",
			"maxLength":   80,
		}
	}

	clonedSchema["type"] = "object"
	clonedSchema["properties"] = clonedProperties

	return clonedSchema
}

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

func ToolToSchema(tool Tool) map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  withToolCallDescriptionParameter(tool.Parameters()),
		},
	}
}
