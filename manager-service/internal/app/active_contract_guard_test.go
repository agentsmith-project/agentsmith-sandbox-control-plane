package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const afscpInternalServiceURL = "http://afscp-api.agentsmith-system.svc.cluster.local:8080"
const asbcpServiceName = "agentsmith-sandbox-control-plane"
const asbcpConfigPath = "/etc/asbcp/asbcp-config.yaml"

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
		"/v1/sandboxes",
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

func TestActiveSurfaceUsesCanonicalASBCPIdentifiers(t *testing.T) {
	repoRoot := repoRootForTest(t)

	forbidden := []string{
		"github.com/sandbox/manager",
		"SANDBOX_MANAGER",
		"SANDBOX_SERVICE_KEY",
		"sandbox-manager",
		"agentsmith-sandbox-manager",
		"sandbox manager",
		"/etc/sandbox-manager",
		"manager-config.example.yaml",
		"manager-config.yaml",
		"manager-configmap",
		"cmd/manager",
		"svc/sandbox-manager",
		"sandbox-manager-secrets",
		"test-manager.sh",
		"build-manager",
		"E2E_MANAGER_",
		"ManagerURL",
		"ManagerBin",
		"ASBCP_" + "STORAGE_CAPACITY",
		"ASBCP_" + "STORAGE_CLASS_NAME",
	}
	forbiddenPaths := []string{
		"k8s/base/manager-configmap.yaml",
		"k8s/base/manager-deployment.yaml",
		"k8s/base/manager-service.yaml",
		"k8s/base/rbac-manager.yaml",
		"k8s/overlays/dev/patches/manager-replicas.yaml",
		"k8s/overlays/staging/patches/manager-replicas.yaml",
		"k8s/overlays/production/patches/manager-replicas.yaml",
		"manager-service/manager-config.example.yaml",
		"manager-service/scripts/test-manager.sh",
	}
	required := map[string][]string{
		"Makefile": {
			"build-asbcp:",
			"./cmd/asbcp",
			"svc/agentsmith-sandbox-control-plane",
			"scripts/test-asbcp-api.sh",
		},
		"manager-service/go.mod": {
			"module github.com/agentsmith-project/agentsmith-sandbox-control-plane",
		},
		"manager-service/README.md": {
			"AgentSmith Sandbox Control Plane (ASBCP)",
			"asbcp-config.example.yaml",
			asbcpConfigPath,
			"cmd/asbcp",
			"scripts/test-asbcp-api.sh",
		},
		"docs/configuration.md": {
			asbcpConfigPath,
			"ASBCP_JUICEFS_STORAGE_CAPACITY",
			"ASBCP_JUICEFS_STORAGE_CLASS_NAME",
		},
		"manager-service/internal/app/app.go": {
			"ASBCP_JUICEFS_STORAGE_CAPACITY",
			"ASBCP_JUICEFS_STORAGE_CLASS_NAME",
		},
		"manager-service/e2e/suite_test.go": {
			"E2E_ASBCP_URL",
			"E2E_ASBCP_BIN",
			"E2E_ASBCP_PORT",
			"ASBCPURL",
			"ASBCPBin",
			"ASBCP_JUICEFS_STORAGE_CAPACITY",
			"ASBCP_JUICEFS_STORAGE_CLASS_NAME",
		},
		"manager-service/asbcp-config.example.yaml": {
			"version: 1",
		},
		"k8s/base/README.md": {
			"asbcp-configmap.yaml",
			"agentsmith-sandbox-control-plane-secrets",
			"ASBCP",
		},
		"k8s/base/kustomization.yaml": {
			"asbcp-configmap.yaml",
			"rbac-asbcp.yaml",
			"asbcp-deployment.yaml",
			"asbcp-service.yaml",
		},
		"k8s/base/configmap.yaml": {
			`afscp-caller-service: "` + asbcpServiceName + `"`,
			`afscp-actor-id: "` + asbcpServiceName + `"`,
		},
		"k8s/base/asbcp-deployment.yaml": {
			`name: ` + asbcpServiceName,
			`app.kubernetes.io/component: asbcp`,
			`name: ASBCP_CONFIG_PATH`,
			`name: ASBCP_SERVICE_KEYS`,
			`name: ASBCP_WORKLOAD_NAMESPACE`,
			`name: ASBCP_AFSCP_INTERNAL_BASE_URL`,
			`name: ASBCP_AFSCP_ORCHESTRATOR_TOKEN`,
			`name: ASBCP_JUICEFS_STORAGE_CAPACITY`,
			`name: ASBCP_JUICEFS_STORAGE_CLASS_NAME`,
			`mountPath: ` + asbcpConfigPath,
			`name: asbcp`,
		},
		"k8s/base/asbcp-service.yaml": {
			`name: ` + asbcpServiceName,
			`app.kubernetes.io/component: asbcp`,
		},
	}

	var violations []string
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !asbcpRenameScope(rel) {
			return nil
		}
		for _, token := range forbiddenPaths {
			if rel == token {
				violations = append(violations, rel+": path "+token)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		for _, token := range forbidden {
			if strings.Contains(content, token) {
				violations = append(violations, rel+": "+token)
			}
		}
		if want, ok := required[rel]; ok {
			for _, token := range want {
				if !strings.Contains(content, token) {
					violations = append(violations, rel+": missing "+token)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan ASBCP identifiers: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("ASBCP canonical identifier violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestReleaseGateIsSingleAuthority(t *testing.T) {
	repoRoot := repoRootForTest(t)
	makefilePath := filepath.Join(repoRoot, "Makefile")
	makefile, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	content := string(makefile)

	stanza := makeTargetStanza(content, "release-gate")
	if stanza == "" {
		t.Fatalf("Makefile must keep release-gate as a wrapper for operator muscle memory")
	}
	required := []string{
		"bash",
		"scripts/verify-release.sh",
	}
	var violations []string
	for _, token := range required {
		if !strings.Contains(stanza, token) {
			violations = append(violations, "release-gate target missing "+token)
		}
	}
	forbidden := []string{
		"lint test-race _check-coverage _build-check",
		"Release gate PASSED",
	}
	for _, token := range forbidden {
		if strings.Contains(stanza, token) {
			violations = append(violations, "release-gate target bypasses scripts/verify-release.sh via "+token)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("release gate authority violations:\n%s", strings.Join(violations, "\n"))
	}
}

func makeTargetStanza(makefile string, target string) string {
	lines := strings.Split(makefile, "\n")
	var out []string
	inTarget := false
	prefix := target + ":"
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			inTarget = true
			out = append(out, line)
			continue
		}
		if inTarget && line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			break
		}
		if inTarget {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func shellFunctionStanza(script string, functionName string) string {
	lines := strings.Split(script, "\n")
	var out []string
	inFunction := false
	prefix := functionName + "() {"
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			inFunction = true
			out = append(out, line)
			continue
		}
		if inFunction {
			out = append(out, line)
			if line == "}" {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestReleaseVerifierCoversBlockingReleaseEvidence(t *testing.T) {
	repoRoot := repoRootForTest(t)
	scriptPath := filepath.Join(repoRoot, "scripts", "verify-release.sh")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read release verifier: %v", err)
	}
	scriptContent := string(script)
	requiredVerifierFunctions := []string{
		"check_version_tag_contract",
		"check_changelog_release_evidence",
		"emit_changelog_release_evidence",
		"--changelog-evidence-json",
		"CHANGELOG.md",
		"### Breaking Changes",
		"Missing CHANGELOG.md release section",
		"empty Breaking Changes subsection",
		"emit_risk_status_evidence",
		"--risk-status-json",
		"docs/RISK_REGISTER.md",
		"release_blocking",
		"check_risk_status_evidence",
		"check_release_workflow_lock_output",
		"check_final_manifest_schema",
		"check_readiness_evidence",
		"check_dockerfile_contract",
		"run_release_fixture_smoke",
		"render_kustomize_overlays",
		"build_release_image",
	}
	var violations []string
	for _, token := range requiredVerifierFunctions {
		if !strings.Contains(scriptContent, token) {
			violations = append(violations, "scripts/verify-release.sh missing "+token)
		}
	}

	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "release-manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read release evidence manifest: %v", err)
	}
	var manifest struct {
		FinalManifestSchema struct {
			SchemaID                   string              `json:"schema_id"`
			Asset                      string              `json:"asset"`
			ImageIdentityEvidenceField string              `json:"image_identity_evidence_field"`
			RequiredFields             []string            `json:"required_fields"`
			NestedRequiredFields       map[string][]string `json:"nested_required_fields"`
		} `json:"final_manifest_schema"`
		RequiredEvidence []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			Rationale string `json:"rationale"`
		} `json:"required_evidence"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse release evidence manifest: %v", err)
	}
	for _, evidence := range manifest.RequiredEvidence {
		switch evidence.Status {
		case "added", "workflow-gated":
		case "deferred":
			violations = append(violations, evidence.ID+": deferred evidence must not pass full release readiness")
		case "pending":
			violations = append(violations, evidence.ID+": pending evidence must not pass release readiness")
		default:
			violations = append(violations, evidence.ID+": unsupported evidence status "+evidence.Status)
		}
	}

	if manifest.FinalManifestSchema.SchemaID != "https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json" {
		violations = append(violations, "final_manifest_schema.schema_id must be https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json")
	}
	if manifest.FinalManifestSchema.Asset != "asbcp-final-manifest.json" {
		violations = append(violations, "final_manifest_schema.asset must be asbcp-final-manifest.json")
	}
	if manifest.FinalManifestSchema.ImageIdentityEvidenceField != "same_digest_proof" {
		violations = append(violations, "final_manifest_schema.image_identity_evidence_field must be same_digest_proof")
	}
	requiredFinalManifestFields := []string{
		"schema_id",
		"asbcp_version",
		"git_tag",
		"commit_sha",
		"image_ref",
		"image_digest",
		"api_contract_version",
		"anonymous_pull",
		"same_digest_proof",
		"known_breaking_changes",
		"changelog_summary",
		"known_risk_status",
		"known_risk_status_source",
		"runbook_url",
		"release_notes",
	}
	declaredFinalManifestFields := map[string]bool{}
	for _, field := range manifest.FinalManifestSchema.RequiredFields {
		declaredFinalManifestFields[field] = true
	}
	for _, field := range requiredFinalManifestFields {
		if !declaredFinalManifestFields[field] {
			violations = append(violations, "final_manifest_schema.required_fields missing "+field)
		}
	}
	requiredNestedFields := map[string][]string{
		"anonymous_pull": {
			"result",
			"tag_ref",
			"image_ref",
			"tag_resolved_digest",
			"build_push_digest",
			"anonymous_digest",
			"docker_config",
			"commands",
		},
		"same_digest_proof": {
			"tag_resolved_digest",
			"build_push_digest",
			"anonymous_digest",
			"matches",
			"source",
		},
		"release_notes": {
			"body_source",
			"github_release_url",
		},
	}
	for objectName, fields := range requiredNestedFields {
		declared := map[string]bool{}
		for _, field := range manifest.FinalManifestSchema.NestedRequiredFields[objectName] {
			declared[field] = true
		}
		for _, field := range fields {
			if !declared[field] {
				violations = append(violations, "final_manifest_schema.nested_required_fields."+objectName+" missing "+field)
			}
		}
	}
	forbiddenNestedFields := map[string][]string{
		"anonymous_pull":    {"pulled_digest", "command"},
		"same_digest_proof": {"anonymous_pull_digest"},
		"release_notes":     {"asset", "body_path"},
	}
	for objectName, fields := range forbiddenNestedFields {
		declared := map[string]bool{}
		for _, field := range manifest.FinalManifestSchema.NestedRequiredFields[objectName] {
			declared[field] = true
		}
		for _, field := range fields {
			if declared[field] {
				violations = append(violations, "final_manifest_schema.nested_required_fields."+objectName+" contains stale "+field)
			}
		}
	}

	if len(violations) > 0 {
		t.Fatalf("release verifier coverage violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestReleaseVersionBumpKeepsPublishedV204Immutable(t *testing.T) {
	repoRoot := repoRootForTest(t)

	versionBytes, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if version := strings.TrimSpace(string(versionBytes)); version != "2.0.5" {
		t.Fatalf("VERSION must be bumped to 2.0.5 for the post-v2.0.4 clean-cut release, got %q", version)
	}

	changelogBytes, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	changelog := string(changelogBytes)
	section205 := changelogSection(changelog, "v2.0.5")
	if section205 == "" {
		t.Fatalf("CHANGELOG.md must add a v2.0.5 release section instead of retconning v2.0.4")
	}
	required205 := []string{
		"release evidence schema",
		"anonymous `docker pull`",
		"production overlay is internal-only by default",
		"old active smoke cleanup",
		"pre-GA clean cut",
	}
	var violations []string
	for _, token := range required205 {
		if !strings.Contains(section205, token) {
			violations = append(violations, "v2.0.5 changelog missing "+token)
		}
	}

	section204 := changelogSection(changelog, "v2.0.4")
	if section204 == "" {
		t.Fatalf("CHANGELOG.md must keep the published v2.0.4 section")
	}
	forbidden204 := []string{
		"same_digest_proof",
		"production overlay is internal-only by default",
		"old active smoke cleanup",
	}
	for _, token := range forbidden204 {
		if strings.Contains(section204, token) {
			violations = append(violations, "published v2.0.4 section was retconned with "+token)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("release version/changelog violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestQuickReleaseVerifierDoesNotTreatBranchRefAsReleaseTag(t *testing.T) {
	repoRoot := repoRootForTest(t)
	cmd := exec.Command("bash", "scripts/verify-release.sh", "--quick")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"GITHUB_REF_NAME=main",
		"GITHUB_REF_TYPE=branch",
		"GITHUB_REF=refs/heads/main",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quick verifier must not enforce release tag contract on branch refs: %v\n%s", err, string(output))
	}
}

func TestCanonicalASBCPConfigPathIsConsistent(t *testing.T) {
	repoRoot := repoRootForTest(t)
	scanRoots := []string{
		"README.md",
		"docs",
		"manager-service",
		"k8s",
	}
	forbidden := "/etc/asbcp/config.yaml"
	var violations []string
	for _, rel := range scanRoots {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", rel, err)
		}
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			if strings.Contains(string(data), forbidden) {
				violations = append(violations, rel+": "+forbidden)
			}
			continue
		}
		err = filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(entry.Name())
			if ext != ".md" && ext != ".go" && ext != ".sh" && ext != ".yaml" && ext != ".yml" && ext != ".example" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), forbidden) {
				scanRel, err := filepath.Rel(repoRoot, path)
				if err != nil {
					return err
				}
				if filepath.ToSlash(scanRel) == "manager-service/internal/app/active_contract_guard_test.go" {
					return nil
				}
				violations = append(violations, filepath.ToSlash(scanRel)+": "+forbidden)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", rel, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("ASBCP config path must be %s:\n%s", asbcpConfigPath, strings.Join(violations, "\n"))
	}
}

func TestDockerfileReleaseContract(t *testing.T) {
	repoRoot := repoRootForTest(t)
	dockerfilePath := filepath.Join(repoRoot, "manager-service", "Dockerfile")
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	content := string(data)
	required := []string{
		"ARG VERSION=dev",
		"ARG VCS_REF=unknown",
		"ARG BUILD_DATE=unknown",
		`ARG ASBCP_BUILD_HTTP_PROXY=""`,
		`ARG ASBCP_BUILD_HTTPS_PROXY=""`,
		`ARG ASBCP_BUILD_NO_PROXY=""`,
		"org.opencontainers.image.title",
		"org.opencontainers.image.version",
		"org.opencontainers.image.revision",
		"org.opencontainers.image.created",
		`-o asbcp ./cmd/asbcp`,
		"USER 10001",
		`CMD ["./asbcp"]`,
	}
	var violations []string
	for _, token := range required {
		if !strings.Contains(content, token) {
			violations = append(violations, "Dockerfile missing "+token)
		}
	}
	forbidden := []string{
		"cmd/manager",
		"sandbox-manager",
		"USER root",
		"ARG HTTP_PROXY",
		"ARG HTTPS_PROXY",
		"ARG http_proxy",
		"ARG https_proxy",
		"ARG NO_PROXY",
		"ARG no_proxy",
		"ENV HTTP_PROXY",
		"ENV HTTPS_PROXY",
		"ENV http_proxy",
		"ENV https_proxy",
		"ENV NO_PROXY",
		"ENV no_proxy",
		`HTTP_PROXY="${HTTP_PROXY}"`,
		`HTTPS_PROXY="${HTTPS_PROXY}"`,
		`http_proxy="${http_proxy}"`,
		`https_proxy="${https_proxy}"`,
		`NO_PROXY="${NO_PROXY}"`,
		`no_proxy="${no_proxy}"`,
		"192.168.",
		"pullot",
		"mirrors.tuna.tsinghua.edu.cn",
		"mirrors.aliyun.com",
		"mirrors.cloud.tencent.com",
		"/etc/apt/sources.list",
	}
	for _, token := range forbidden {
		if strings.Contains(content, token) {
			violations = append(violations, "Dockerfile contains forbidden "+token)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("Dockerfile release contract violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestReleaseGateBuildClearsHostProxyDefaults(t *testing.T) {
	repoRoot := repoRootForTest(t)
	scriptPath := filepath.Join(repoRoot, "scripts", "verify-release.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read release verifier: %v", err)
	}
	content := string(data)
	buildReleaseImage := shellFunctionStanza(content, "build_release_image")
	if buildReleaseImage == "" {
		t.Fatalf("release verifier missing build_release_image function")
	}
	required := []string{
		`asbcp_build_http_proxy="${ASBCP_BUILD_HTTP_PROXY:-}"`,
		`asbcp_build_https_proxy="${ASBCP_BUILD_HTTPS_PROXY:-}"`,
		`asbcp_build_no_proxy="${ASBCP_BUILD_NO_PROXY:-}"`,
		"--build-arg \"ASBCP_BUILD_HTTP_PROXY=${asbcp_build_http_proxy}\"",
		"--build-arg \"ASBCP_BUILD_HTTPS_PROXY=${asbcp_build_https_proxy}\"",
		"--build-arg \"ASBCP_BUILD_NO_PROXY=${asbcp_build_no_proxy}\"",
		"--build-arg \"HTTP_PROXY=\"",
		"--build-arg \"HTTPS_PROXY=\"",
		"--build-arg \"http_proxy=\"",
		"--build-arg \"https_proxy=\"",
		"--build-arg \"NO_PROXY=\"",
		"--build-arg \"no_proxy=\"",
		"image must not persist proxy env",
		"ASBCP_BUILD_HTTP_PROXY=*|ASBCP_BUILD_HTTPS_PROXY=*|ASBCP_BUILD_NO_PROXY=*|HTTP_PROXY=*|HTTPS_PROXY=*|http_proxy=*|https_proxy=*|NO_PROXY=*|no_proxy=*",
	}
	forbidden := []string{
		"env -u HTTP_PROXY",
		"env -u HTTPS_PROXY",
		"env -u http_proxy",
		"env -u https_proxy",
		"env -u ALL_PROXY",
		"env -u all_proxy",
	}
	var violations []string
	for _, token := range required {
		if !strings.Contains(buildReleaseImage, token) {
			violations = append(violations, "release gate build missing empty Dockerfile proxy build arg "+token)
		}
	}
	for _, token := range forbidden {
		if strings.Contains(buildReleaseImage, token) {
			violations = append(violations, "release gate must not clear Docker/BuildKit host proxy env with "+token)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("release gate proxy boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestReleaseWorkflowEmitsAgentSmithLockFieldsAndValidatesTag(t *testing.T) {
	repoRoot := repoRootForTest(t)
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "release.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	content := string(data)
	required := []string{
		"Validate release version",
		"Run authoritative release gate",
		`v${version}`,
		"steps.version.outputs.version",
		"steps.version.outputs.image_tag",
		`ASBCP_VERSION: ${{ steps.version.outputs.image_tag }}`,
		"ASBCP_GIT_TAG",
		"asbcp_version=",
		"asbcp_source_image=",
		"asbcp_release_url=",
		"asbcp_commit_sha=",
		"steps.build.outputs.digest",
		`asbcp_version={os.environ["ASBCP_VERSION"]}`,
	}
	var violations []string
	for _, token := range required {
		if !strings.Contains(content, token) {
			violations = append(violations, "release workflow missing "+token)
		}
	}
	forbidden := []string{
		"AgentSmith consumer adoption",
		"AgentSmith release gate",
		"AgentSmith release gates",
		`ASBCP_VERSION: ${{ steps.version.outputs.version }}`,
	}
	for _, token := range forbidden {
		if strings.Contains(content, token) {
			violations = append(violations, "release workflow must not gate ASBCP release on "+token)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("release workflow version/digest lock contract violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestReleaseWorkflowPublishesFinalManifestAfterAnonymousInspect(t *testing.T) {
	repoRoot := repoRootForTest(t)
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "release.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	content := string(data)
	required := []string{
		"Run authoritative release gate",
		"Build and push ASBCP image",
		"Verify anonymous digest pull",
		"Generate final manifest",
		"Create GitHub Release",
		"asbcp-final-manifest.json",
		"asbcp-release-notes.md",
		"body_path:",
		"fail_on_unmatched_files: true",
		"files:",
		`"schema_id"`,
		`"asbcp_version"`,
		`"git_tag"`,
		`"commit_sha"`,
		`"image_ref"`,
		`"image_digest"`,
		`"api_contract_version"`,
		`"anonymous_pull"`,
		`"same_digest_proof"`,
		`"known_breaking_changes"`,
		`"changelog_summary"`,
		`"known_risk_status"`,
		`"known_risk_status_source"`,
		`"runbook_url"`,
		`"release_notes"`,
		`"body_source"`,
		"TAG_RESOLVED_DIGEST",
		"ANONYMOUS_DIGEST",
		"SAME_DIGEST_MATCH",
		"fresh-empty",
		"--changelog-evidence-json",
		"dist/asbcp-changelog-evidence.json",
		"--risk-status-json",
		"dist/asbcp-risk-status.json",
		`"schema_id": "https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json"`,
		`"tag_ref"`,
		`"tag_resolved_digest"`,
		`"build_push_digest"`,
		`"anonymous_digest"`,
		`changelog_evidence["known_breaking_changes"]`,
		`Changelog summary: {changelog_evidence["changelog_summary"]}`,
		`risk_status_evidence["known_risk_status"]`,
		`risk_status_evidence["source"]`,
		`Path("dist/asbcp-release-notes.md").write_text(manifest["release_notes"]["body_source"]`,
	}
	var violations []string
	for _, token := range required {
		if !strings.Contains(content, token) {
			violations = append(violations, "release workflow missing final manifest token "+token)
		}
	}
	forbidden := []string{
		"\n      - name: Verify digest pull\n",
		`"public_inspect_result"`,
		`"version": os.environ["ASBCP_VERSION"]`,
		`"tag": os.environ["ASBCP_TAG"]`,
		`"commit": os.environ["GITHUB_SHA"]`,
		"BREAKING_CHANGES",
		`os.environ["BREAKING_CHANGES"]`,
		`"asset": "asbcp-release-notes.md"`,
		`"body_source": "dist/asbcp-release-notes.md"`,
		"pulled_digest",
		"anonymous_pull_digest",
		"ANONYMOUS_PULL_DIGEST",
		"docker manifest inspect",
		"runtime_evidence_field",
		"def parse_known_breaking_changes",
		"def parse_changelog_summary",
	}
	for _, token := range forbidden {
		if strings.Contains(content, token) {
			violations = append(violations, "release workflow final manifest must not use stale token "+strconv.Quote(token))
		}
	}

	sequence := []string{
		"Run authoritative release gate",
		"Build and push ASBCP image",
		"Verify anonymous digest pull",
		"Generate final manifest",
		"Create GitHub Release",
	}
	previousIndex := -1
	for _, token := range sequence {
		index := strings.Index(content, token)
		if index == -1 {
			violations = append(violations, "release workflow missing ordered step "+token)
			continue
		}
		if index <= previousIndex {
			violations = append(violations, "release workflow must order "+strings.Join(sequence, " -> "))
			break
		}
		previousIndex = index
	}
	if len(violations) > 0 {
		t.Fatalf("release workflow final manifest contract violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestGovernanceGuardCoversTrackedOldNamePaths(t *testing.T) {
	repoRoot := repoRootForTest(t)
	guardPath := filepath.Join(repoRoot, ".github", "tests", "asbcp-governance-guard.sh")
	guardBytes, err := os.ReadFile(guardPath)
	if err != nil {
		t.Fatalf("read governance guard: %v", err)
	}
	guard := string(guardBytes)
	requiredGuardTokens := []string{
		`cd "${ROOT}"`,
		`git -C "${ROOT}" ls-files`,
		`--ignored`,
		`find "${ROOT}"`,
		`-path "${ROOT}/.git"`,
		`-prune`,
		"canonical_forbidden_path_tokens",
		"active_path_exception_root",
		"active_path_exception_prefix",
		"path_to_scan",
		"scan_old_name_path",
		"manager",
		"manager-service/",
		"Go module root exception only",
		"manager-service/e2e",
		"E2E_MANAGER_",
		"ManagerURL",
		"ManagerBin",
		"manager-config",
		"sandbox-manager",
		"sandbox manager",
		"agentsmith-sandbox-manager",
		"cmd/manager",
		"cmd/cleaner",
		"build-manager",
		"test-asbcp-api.sh",
		"test-manager.sh",
		"api-reference-v2",
		"agentsmith-integration-contract-v2",
		"wait-for-minio.sh",
		"mbos-sandbox-v1",
	}
	var violations []string
	for _, token := range requiredGuardTokens {
		if !strings.Contains(guard, token) {
			violations = append(violations, "governance guard missing old-name path coverage token "+token)
		}
	}

	quickCmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "verify-release.sh"), "--quick")
	quickCmd.Dir = t.TempDir()
	quickCmd.Env = append(os.Environ(),
		"GITHUB_REF_NAME=main",
		"GITHUB_REF_TYPE=branch",
		"GITHUB_REF=refs/heads/main",
	)
	quickOutput, err := quickCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quick verifier must be callable outside repo root: %v\n%s", err, string(quickOutput))
	}
	if strings.Contains(string(quickOutput), "No such file or directory") {
		violations = append(violations, "quick verifier used repo-relative guard paths outside repo root:\n"+string(quickOutput))
	}
	for _, token := range []string{
		"scripts/test",
		"manager-service/scripts",
		"manager-service/e2e",
		"k8s/config",
		"k8s/overlays",
		"E2E_MANAGER_",
		"ManagerURL",
		"ManagerBin",
		"manager",
		"Manager",
		"MANAGER_URL",
		"SANDBOX_ID",
		"create_sandbox",
		"delete_sandbox",
		"private operator opt-in",
	} {
		if !strings.Contains(guard, token) {
			violations = append(violations, "governance guard missing active old smoke guard token "+token)
		}
	}

	output, err := exec.Command("git", "-C", repoRoot, "ls-files", "--cached", "--others", "--exclude-standard").CombinedOutput()
	if err != nil {
		t.Fatalf("list tracked paths: %v\n%s", err, string(output))
	}
	ignoredOutput, err := exec.Command("git", "-C", repoRoot, "ls-files", "--others", "--ignored", "--exclude-standard").CombinedOutput()
	if err != nil {
		t.Fatalf("list ignored paths: %v\n%s", err, string(ignoredOutput))
	}
	pathCandidates := append(splitLines(string(output)), splitLines(string(ignoredOutput))...)
	err = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		pathCandidates = append(pathCandidates, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo directories: %v", err)
	}
	forbiddenPathTokens := []string{
		"manager",
		"manager-config",
		"sandbox-manager",
		"sandbox manager",
		"agentsmith-sandbox-manager",
		"cmd/manager",
		"cmd/cleaner",
		"bin/cleaner",
		"api-reference-v2",
		"agentsmith-integration-contract-v2",
		"wait-for-minio.sh",
		"mbos-sandbox-v1",
	}
	for _, path := range pathCandidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat path %s: %v", path, err)
		}
		pathToScan := path
		if path == "manager-service" {
			pathToScan = ""
		} else if strings.HasPrefix(path, "manager-service/") {
			pathToScan = strings.TrimPrefix(path, "manager-service/")
		}
		for _, token := range forbiddenPathTokens {
			if strings.Contains(pathToScan, token) {
				violations = append(violations, path+": forbidden old ASBCP path token "+token)
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("old-name path guard violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestActiveSmokeAndOperatorSurfaceUsesASBCPTerms(t *testing.T) {
	repoRoot := repoRootForTest(t)
	scanRoots := []string{
		"scripts/test",
		"manager-service/scripts",
		"manager-service/e2e",
		"k8s/config",
		"k8s/overlays/dev/README.md",
		"k8s/overlays/dev/patches/README.md",
		"k8s/overlays/staging/README.md",
		"k8s/overlays/staging/patches/README.md",
		"k8s/overlays/production/README.md",
		"k8s/overlays/production/patches/README.md",
		"k8s/overlays/production/patches/security-hardening.yaml",
	}
	forbidden := []string{
		"E2E_MANAGER_",
		"ManagerURL",
		"ManagerBin",
		"manager",
		"Manager",
		"MANAGER_URL",
		"MANAGER_",
		"SANDBOX_ID",
		"create_sandbox",
		"delete_sandbox",
		"create sandbox",
		"Create sandbox",
		"Creating sandbox",
		"Failed to create sandbox",
		"Sandbox ID",
		"sandbox ID",
		"MANAGER_VERSION",
		"GC_VERSION",
		"Manager 副本",
		"Manager 可能",
		"Manager 测试",
		"Manager 代码",
	}

	var violations []string
	for _, rel := range scanRoots {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", rel, err)
		}
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			for _, token := range forbidden {
				if strings.Contains(string(data), token) {
					violations = append(violations, rel+": "+token)
				}
			}
			continue
		}
		err = filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".sh") && !strings.HasSuffix(entry.Name(), ".md") && !strings.HasSuffix(entry.Name(), ".example") && !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanRel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			content := string(data)
			for _, token := range forbidden {
				if strings.Contains(content, token) {
					violations = append(violations, filepath.ToSlash(scanRel)+": "+token)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", rel, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("active smoke/operator surface still uses retired manager/sandbox terms:\n%s", strings.Join(violations, "\n"))
	}
}

func TestProductionOverlayIsInternalOnlyByDefault(t *testing.T) {
	repoRoot := repoRootForTest(t)
	kustomizationPath := filepath.Join(repoRoot, "k8s", "overlays", "production", "kustomization.yaml")
	data, err := os.ReadFile(kustomizationPath)
	if err != nil {
		t.Fatalf("read production kustomization: %v", err)
	}
	content := string(data)
	forbiddenResources := []string{
		"access/ingress.yaml",
		"access/nodeport.yaml",
		"access/loadbalancer.yaml",
	}
	var violations []string
	for _, token := range forbiddenResources {
		if strings.Contains(content, token) {
			violations = append(violations, "production kustomization must not include "+token+" by default")
		}
	}

	readmeBytes, err := os.ReadFile(filepath.Join(repoRoot, "k8s", "overlays", "production", "README.md"))
	if err != nil {
		t.Fatalf("read production README: %v", err)
	}
	readme := string(readmeBytes)
	requiredReadme := []string{
		"internal-only by default",
		"private operator opt-in",
		"ClusterIP",
	}
	for _, token := range requiredReadme {
		if !strings.Contains(readme, token) {
			violations = append(violations, "production README missing "+token)
		}
	}
	forbiddenReadme := []string{
		"默认包含 Ingress",
		"包含 Ingress（默认）",
		"internet-facing",
	}
	for _, token := range forbiddenReadme {
		if strings.Contains(readme, token) {
			violations = append(violations, "production README suggests default public exposure via "+token)
		}
	}

	if kubectl, err := exec.LookPath("kubectl"); err == nil {
		overlayPath := filepath.Join(repoRoot, "k8s", "overlays", "production")
		out, err := exec.Command(kubectl, "kustomize", overlayPath).CombinedOutput()
		if err != nil {
			t.Fatalf("render production overlay: %v\n%s", err, string(out))
		}
		rendered := string(out)
		for _, token := range []string{"kind: Ingress", "type: NodePort", "type: LoadBalancer"} {
			if strings.Contains(rendered, token) {
				violations = append(violations, "production overlay renders external ASBCP route "+token)
			}
		}
	} else {
		t.Log("kubectl not found; production render exposure assertion skipped")
	}

	if len(violations) > 0 {
		t.Fatalf("production overlay exposure violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestReleaseRiskStatusComesFromRiskRegister(t *testing.T) {
	repoRoot := repoRootForTest(t)
	riskRegisterBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "RISK_REGISTER.md"))
	if err != nil {
		t.Fatalf("read risk register: %v", err)
	}
	riskRegister := string(riskRegisterBytes)
	requiredRiskTokens := []string{
		"| ID | Risk | Owner | Evidence | Release-blocking | Status |",
		"| R-001 |",
		"| No |",
		"non-release-blocking",
	}
	var violations []string
	for _, token := range requiredRiskTokens {
		if !strings.Contains(riskRegister, token) {
			violations = append(violations, "docs/RISK_REGISTER.md missing "+token)
		}
	}

	workflowBytes, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(workflowBytes)
	requiredWorkflowTokens := []string{
		"--risk-status-json",
		"dist/asbcp-risk-status.json",
		`risk_status_evidence["known_risk_status"]`,
		`risk_status_evidence["source"]`,
		`"known_risk_status_source"`,
	}
	for _, token := range requiredWorkflowTokens {
		if !strings.Contains(workflow, token) {
			violations = append(violations, "release workflow missing risk-register evidence token "+token)
		}
	}
	if strings.Contains(workflow, "KNOWN_RISK_STATUS:") || strings.Contains(workflow, `os.environ["KNOWN_RISK_STATUS"]`) {
		violations = append(violations, "release workflow must not use a static KNOWN_RISK_STATUS string")
	}

	manifestBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "release-evidence", "release-manifest.json"))
	if err != nil {
		t.Fatalf("read release evidence manifest: %v", err)
	}
	manifest := string(manifestBytes)
	for _, token := range []string{"known_risk_status_source", "docs/RISK_REGISTER.md", "release_blocking"} {
		if !strings.Contains(manifest, token) {
			violations = append(violations, "release manifest missing risk source token "+token)
		}
	}

	cmd := exec.Command("bash", "scripts/verify-release.sh", "--risk-status-json")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("risk status evidence command failed: %v\n%s", err, string(output))
	}
	var payload struct {
		KnownRiskStatus string   `json:"known_risk_status"`
		Source          string   `json:"source"`
		BlockingRisks   []string `json:"blocking_risks"`
		OpenRisks       []string `json:"open_risks"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("parse risk status evidence: %v\n%s", err, string(output))
	}
	if payload.Source != "docs/RISK_REGISTER.md release_blocking column" {
		violations = append(violations, "risk status evidence source must be docs/RISK_REGISTER.md release_blocking column")
	}
	if len(payload.BlockingRisks) != 0 {
		violations = append(violations, "risk status evidence has release-blocking risks: "+strings.Join(payload.BlockingRisks, ", "))
	}
	if len(payload.OpenRisks) == 0 {
		violations = append(violations, "risk status evidence must preserve open non-release-blocking risks")
	}
	if strings.Contains(strings.ToLower(payload.KnownRiskStatus), "no risks") {
		violations = append(violations, "known_risk_status must not claim there are no risks")
	}

	if len(violations) > 0 {
		t.Fatalf("risk register release status violations:\n%s", strings.Join(violations, "\n"))
	}
}

func splitLines(output string) []string {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(output), "\n")
}

func changelogSection(changelog string, tag string) string {
	lines := strings.Split(changelog, "\n")
	accepted := map[string]bool{
		tag:             true,
		"[" + tag + "]": true,
	}
	start := -1
	for index, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(line, "## "))
		headingName := strings.TrimSpace(strings.SplitN(heading, " - ", 2)[0])
		if accepted[headingName] {
			start = index + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for index := start; index < len(lines); index++ {
		if strings.HasPrefix(lines[index], "## ") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func TestRunnerImageIsNotASBCPActiveReleaseSurface(t *testing.T) {
	repoRoot := repoRootForTest(t)
	activeSurfaces := []string{
		"Makefile",
		"scripts/verify-release.sh",
		"scripts/lib/kind.sh",
		"k8s/base",
		"k8s/overlays",
		"k8s/scripts",
	}
	// scripts/lib/images.sh still exposes runner as an explicit non-active fixture.
	// ASBCP active release/dev paths must not build, push, or render that image.
	forbidden := []string{
		"sandbox-runner",
		"images/runner",
	}

	var violations []string
	for _, rel := range activeSurfaces {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("stat %s: %v", rel, err)
		}
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			for _, token := range forbidden {
				if strings.Contains(string(data), token) {
					violations = append(violations, rel+": "+token)
				}
			}
			continue
		}
		err = filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") && !strings.HasSuffix(entry.Name(), ".md") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanRel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			scanRel = filepath.ToSlash(scanRel)
			for _, token := range forbidden {
				if strings.Contains(string(data), token) {
					violations = append(violations, scanRel+": "+token)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", rel, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("runner image is a non-active fixture and must stay out of ASBCP active release surfaces:\n%s", strings.Join(violations, "\n"))
	}
}

func TestRunnerFixtureAllowlistIsDocumented(t *testing.T) {
	repoRoot := repoRootForTest(t)
	requiredMarkers := map[string]string{
		"sbx":                    "runner fixture",
		"scripts/lib/images.sh":  "non-active fixture",
		"scripts/lib/offline.sh": "non-active fixture",
	}

	var violations []string
	for rel, marker := range requiredMarkers {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(data), marker) {
			violations = append(violations, rel+": missing documented runner fixture marker "+marker)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("runner fixture allowlist must be explicit:\n%s", strings.Join(violations, "\n"))
	}
}

func TestActiveK8sFilenamesUseCanonicalASBCPNames(t *testing.T) {
	repoRoot := repoRootForTest(t)
	requiredFilenames := []string{
		"k8s/base/rbac-asbcp.yaml",
		"k8s/base/asbcp-deployment.yaml",
		"k8s/base/asbcp-service.yaml",
		"k8s/overlays/dev/patches/asbcp-replicas.yaml",
		"k8s/overlays/staging/patches/asbcp-replicas.yaml",
		"k8s/overlays/production/patches/asbcp-replicas.yaml",
	}
	for _, rel := range requiredFilenames {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("canonical ASBCP filename %s must exist: %v", rel, err)
		}
	}

	forbiddenFilenames := []string{
		"k8s/base/rbac-manager.yaml",
		"k8s/base/manager-deployment.yaml",
		"k8s/base/manager-service.yaml",
		"k8s/overlays/dev/patches/manager-replicas.yaml",
		"k8s/overlays/staging/patches/manager-replicas.yaml",
		"k8s/overlays/production/patches/manager-replicas.yaml",
	}
	for _, rel := range forbiddenFilenames {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err == nil {
			t.Fatalf("old active K8s manager filename must not exist: %s", rel)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", rel, err)
		}
	}

	requiredReferences := map[string][]string{
		"k8s/base/README.md": {
			"rbac-asbcp.yaml",
			"asbcp-deployment.yaml",
			"asbcp-service.yaml",
		},
		"k8s/base/kustomization.yaml": {
			"rbac-asbcp.yaml",
			"asbcp-deployment.yaml",
			"asbcp-service.yaml",
		},
		"k8s/overlays/dev/kustomization.yaml": {
			"patches/asbcp-replicas.yaml",
		},
		"k8s/overlays/staging/kustomization.yaml": {
			"patches/asbcp-replicas.yaml",
		},
		"k8s/overlays/production/kustomization.yaml": {
			"patches/asbcp-replicas.yaml",
		},
	}
	var violations []string
	for rel, markers := range requiredReferences {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := string(data)
		for _, marker := range markers {
			if !strings.Contains(content, marker) {
				violations = append(violations, rel+": missing "+marker)
			}
		}
	}

	forbiddenReferences := []string{
		"legacy filename compatibility",
		"Legacy filename compatibility",
		"rbac-manager.yaml",
		"manager-deployment.yaml",
		"manager-service.yaml",
		"manager-replicas.yaml",
	}
	for _, rel := range []string{
		"k8s/base/README.md",
		"k8s/base/kustomization.yaml",
		"k8s/overlays/dev/kustomization.yaml",
		"k8s/overlays/staging/kustomization.yaml",
		"k8s/overlays/production/kustomization.yaml",
		"k8s/overlays/dev/patches/README.md",
		"k8s/overlays/staging/patches/README.md",
		"k8s/overlays/production/patches/README.md",
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		content := string(data)
		for _, token := range forbiddenReferences {
			if strings.Contains(content, token) {
				violations = append(violations, rel+": "+token)
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("active K8s filenames must use canonical ASBCP names only:\n%s", strings.Join(violations, "\n"))
	}
}

func asbcpRenameScope(rel string) bool {
	if rel == "manager-service/internal/app/active_contract_guard_test.go" {
		return false
	}
	switch {
	case rel == "Makefile" || rel == "VERSION" || rel == "docker-compose.test.yaml" || rel == "sbx":
		return true
	case rel == "scripts/verify-release.sh":
		return true
	case rel == "docs/configuration.md":
		return true
	case rel == "manager-service/go.mod" || rel == "manager-service/go.sum":
		return true
	case rel == "manager-service/README.md" || rel == "manager-service/asbcp-config.example.yaml" || rel == "manager-service/manager-config.example.yaml":
		return true
	case strings.HasPrefix(rel, "manager-service/cmd/") && strings.HasSuffix(rel, ".go"):
		return true
	case strings.HasPrefix(rel, "manager-service/internal/") && strings.HasSuffix(rel, ".go"):
		return true
	case strings.HasPrefix(rel, "manager-service/integration/") && strings.HasSuffix(rel, ".go"):
		return true
	case strings.HasPrefix(rel, "manager-service/e2e/") && strings.HasSuffix(rel, ".go"):
		return true
	case strings.HasPrefix(rel, "manager-service/scripts/") && strings.HasSuffix(rel, ".sh"):
		return true
	case strings.HasPrefix(rel, "manager-service/") && strings.HasSuffix(rel, "Dockerfile"):
		return true
	case strings.HasPrefix(rel, "k8s/base/") && strings.HasSuffix(rel, ".md"):
		return true
	case strings.HasPrefix(rel, "k8s/base/") && (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")):
		return true
	case strings.HasPrefix(rel, "k8s/overlays/") && (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")):
		return true
	case strings.HasPrefix(rel, "k8s/scripts/") && strings.HasSuffix(rel, ".sh"):
		return true
	case strings.HasPrefix(rel, "scripts/lib/") && strings.HasSuffix(rel, ".sh"):
		return true
	case strings.HasPrefix(rel, "scripts/test/") && strings.HasSuffix(rel, ".sh"):
		return true
	default:
		return false
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
				t.Fatalf("%s overlay must patch ASBCP runtime AFSCP endpoint", overlay)
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

func TestKustomizeRendersASBCPReleaseControls(t *testing.T) {
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl is required to render kustomize overlays")
	}

	repoRoot := repoRootForTest(t)
	versionBytes, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	if err != nil {
		t.Fatalf("read ASBCP version: %v", err)
	}
	version := strings.TrimSpace(string(versionBytes))
	expectedImages := map[string]string{
		"dev":        "image: agentsmith-sandbox-control-plane:" + version,
		"staging":    "image: localhost:5001/agentsmith-sandbox-control-plane:" + version,
		"production": "image: localhost:5001/agentsmith-sandbox-control-plane:" + version,
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
				t.Fatalf("%s overlay must render ASBCP image %q", overlay, image)
			}
			if !strings.Contains(rendered, "app.kubernetes.io/version: "+version) {
				t.Fatalf("%s overlay must render app.kubernetes.io/version %q", overlay, version)
			}
			if strings.Contains(rendered, "app.kubernetes.io/version: 1.0.0") {
				t.Fatalf("%s overlay rendered stale app.kubernetes.io/version 1.0.0", overlay)
			}
			if !strings.Contains(rendered, "strategy:\n    type: Recreate") {
				t.Fatalf("%s overlay must render ASBCP with Recreate rollout strategy", overlay)
			}
			if !strings.Contains(rendered, "name: "+asbcpServiceName) {
				t.Fatalf("%s overlay must render ASBCP K8s identity %q", overlay, asbcpServiceName)
			}
			if !strings.Contains(rendered, "app.kubernetes.io/component: asbcp") {
				t.Fatalf("%s overlay must render ASBCP component label", overlay)
			}
			if strings.Contains(rendered, "sandbox-manager") || strings.Contains(rendered, "SANDBOX_MANAGER") {
				t.Fatalf("%s overlay rendered old sandbox-manager identity", overlay)
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
