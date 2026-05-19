package workloadfacts

import (
	"regexp"
	"strings"
	"testing"
)

func TestK8sIdentityMappingStableLabelSafeAndCollisionResistant(t *testing.T) {
	rawA := "Workspace With Spaces/" + strings.Repeat("same-prefix-", 12) + "a"
	rawB := "Workspace With Spaces/" + strings.Repeat("same-prefix-", 12) + "b"

	nameA := ObjectName("workload", rawA)
	nameARepeat := ObjectName("workload", rawA)
	nameB := ObjectName("workload", rawB)
	labelA := LabelValue(rawA)
	labelB := LabelValue(rawB)

	assertDNSLabel(t, nameA)
	assertDNSLabel(t, labelA)
	if nameA != nameARepeat {
		t.Fatalf("object name must be stable, got %q then %q", nameA, nameARepeat)
	}
	if nameA == nameB {
		t.Fatalf("object names with equal truncated slug prefixes must keep distinct hash suffixes: %q", nameA)
	}
	if labelA == labelB {
		t.Fatalf("label values with equal truncated slug prefixes must keep distinct hash suffixes: %q", labelA)
	}
	if strings.ContainsAny(labelA, "_./ ") {
		t.Fatalf("label value must be DNS-label safe, got %q", labelA)
	}
}

func TestK8sIdentityLegacyDNSSafeObjectNameIsPreserved(t *testing.T) {
	name := ObjectName("workload", "wl-1")

	if name != "workload-wl-1" {
		t.Fatalf("DNS-safe legacy object name should be preserved, got %q", name)
	}
	assertDNSLabel(t, name)
}

func assertDNSLabel(t *testing.T, value string) {
	t.Helper()
	if len(value) == 0 || len(value) > 63 {
		t.Fatalf("value length must be 1..63, got %d for %q", len(value), value)
	}
	if !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(value) {
		t.Fatalf("value must be DNS-label safe, got %q", value)
	}
}
