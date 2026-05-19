package workloadfacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	LabelManagedBy      = "app.kubernetes.io/managed-by"
	LabelManagedByValue = "agentsmith"
	LabelFactKind       = "mbos.io/fact-kind"
	LabelWorkspaceID    = "mbos.io/workspace-id"
	LabelProjectID      = "mbos.io/project-id"
	LabelMountBindingID = "mbos.io/mount-binding-id"

	AnnotationWorkspaceID         = "mbos.io/workspace-id"
	AnnotationProjectID           = "mbos.io/project-id"
	AnnotationWorkloadID          = "mbos.io/workload-id"
	AnnotationAFSCPNamespaceID    = "mbos.io/afscp-namespace-id"
	AnnotationAFSCPMountBindingID = "mbos.io/afscp-mount-binding-id"

	FactKindWorkloadTerminal = "workload-terminal"
)

const factDataKey = "fact.json"

var ErrNotFound = errors.New("workload fact not found")

type Key struct {
	WorkspaceID string
	ProjectID   string
	WorkloadID  string
}

type Fact struct {
	WorkspaceID        string    `json:"workspace_id"`
	ProjectID          string    `json:"project_id"`
	WorkloadID         string    `json:"workload_id"`
	WorkspaceBindingID string    `json:"workspace_binding_id,omitempty"`
	NamespaceID        string    `json:"namespace_id"`
	MountBindingID     string    `json:"mount_binding_id"`
	PodName            string    `json:"pod_name"`
	PodUID             string    `json:"pod_uid,omitempty"`
	ReleaseDone        bool      `json:"release_done"`
	PodDeleted         bool      `json:"pod_deleted"`
	TerminalStatusDone bool      `json:"terminal_status_done"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (f Fact) Key() Key {
	return Key{WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, WorkloadID: f.WorkloadID}
}

func (f Fact) BindingID() string {
	if strings.TrimSpace(f.WorkspaceBindingID) != "" {
		return strings.TrimSpace(f.WorkspaceBindingID)
	}
	return strings.TrimSpace(f.MountBindingID)
}

func (f Fact) Terminal() bool {
	return f.ReleaseDone && f.PodDeleted && f.TerminalStatusDone
}

type Source interface {
	ListByBinding(ctx context.Context, workspaceID, projectID, bindingID string) ([]Fact, error)
}

type Store interface {
	Source
	Get(ctx context.Context, key Key) (Fact, error)
	Save(ctx context.Context, fact Fact) error
}

type MemoryStore struct {
	mu    sync.Mutex
	facts map[Key]Fact
	err   error
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{facts: make(map[Key]Fact)}
}

func NewUnavailableMemoryStore(err error) *MemoryStore {
	if err == nil {
		err = errors.New("workload fact store unavailable")
	}
	return &MemoryStore{facts: make(map[Key]Fact), err: err}
}

func (s *MemoryStore) Get(_ context.Context, key Key) (Fact, error) {
	if s.err != nil {
		return Fact{}, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, ok := s.facts[key]
	if !ok {
		return Fact{}, ErrNotFound
	}
	return fact, nil
}

func (s *MemoryStore) Save(_ context.Context, fact Fact) error {
	if s.err != nil {
		return s.err
	}
	normalizeFact(&fact)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts[fact.Key()] = fact
	return nil
}

func (s *MemoryStore) ListByBinding(_ context.Context, workspaceID, projectID, bindingID string) ([]Fact, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Fact
	for _, fact := range s.facts {
		if fact.WorkspaceID == workspaceID && fact.ProjectID == projectID && fact.BindingID() == bindingID {
			out = append(out, fact)
		}
	}
	return out, nil
}

type ConfigMapStore struct {
	configMaps corev1.ConfigMapInterface
}

func NewConfigMapStore(configMaps corev1.ConfigMapInterface) *ConfigMapStore {
	return &ConfigMapStore{configMaps: configMaps}
}

func (s *ConfigMapStore) Get(ctx context.Context, key Key) (Fact, error) {
	cm, err := s.configMaps.Get(ctx, configMapName(key), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return Fact{}, ErrNotFound
		}
		return Fact{}, err
	}
	fact, err := factFromConfigMap(cm)
	if err != nil {
		return Fact{}, err
	}
	if err := validateFactKeyScope(fact, key); err != nil {
		return Fact{}, err
	}
	return fact, nil
}

func (s *ConfigMapStore) Save(ctx context.Context, fact Fact) error {
	normalizeFact(&fact)
	cm, err := configMapForFact(fact)
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

func (s *ConfigMapStore) ListByBinding(ctx context.Context, workspaceID, projectID, bindingID string) ([]Fact, error) {
	selector := strings.Join([]string{
		LabelFactKind + "=" + FactKindWorkloadTerminal,
		LabelWorkspaceID + "=" + LabelValue(workspaceID),
		LabelProjectID + "=" + LabelValue(projectID),
		LabelMountBindingID + "=" + LabelValue(bindingID),
	}, ",")
	list, err := s.configMaps.List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	out := make([]Fact, 0, len(list.Items))
	for idx := range list.Items {
		fact, err := factFromConfigMap(&list.Items[idx])
		if err != nil {
			return nil, err
		}
		if err := validateFactBindingScope(fact, workspaceID, projectID, bindingID); err != nil {
			return nil, err
		}
		out = append(out, fact)
	}
	return out, nil
}

func configMapForFact(fact Fact) (*v1.ConfigMap, error) {
	payload, err := json.Marshal(fact)
	if err != nil {
		return nil, err
	}
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        configMapName(fact.Key()),
			Labels:      LabelsForFact(fact),
			Annotations: RawAnnotationsForFact(fact),
		},
		Data: map[string]string{factDataKey: string(payload)},
	}, nil
}

func factFromConfigMap(cm *v1.ConfigMap) (Fact, error) {
	if cm == nil || cm.Data == nil || strings.TrimSpace(cm.Data[factDataKey]) == "" {
		return Fact{}, fmt.Errorf("workload fact configmap is missing %s", factDataKey)
	}
	var fact Fact
	if err := json.Unmarshal([]byte(cm.Data[factDataKey]), &fact); err != nil {
		return Fact{}, err
	}
	normalizeFact(&fact)
	return fact, nil
}

func validateFactKeyScope(fact Fact, key Key) error {
	normalizeFact(&fact)
	key.WorkspaceID = strings.TrimSpace(key.WorkspaceID)
	key.ProjectID = strings.TrimSpace(key.ProjectID)
	key.WorkloadID = strings.TrimSpace(key.WorkloadID)
	if fact.WorkspaceID != key.WorkspaceID || fact.ProjectID != key.ProjectID || fact.WorkloadID != key.WorkloadID {
		return fmt.Errorf("workload fact payload scope mismatch")
	}
	return nil
}

func validateFactBindingScope(fact Fact, workspaceID, projectID, bindingID string) error {
	normalizeFact(&fact)
	workspaceID = strings.TrimSpace(workspaceID)
	projectID = strings.TrimSpace(projectID)
	bindingID = strings.TrimSpace(bindingID)
	if fact.WorkspaceID != workspaceID || fact.ProjectID != projectID || fact.BindingID() != bindingID {
		return fmt.Errorf("workload fact payload scope mismatch")
	}
	return nil
}

func configMapName(key Key) string {
	return ObjectName("workload-fact", key.WorkspaceID, key.ProjectID, key.WorkloadID)
}

func LabelsForFact(fact Fact) map[string]string {
	return map[string]string{
		LabelManagedBy:      LabelManagedByValue,
		LabelFactKind:       FactKindWorkloadTerminal,
		LabelWorkspaceID:    LabelValue(fact.WorkspaceID),
		LabelProjectID:      LabelValue(fact.ProjectID),
		LabelMountBindingID: LabelValue(fact.BindingID()),
	}
}

func RawAnnotationsForFact(fact Fact) map[string]string {
	return map[string]string{
		AnnotationWorkspaceID:         fact.WorkspaceID,
		AnnotationProjectID:           fact.ProjectID,
		AnnotationWorkloadID:          fact.WorkloadID,
		AnnotationAFSCPNamespaceID:    fact.NamespaceID,
		AnnotationAFSCPMountBindingID: fact.MountBindingID,
	}
}

func normalizeFact(fact *Fact) {
	fact.WorkspaceID = strings.TrimSpace(fact.WorkspaceID)
	fact.ProjectID = strings.TrimSpace(fact.ProjectID)
	fact.WorkloadID = strings.TrimSpace(fact.WorkloadID)
	fact.WorkspaceBindingID = strings.TrimSpace(fact.WorkspaceBindingID)
	fact.NamespaceID = strings.TrimSpace(fact.NamespaceID)
	fact.MountBindingID = strings.TrimSpace(fact.MountBindingID)
	fact.PodName = strings.TrimSpace(fact.PodName)
	fact.PodUID = strings.TrimSpace(fact.PodUID)
	if fact.WorkspaceBindingID == "" {
		fact.WorkspaceBindingID = fact.MountBindingID
	}
	if fact.UpdatedAt.IsZero() {
		fact.UpdatedAt = time.Now().UTC()
	}
}
