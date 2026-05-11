package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const afscpInternalServiceURL = "http://afscp-api.agentsmith-system.svc.cluster.local:8080"

func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestActiveSurfaceExposesOnlyAFSCPWorkloadMountContract(t *testing.T) {
	repoRoot := repoRootForTest(t)

	alwaysForbidden := []string{
		"v2.0.0",
		"mbos-sandbox" + "-v1",
		"metadata_url",
		"filesystem_name",
		"storage_endpoint",
		"juicefs_storage_endpoint",
		"e2e_metadata_url",
		"e2e_filesystem_name",
		"e2e_storage_endpoint",
		"file_library_id",
	}
	docsForbidden := []string{
		"api-reference-" + "v2.md",
		"agentsmith-integration-contract-" + "v2.md",
		"bin/cleaner",
		"cmd/cleaner",
		"cleaner binary",
		"direct pod-deleting cleaner",
		"root credentials",
		"bucket",
		"static pvc",
		"local workspace",
		"juicefs-workloads-pvc",
		"minio",
	}
	buildForbidden := []string{
		"build-cleaner",
		"cmd/cleaner",
		"bin/cleaner",
		"sandbox-gc",
		"images/gc",
		"--only gc",
		"manager|gc",
		"runner|gc",
		"/cleaner",
	}
	k8sForbidden := []string{
		"agentsmith-api.agentsmith-system.svc.cluster.local",
		"kind: persistentvolumeclaim",
		"juicefs-workloads-pvc",
		"juicefs-pvc.yaml",
		"minio",
		"storage_credentials_path",
		"storage_endpoint",
		"storage_bucket",
		"metadata_url",
		"filesystem_name",
		"bucket:",
		"mkdirall",
		"mount subpaths of this pvc",
		"runner-image-default",
		"ttl-seconds-default",
		"configmap-runner-image-full",
		"sandbox-workspaces",
		"namespace: sandbox\n",
		"--namespace=sandbox",
		"workdir: /workspace",
		"mountpath: /workspace",
		"allowedprefixes: [\"/workspace\"]",
		"rootprefix: /workspace",
		"defaultdest: /workspace",
		"defaultsrc: /workspace",
		"afscp-internal-base-url: \"\"",
	}
	scriptsForbidden := []string{
		"api-reference-" + "v2.md",
		"agentsmith-integration-contract-" + "v2.md",
		"v2 api",
		"minimal v2 schema",
		"removed in v2",
		"not in v2",
		"bin/cleaner",
		"cmd/cleaner",
		"cleaner is built into manager image",
		"metadata_url",
		"filesystem_name",
		"storage_endpoint",
		"file_library_id",
		"minio",
	}
	e2eForbidden := []string{
		"cleaner binary",
		"run cleaner",
		"runcleanerbin",
		"bin/cleaner",
		"cmd/cleaner",
		"corev1().pods(ns).delete(",
		"corev1().pods(suite.namespace).delete(",
		"deletecollection(",
	}
	directPodDeleteScriptForbidden := []string{
		"kubectl delete pod",
		"kubectl delete pods",
	}
	pathForbidden := []string{
		"docs/api-reference-v2.md",
		"docs/contracts/agentsmith-integration-contract-v2.md",
		"manager-service/cmd/cleaner",
		"scripts/wait-for-minio.sh",
	}

	var violations []string
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, token := range pathForbidden {
			if strings.Contains(rel, token) {
				violations = append(violations, rel+": path "+token)
			}
		}
		scope := activeContractScope(rel)
		if scope == "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := strings.ToLower(string(data))
		forbidden := alwaysForbidden
		if scope == "docs" {
			forbidden = append(forbidden, docsForbidden...)
		}
		if scope == "k8s" {
			forbidden = append(forbidden, k8sForbidden...)
		}
		if scope == "build" {
			forbidden = append(forbidden, buildForbidden...)
		}
		if scope == "e2e" {
			forbidden = append(forbidden, e2eForbidden...)
		}
		if scope == "scripts" {
			forbidden = append(forbidden, scriptsForbidden...)
			forbidden = append(forbidden, directPodDeleteScriptForbidden...)
		}
		for _, token := range forbidden {
			if strings.Contains(content, token) {
				violations = append(violations, rel+": "+token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan active contract surface: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("old raw storage contract leaked into active surface:\n%s", strings.Join(violations, "\n"))
	}
}

func TestKustomizeOverlaysConfigureAFSCPInternalBaseURL(t *testing.T) {
	repoRoot := repoRootForTest(t)
	expected := map[string]string{
		"dev":        afscpInternalServiceURL,
		"staging":    afscpInternalServiceURL,
		"production": afscpInternalServiceURL,
	}
	for overlay, url := range expected {
		t.Run(overlay, func(t *testing.T) {
			kustomizationPath := filepath.Join(repoRoot, "k8s", "overlays", overlay, "kustomization.yaml")
			data, err := os.ReadFile(kustomizationPath)
			if err != nil {
				t.Fatalf("read overlay kustomization: %v", err)
			}
			if !strings.Contains(string(data), "patches/afscp-config.yaml") {
				t.Fatalf("%s overlay must patch sandbox-config AFSCP endpoint", overlay)
			}

			patchPath := filepath.Join(repoRoot, "k8s", "overlays", overlay, "patches", "afscp-config.yaml")
			patch, err := os.ReadFile(patchPath)
			if err != nil {
				t.Fatalf("read AFSCP config patch: %v", err)
			}
			content := string(patch)
			if !strings.Contains(content, "afscp-internal-base-url: "+strconv.Quote(url)) {
				t.Fatalf("%s overlay must set non-empty AFSCP internal base URL %q", overlay, url)
			}
		})
	}
}

func TestKustomizeBaseConfiguresAFSCPInternalBaseURLDefault(t *testing.T) {
	repoRoot := repoRootForTest(t)
	configPath := filepath.Join(repoRoot, "k8s", "base", "configmap.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read base configmap: %v", err)
	}
	if !strings.Contains(string(data), "afscp-internal-base-url: "+strconv.Quote(afscpInternalServiceURL)) {
		t.Fatalf("base configmap must set non-empty AFSCP internal base URL %q", afscpInternalServiceURL)
	}
}

func TestKustomizeRendersManagerReleaseControls(t *testing.T) {
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl is required to render kustomize overlays")
	}

	repoRoot := repoRootForTest(t)
	versionBytes, err := os.ReadFile(filepath.Join(repoRoot, "manager-service", "VERSION"))
	if err != nil {
		t.Fatalf("read manager version: %v", err)
	}
	version := strings.TrimSpace(string(versionBytes))
	expectedImages := map[string]string{
		"dev":        "image: sandbox-manager:" + version,
		"staging":    "image: localhost:5001/sandbox-manager:" + version,
		"production": "image: localhost:5001/sandbox-manager:" + version,
	}

	for overlay, image := range expectedImages {
		t.Run(overlay, func(t *testing.T) {
			overlayPath := filepath.Join(repoRoot, "k8s", "overlays", overlay)
			out, err := exec.Command(kubectl, "kustomize", overlayPath).CombinedOutput()
			if err != nil {
				t.Fatalf("render %s overlay: %v\n%s", overlay, err, string(out))
			}
			rendered := string(out)
			if !strings.Contains(rendered, image) {
				t.Fatalf("%s overlay must render manager image %q", overlay, image)
			}
			if !strings.Contains(rendered, "app.kubernetes.io/version: "+version) {
				t.Fatalf("%s overlay must render app.kubernetes.io/version %q", overlay, version)
			}
			if strings.Contains(rendered, "app.kubernetes.io/version: 1.0.0") {
				t.Fatalf("%s overlay rendered stale app.kubernetes.io/version 1.0.0", overlay)
			}
			if !strings.Contains(rendered, "strategy:\n    type: Recreate") {
				t.Fatalf("%s overlay must render sandbox-manager with Recreate rollout strategy", overlay)
			}
		})
	}
}

