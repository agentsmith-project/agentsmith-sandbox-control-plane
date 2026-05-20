package workloadfacts

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

const (
	dnsLabelMaxLength = 63
	hashSuffixLength  = 12
)

// ObjectName returns a DNS-label-safe Kubernetes object name with a stable hash suffix.
func ObjectName(prefix string, rawParts ...string) string {
	safePrefix := dnsSlug(prefix, "asbcp")
	hash := identityHash(rawParts...)
	slug := dnsSlug(strings.Join(rawParts, "-"), "id")

	maxSlugLength := dnsLabelMaxLength - len(safePrefix) - len(hash) - 2
	if maxSlugLength < 1 {
		maxPrefixLength := dnsLabelMaxLength - len(hash) - 3
		if maxPrefixLength < 1 {
			maxPrefixLength = 1
		}
		safePrefix = trimDNSLabel(safePrefix, maxPrefixLength)
		maxSlugLength = dnsLabelMaxLength - len(safePrefix) - len(hash) - 2
	}
	slug = trimDNSLabel(slug, maxSlugLength)
	return safePrefix + "-" + slug + "-" + hash
}

// LabelValue returns a DNS-label-safe Kubernetes label value with a stable hash suffix.
func LabelValue(rawParts ...string) string {
	hash := identityHash(rawParts...)
	slug := dnsSlug(strings.Join(rawParts, "-"), "id")
	maxSlugLength := dnsLabelMaxLength - len(hash) - 1
	slug = trimDNSLabel(slug, maxSlugLength)
	return slug + "-" + hash
}

func identityHash(rawParts ...string) string {
	var b strings.Builder
	for _, part := range rawParts {
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteByte(':')
		b.WriteString(part)
		b.WriteByte('\x00')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:hashSuffixLength]
}

func isDNSLabel(value string) bool {
	if len(value) == 0 || len(value) > dnsLabelMaxLength {
		return false
	}
	for idx, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && idx > 0 && idx < len(value)-1:
		default:
			return false
		}
	}
	return true
}

func dnsSlug(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	previousHyphen := false
	for _, r := range value {
		isSafe := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isSafe {
			b.WriteRune(r)
			previousHyphen = false
			continue
		}
		if !previousHyphen {
			b.WriteByte('-')
			previousHyphen = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return fallback
	}
	return slug
}

func trimDNSLabel(value string, maxLength int) string {
	if maxLength < 1 {
		maxLength = 1
	}
	if len(value) <= maxLength {
		return value
	}
	value = strings.Trim(value[:maxLength], "-")
	if value == "" {
		return "x"
	}
	return value
}
