package providers

import "testing"

func TestModelCapabilities_GLMM5_NoVision(t *testing.T) {
	caps := ModelCapabilitiesFor("zai-org/GLM-5-FP8")
	if caps.SupportsVision {
		t.Fatal("GLM-5 should not advertise native vision support")
	}
	if caps.SupportsInlineVision {
		t.Fatal("GLM-5 should not advertise inline vision transport support")
	}
}

func TestModelCapabilities_GLM46V_Vision(t *testing.T) {
	caps := ModelCapabilitiesFor("glm-4.6v")
	if !caps.SupportsVision {
		t.Fatal("glm-4.6v should advertise native vision support")
	}
	if caps.SupportsInlineVision {
		t.Fatal("glm-4.6v should not advertise inline vision transport by default")
	}
}

func TestModelCapabilities_GLM5V_Vision(t *testing.T) {
	for _, model := range []string{"glm-5v-turbo", "zai-org/GLM-5V-Turbo"} {
		caps := ModelCapabilitiesFor(model)
		if !caps.SupportsVision {
			t.Fatalf("%s should advertise native vision support", model)
		}
		if !caps.SupportsInlineVision {
			t.Fatalf("%s should advertise inline vision transport support", model)
		}
	}
}

func TestModelCapabilities_GPT4O_InlineVision(t *testing.T) {
	caps := ModelCapabilitiesFor("gpt-4o")
	if !caps.SupportsVision {
		t.Fatal("gpt-4o should advertise native vision support")
	}
	if !caps.SupportsInlineVision {
		t.Fatal("gpt-4o should advertise inline vision transport support")
	}
}

func TestModelCapabilities_Claude_InlineVision(t *testing.T) {
	caps := ModelCapabilitiesFor("claude-opus-4-1")
	if !caps.SupportsVision {
		t.Fatal("claude models should advertise native vision support")
	}
	if !caps.SupportsInlineVision {
		t.Fatal("claude models should advertise inline vision transport support")
	}
}

func TestModelCapabilities_UnknownModel_DefaultsToNoVision(t *testing.T) {
	caps := ModelCapabilitiesFor("custom/unknown-model")
	if caps.SupportsVision {
		t.Fatal("unknown models should default to no vision support")
	}
}
