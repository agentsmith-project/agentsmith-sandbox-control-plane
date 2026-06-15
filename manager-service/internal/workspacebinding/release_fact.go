package workspacebinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workloadfacts"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	labelReleaseFactKind        = "mbos.io/fact-kind"
	labelReleaseFactKindValue   = "workspace-binding-release"
	releaseFactDataKey          = "release.json"
	annotationReleaseObservedAt = "mbos.io/release-observed-at"
)

var errWorkspaceBindingReleaseFactNotFound = errors.New("workspace binding release fact not found")

type WorkspaceBindingReleaseStore interface {
	Get(ctx context.Context, key WorkspaceBindingReleaseKey) (WorkspaceBindingReleaseFact, error)
	Save(ctx context.Context, fact WorkspaceBindingReleaseFact) error
}

type WorkspaceBindingReleaseKey struct {
	WorkspaceID string
	ProjectID   string
	BindingID   string
}

type WorkspaceBindingReleaseFact struct {
	WorkspaceID        string    `json:"workspace_id"`
	ProjectID          string    `json:"project_id"`
	BindingID          string    `json:"binding_id"`
	PVName             string    `json:"pv_name"`
	PVCName            string    `json:"pvc_name"`
	NamespaceID        string    `json:"namespace_id"`
	MountBindingID     string    `json:"mount_binding_id"`
	StorageDeleted     bool      `json:"storage_deleted"`
	TerminalStatusDone bool      `json:"terminal_status_done"`
	ObservedAt         time.Time `json:"observed_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (f WorkspaceBindingReleaseFact) Key() WorkspaceBindingReleaseKey {
	return WorkspaceBindingReleaseKey{WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, BindingID: f.BindingID}
}

func (f WorkspaceBindingReleaseFact) MountRef() workspaceBindingMountRef {
	return workspaceBindingMountRef{namespaceID: f.NamespaceID, mountBindingID: f.MountBindingID}
}

func (f WorkspaceBindingReleaseFact) Complete() bool {
	return f.StorageDeleted && f.TerminalStatusDone
}

type workspaceBindingReleaseConfigMapStore struct {
	configMaps corev1.ConfigMapInterface
}

func newWorkspaceBindingReleaseConfigMapStore(configMaps corev1.ConfigMapInterface) *workspaceBindingReleaseConfigMapStore {
	return &workspaceBindingReleaseConfigMapStore{configMaps: configMaps}
}

func (s *workspaceBindingReleaseConfigMapStore) Get(ctx context.Context, key WorkspaceBindingReleaseKey) (WorkspaceBindingReleaseFact, error) {
	cm, err := s.configMaps.Get(ctx, workspaceBindingReleaseConfigMapName(key), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return WorkspaceBindingReleaseFact{}, errWorkspaceBindingReleaseFactNotFound
		}
		return WorkspaceBindingReleaseFact{}, err
	}
	fact, err := workspaceBindingReleaseFactFromConfigMap(cm)
	if err != nil {
		return WorkspaceBindingReleaseFact{}, err
	}
	if err := validateWorkspaceBindingReleaseFactKey(fact, key); err != nil {
		return WorkspaceBindingReleaseFact{}, err
	}
	return fact, nil
}

func (s *workspaceBindingReleaseConfigMapStore) Save(ctx context.Context, fact WorkspaceBindingReleaseFact) error {
	normalizeWorkspaceBindingReleaseFact(&fact)
	cm, err := configMapForWorkspaceBindingReleaseFact(fact)
	if err != nil {
		return err
	}
	existing, err := s.configMaps.Get(ctx, cm.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, createErr := s.configMaps.Create(ctx, cm, metav1.CreateOptions{})
			return createErr
		}
		return err
	}
	cm.ResourceVersion = existing.ResourceVersion
	_, err = s.configMaps.Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func configMapForWorkspaceBindingReleaseFact(fact WorkspaceBindingReleaseFact) (*v1.ConfigMap, error) {
	payload, err := json.Marshal(fact)
	if err != nil {
		return nil, err
	}
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        workspaceBindingReleaseConfigMapName(fact.Key()),
			Labels:      labelsForWorkspaceBindingReleaseFact(fact),
			Annotations: annotationsForWorkspaceBindingReleaseFact(fact),
		},
		Data: map[string]string{releaseFactDataKey: string(payload)},
	}, nil
}

func workspaceBindingReleaseFactFromConfigMap(cm *v1.ConfigMap) (WorkspaceBindingReleaseFact, error) {
	if cm == nil || cm.Data == nil || strings.TrimSpace(cm.Data[releaseFactDataKey]) == "" {
		return WorkspaceBindingReleaseFact{}, fmt.Errorf("workspace binding release fact configmap is missing %s", releaseFactDataKey)
	}
	var fact WorkspaceBindingReleaseFact
	if err := json.Unmarshal([]byte(cm.Data[releaseFactDataKey]), &fact); err != nil {
		return WorkspaceBindingReleaseFact{}, err
	}
	normalizeWorkspaceBindingReleaseFact(&fact)
	return fact, nil
}

func validateWorkspaceBindingReleaseFactKey(fact WorkspaceBindingReleaseFact, key WorkspaceBindingReleaseKey) error {
	normalizeWorkspaceBindingReleaseFact(&fact)
	normalizeWorkspaceBindingReleaseKey(&key)
	if fact.WorkspaceID != key.WorkspaceID || fact.ProjectID != key.ProjectID || fact.BindingID != key.BindingID {
		return fmt.Errorf("workspace binding release fact payload scope mismatch")
	}
	return nil
}

func workspaceBindingReleaseConfigMapName(key WorkspaceBindingReleaseKey) string {
	normalizeWorkspaceBindingReleaseKey(&key)
	return workloadfacts.ObjectName("workspace-binding-release", key.WorkspaceID, key.ProjectID, key.BindingID)
}

func labelsForWorkspaceBindingReleaseFact(fact WorkspaceBindingReleaseFact) map[string]string {
	return map[string]string{
		workloadfacts.LabelManagedBy:      workloadfacts.LabelManagedByValue,
		labelReleaseFactKind:              labelReleaseFactKindValue,
		workloadfacts.LabelWorkspaceID:    workloadfacts.LabelValue(fact.WorkspaceID),
		workloadfacts.LabelProjectID:      workloadfacts.LabelValue(fact.ProjectID),
		workloadfacts.LabelMountBindingID: workloadfacts.LabelValue(fact.BindingID),
	}
}

func annotationsForWorkspaceBindingReleaseFact(fact WorkspaceBindingReleaseFact) map[string]string {
	return map[string]string{
		annotationWorkspaceID:         fact.WorkspaceID,
		annotationProjectID:           fact.ProjectID,
		annotationAFSCPNamespaceID:    fact.NamespaceID,
		annotationAFSCPMountBindingID: fact.MountBindingID,
		annotationReleaseObservedAt:   fact.ObservedAt.UTC().Format(time.RFC3339),
	}
}

func normalizeWorkspaceBindingReleaseFact(fact *WorkspaceBindingReleaseFact) {
	fact.WorkspaceID = strings.TrimSpace(fact.WorkspaceID)
	fact.ProjectID = strings.TrimSpace(fact.ProjectID)
	fact.BindingID = strings.TrimSpace(fact.BindingID)
	fact.PVName = strings.TrimSpace(fact.PVName)
	fact.PVCName = strings.TrimSpace(fact.PVCName)
	fact.NamespaceID = strings.TrimSpace(fact.NamespaceID)
	fact.MountBindingID = strings.TrimSpace(fact.MountBindingID)
	now := time.Now().UTC()
	if fact.ObservedAt.IsZero() {
		fact.ObservedAt = now
	}
	if fact.UpdatedAt.IsZero() {
		fact.UpdatedAt = now
	}
}

func normalizeWorkspaceBindingReleaseKey(key *WorkspaceBindingReleaseKey) {
	key.WorkspaceID = strings.TrimSpace(key.WorkspaceID)
	key.ProjectID = strings.TrimSpace(key.ProjectID)
	key.BindingID = strings.TrimSpace(key.BindingID)
}
