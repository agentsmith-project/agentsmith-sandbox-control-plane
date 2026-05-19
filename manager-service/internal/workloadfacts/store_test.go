package workloadfacts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestConfigMapGetMismatchedPayloadFailsClosed(t *testing.T) {
	key := Key{WorkspaceID: "ws-demo", ProjectID: "proj-demo", WorkloadID: "wl-demo"}
	payload := Fact{
		WorkspaceID:        key.WorkspaceID,
		ProjectID:          key.ProjectID,
		WorkloadID:         "other-workload",
		WorkspaceBindingID: "wmb-demo",
		MountBindingID:     "wmb-demo",
		PodName:            "workload-wl-demo",
	}
	cm := configMapWithPayload(t, configMapName(key), LabelsForFact(Fact{
		WorkspaceID:        key.WorkspaceID,
		ProjectID:          key.ProjectID,
		WorkloadID:         key.WorkloadID,
		WorkspaceBindingID: "wmb-demo",
		MountBindingID:     "wmb-demo",
	}), payload)
	store := NewConfigMapStore(newFakeConfigMaps(cm))

	_, err := store.Get(context.Background(), key)

	if err == nil {
		t.Fatalf("expected mismatched payload scope to fail closed")
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestConfigMapListByBindingMismatchedPayloadFailsClosed(t *testing.T) {
	requested := Fact{
		WorkspaceID:        "ws-demo",
		ProjectID:          "proj-demo",
		WorkloadID:         "wl-demo",
		WorkspaceBindingID: "wmb-demo",
		MountBindingID:     "wmb-demo",
		PodName:            "workload-wl-demo",
	}
	tests := []struct {
		name    string
		payload string
	}{
		{
			name: "workspace mismatch",
			payload: factJSON(t, Fact{
				WorkspaceID:        "other-ws",
				ProjectID:          requested.ProjectID,
				WorkloadID:         requested.WorkloadID,
				WorkspaceBindingID: requested.WorkspaceBindingID,
				MountBindingID:     requested.MountBindingID,
				PodName:            requested.PodName,
			}),
		},
		{
			name: "binding mismatch",
			payload: factJSON(t, Fact{
				WorkspaceID:        requested.WorkspaceID,
				ProjectID:          requested.ProjectID,
				WorkloadID:         requested.WorkloadID,
				WorkspaceBindingID: "wmb-other",
				MountBindingID:     "wmb-other",
				PodName:            requested.PodName,
			}),
		},
		{
			name:    "damaged payload",
			payload: "{not-json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:   configMapName(requested.Key()),
					Labels: LabelsForFact(requested),
				},
				Data: map[string]string{factDataKey: tt.payload},
			}
			store := NewConfigMapStore(newFakeConfigMaps(cm))

			_, err := store.ListByBinding(context.Background(), requested.WorkspaceID, requested.ProjectID, requested.BindingID())

			if err == nil {
				t.Fatalf("expected mismatched or damaged payload to fail closed")
			}
		})
	}
}

func configMapWithPayload(t *testing.T, name string, labels map[string]string, payload Fact) *v1.ConfigMap {
	t.Helper()
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Data: map[string]string{factDataKey: factJSON(t, payload)},
	}
}

func factJSON(t *testing.T, fact Fact) string {
	t.Helper()
	payload, err := json.Marshal(fact)
	if err != nil {
		t.Fatalf("marshal fact: %v", err)
	}
	return string(payload)
}

type fakeConfigMaps struct {
	items map[string]*v1.ConfigMap
}

func newFakeConfigMaps(items ...*v1.ConfigMap) *fakeConfigMaps {
	out := &fakeConfigMaps{items: make(map[string]*v1.ConfigMap, len(items))}
	for _, item := range items {
		out.items[item.Name] = item.DeepCopy()
	}
	return out
}

func (f *fakeConfigMaps) Create(_ context.Context, configMap *v1.ConfigMap, _ metav1.CreateOptions) (*v1.ConfigMap, error) {
	f.items[configMap.Name] = configMap.DeepCopy()
	return configMap.DeepCopy(), nil
}

func (f *fakeConfigMaps) Update(_ context.Context, configMap *v1.ConfigMap, _ metav1.UpdateOptions) (*v1.ConfigMap, error) {
	f.items[configMap.Name] = configMap.DeepCopy()
	return configMap.DeepCopy(), nil
}

func (f *fakeConfigMaps) Delete(context.Context, string, metav1.DeleteOptions) error {
	return nil
}

func (f *fakeConfigMaps) DeleteCollection(context.Context, metav1.DeleteOptions, metav1.ListOptions) error {
	return nil
}

func (f *fakeConfigMaps) Get(_ context.Context, name string, _ metav1.GetOptions) (*v1.ConfigMap, error) {
	item, ok := f.items[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name)
	}
	return item.DeepCopy(), nil
}

func (f *fakeConfigMaps) List(_ context.Context, opts metav1.ListOptions) (*v1.ConfigMapList, error) {
	selector, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		return nil, err
	}
	list := &v1.ConfigMapList{}
	for _, item := range f.items {
		if selector.Empty() || selector.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, *item.DeepCopy())
		}
	}
	return list, nil
}

func (f *fakeConfigMaps) Watch(context.Context, metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

func (f *fakeConfigMaps) Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*v1.ConfigMap, error) {
	return nil, nil
}

func (f *fakeConfigMaps) Apply(context.Context, *applycorev1.ConfigMapApplyConfiguration, metav1.ApplyOptions) (*v1.ConfigMap, error) {
	return nil, nil
}
