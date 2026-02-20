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
		t.Fatal("inline vision transport is not wired yet")
	}
}

func TestModelCapabilities_UnknownModel_DefaultsToNoVision(t *testing.T) {
	caps := ModelCapabilitiesFor("custom/unknown-model")
	if caps.SupportsVision {
		t.Fatal("unknown models should default to no vision support")
	}
}
