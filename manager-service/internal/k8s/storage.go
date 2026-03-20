package k8s

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Client) EnsureSecret(ctx context.Context, namespace string, secret *v1.Secret) error {
	name := secret.GetName()
	if name == "" {
		return fmt.Errorf("secret name is required")
	}
	existing, err := c.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, createErr := c.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
			return createErr
		}
		return err
	}
	secret.SetResourceVersion(existing.GetResourceVersion())
	_, err = c.clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func (c *Client) GetSecret(ctx context.Context, namespace, name string) (*v1.Secret, error) {
	return c.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) DeleteSecret(ctx context.Context, namespace, name string) error {
	err := c.clientset.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) EnsurePersistentVolume(ctx context.Context, volume *v1.PersistentVolume) error {
	name := volume.GetName()
	if name == "" {
		return fmt.Errorf("persistent volume name is required")
	}
	existing, err := c.clientset.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, createErr := c.clientset.CoreV1().PersistentVolumes().Create(ctx, volume, metav1.CreateOptions{})
			return createErr
		}
		return err
	}
	volume.SetResourceVersion(existing.GetResourceVersion())
	_, err = c.clientset.CoreV1().PersistentVolumes().Update(ctx, volume, metav1.UpdateOptions{})
	return err
}

func (c *Client) GetPersistentVolume(ctx context.Context, name string) (*v1.PersistentVolume, error) {
	return c.clientset.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) DeletePersistentVolume(ctx context.Context, name string) error {
	err := c.clientset.CoreV1().PersistentVolumes().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) EnsurePersistentVolumeClaim(ctx context.Context, namespace string, claim *v1.PersistentVolumeClaim) error {
	name := claim.GetName()
	if name == "" {
		return fmt.Errorf("persistent volume claim name is required")
	}
	existing, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, createErr := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, claim, metav1.CreateOptions{})
			return createErr
		}
		return err
	}
	claim.SetResourceVersion(existing.GetResourceVersion())
	claim.Spec.VolumeName = existing.Spec.VolumeName
	_, err = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, claim, metav1.UpdateOptions{})
	return err
}

func (c *Client) GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*v1.PersistentVolumeClaim, error) {
	return c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error {
	err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}