func activeContractScope(rel string) string {
	switch {
	case rel == "Makefile" || rel == "sbx":
		return "build"
	case strings.HasSuffix(rel, "Dockerfile"):
		return "build"
	case rel == "README.md" || rel == "manager-service/README.md":
		return "docs"
	case strings.HasPrefix(rel, "k8s/") && strings.HasSuffix(rel, ".md"):
		return "docs"
	case strings.HasPrefix(rel, "docs/") && strings.HasSuffix(rel, ".md") && !strings.HasPrefix(rel, "docs/plans/"):
		return "docs"
	case strings.HasPrefix(rel, "k8s/base/") && (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")):
		return "k8s"
	case strings.HasPrefix(rel, "k8s/overlays/") && (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")):
		return "k8s"
	case strings.HasPrefix(rel, "manager-service/e2e/") && strings.HasSuffix(rel, ".go"):
		return "e2e"
	case strings.HasPrefix(rel, "manager-service/integration/") && strings.HasSuffix(rel, ".go"):
		return "integration"
	case strings.HasPrefix(rel, "manager-service/scripts/") && strings.HasSuffix(rel, ".sh"):
		return "scripts"
	case strings.HasPrefix(rel, "manager-service/internal/") && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go"):
		return "runtime"
	case strings.HasPrefix(rel, "manager-service/cmd/") && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go"):
		return "runtime"
	case strings.HasPrefix(rel, "scripts/") && strings.HasSuffix(rel, ".sh"):
		return "scripts"
	case strings.HasPrefix(rel, "k8s/scripts/") && strings.HasSuffix(rel, ".sh"):
		return "scripts"
	default:
		return ""
	}
}
